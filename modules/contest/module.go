package contest

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"github.com/the-maldridge/authware"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

const (
	ModuleName = "CONTEST"

	PermissionAdmin = "ADMIN"
)

//go:embed ui/*
var efs embed.FS

type Contest struct {
	ID uint

	Name string

	// Frozen marks a contest as finished: while it is set the cloud
	// refuses uploads for it (score pushes) and its record and roster
	// can no longer be edited. Freezing is reversible.
	Frozen bool

	RegionID uint
	Region   team.Region
}

type ContestRoster struct {
	ID uint

	ContestID uint `gorm:"uniqueIndex:idx_contest_roster,composite"`
	Contest   Contest

	TeamID uint `gorm:"uniqueIndex:idx_contest_roster,composite"`
	Team   team.Team
}

// clearerEntry pairs a module's registered name with the contest data
// clearing capability it exposes, so the wipe happens in a stable
// order and the audit log names the module it came from.
type clearerEntry struct {
	name    string
	clearer modules.ContestDataClearer
}

// clearersFrom collects the contest data clearing capability from
// every resolved module that implements it. Modules are visited in
// name order so the wipe and its log output are deterministic. A nil
// deps (a module built in isolation) yields no clearers.
func clearersFrom(deps modules.ModuleDeps) []clearerEntry {
	if len(deps) == 0 {
		return nil
	}
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	slices.Sort(names)

	out := make([]clearerEntry, 0, len(names))
	for _, name := range names {
		if c, ok := deps[name].(modules.ContestDataClearer); ok {
			out = append(out, clearerEntry{name: name, clearer: c})
		}
	}
	return out
}

type Module struct {
	r  chi.Router
	db *db.DB
	ws *web.Server

	scope string

	// clearers are the modules holding data keyed to a contest, which
	// the Clear Data action erases.
	clearers []clearerEntry

	basePath string
}

func New(scope string, db *db.DB, ws *web.Server, deps modules.ModuleDeps) *Module {
	m := Module{
		r:  chi.NewRouter(),
		db: db,
		ws: ws,

		scope: scope,

		clearers: clearersFrom(deps),
	}

	if err := m.ws.InstallPermission(context.Background(), ModuleName, PermissionAdmin); err != nil {
		return nil
	}

	pAdmin := web.Permission{Module: ModuleName, Grant: PermissionAdmin}

	m.r.Route("/", func(r chi.Router) {
		r.Get("/", m.uiViewListContests)

		r.Get("/add", m.ws.GuardRoute(pAdmin, m.uiViewAddForm))
		r.Post("/add", m.ws.GuardRoute(pAdmin, m.uiViewUpsert))

		r.Get("/{id}/edit", m.ws.GuardRoute(pAdmin, m.uiViewEditForm))
		r.Post("/{id}/edit", m.ws.GuardRoute(pAdmin, m.uiViewUpsert))
		r.Post("/{id}/delete", m.ws.GuardRoute(pAdmin, m.uiViewDelete))
		r.Post("/{id}/freeze", m.ws.GuardRoute(pAdmin, m.uiViewToggleFrozen))
		r.Post("/{id}/unfreeze", m.ws.GuardRoute(pAdmin, m.uiViewToggleFrozen))
		r.Post("/{id}/clear", m.ws.GuardRoute(pAdmin, m.uiViewClearData))

		r.Get("/{id}/roster", m.uiViewRoster)
		r.Post("/{id}/roster/init", m.ws.GuardRoute(pAdmin, m.uiViewRosterInit))
		r.Get("/{id}/roster/add", m.ws.GuardRoute(pAdmin, m.uiViewRosterAddForm))
		r.Post("/{id}/roster/add", m.ws.GuardRoute(pAdmin, m.uiViewRosterAdd))
		r.Post("/{id}/roster/{team_id}/delete", m.ws.GuardRoute(pAdmin, m.uiViewRosterRemove))

		r.Get("/cloud/contests", m.cloudContestsData)
		r.Get("/cloud/contests/{id}/roster", m.cloudContestRosterData)
	})

	return &m
}

func (m *Module) Router() chi.Router {
	return m.r
}

func (m *Module) Migrate() error {
	if err := m.db.AutoMigrate(Contest{}); err != nil {
		return err
	}
	if err := m.db.AutoMigrate(ContestRoster{}); err != nil {
		return err
	}
	return nil
}

func (m *Module) TemplateLoader() pongo2.TemplateLoader {
	if m.ws != nil && m.ws.TemplateDebug() {
		return web.DebugTemplateLoader("modules/contest/ui/p2", efs)
	}
	sub, _ := fs.Sub(efs, "ui/p2")
	return pongo2.NewFSLoader(sub)
}

func (m *Module) NavList(prefix string) []web.NavElement {
	m.basePath = prefix

	children := []web.NavChild{{
		Text:   "List",
		Target: path.Join(prefix, "/"),
	}}

	return []web.NavElement{{
		Text:     "Contest",
		Weight:   98,
		Children: children,
	}}
}

// contestDataCounts merges the per-module contest data counts into one
// map keyed by contest ID. A module whose counts query fails is logged
// and skipped: the list page is contest administration's front door
// and must stay usable even when some other module's data is
// unreadable.
func (m *Module) contestDataCounts(ctx context.Context) map[uint][]modules.ContestDataRow {
	out := make(map[uint][]modules.ContestDataRow)
	for _, e := range m.clearers {
		counts, err := e.clearer.ContestDataCounts(ctx)
		if err != nil {
			slog.Error("Error counting contest data", "error", err, "module", e.name)
			continue
		}
		for id, rows := range counts {
			out[id] = append(out[id], rows...)
		}
	}
	return out
}

// contestDataRow is one row of the clear-confirmation itemization: a
// labelled count plus the running total, which pongo2 cannot compute
// itself.
type contestDataRow struct {
	Label string
	Rows  int64
}

// clearTarget describes one contest's clear-confirmation state for
// the list page.
type clearTarget struct {
	Total int64
	Rows  []contestDataRow
}

// contestListItem is one row of the contest list with its clear state
// attached. pongo2 cannot index a map keyed by uint, so the clear
// state travels on the row itself: the embedded Contest promotes every
// existing field, so templates keep reading contest.Name and friends
// while contest.Clear reaches the new state.
type contestListItem struct {
	Contest
	Clear clearTarget
}

func (m *Module) uiViewListContests(w http.ResponseWriter, r *http.Request) {
	contests, err := gorm.G[Contest](m.db.DB).
		Preload("Region", nil).
		Find(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	sort.Slice(contests, func(i, j int) bool {
		return contests[i].Name < contests[j].Name
	})

	counts := m.contestDataCounts(r.Context())
	items := make([]contestListItem, 0, len(contests))
	for _, c := range contests {
		t := clearTarget{Rows: make([]contestDataRow, 0, len(counts[c.ID]))}
		for _, row := range counts[c.ID] {
			if row.Rows == 0 {
				continue
			}
			t.Rows = append(t.Rows, contestDataRow{Label: row.Label, Rows: row.Rows})
			t.Total += row.Rows
		}
		items = append(items, contestListItem{Contest: c, Clear: t})
	}

	// The redirect after a clear names the contest by ID; show its name
	// instead, resolving best-effort so a contest deleted in the
	// meantime still yields a usable notification.
	clearedFor := ""
	if id := m.ws.StrToUint(r.URL.Query().Get("cleared_for")); id != 0 {
		for _, c := range contests {
			if c.ID == id {
				clearedFor = c.Name
				break
			}
		}
	}

	m.ws.DoTemplate(w, r, "views/contest/list.p2", pongo2.Context{
		"contests":   items,
		"cleared":    m.ws.StrToUint(r.URL.Query().Get("cleared")),
		"clearedFor": clearedFor,
		"clearError": r.URL.Query().Get("clear_error"),
	})
}

func (m *Module) uiViewAddForm(w http.ResponseWriter, r *http.Request) {
	regions, err := gorm.G[team.Region](m.db.DB).Find(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	sort.Slice(regions, func(i, j int) bool {
		return regions[i].Name < regions[j].Name
	})

	m.ws.DoTemplate(w, r, "views/contest/form.p2", pongo2.Context{
		"contest": Contest{},
		"regions": regions,
	})
}

func (m *Module) uiViewEditForm(w http.ResponseWriter, r *http.Request) {
	c, err := gorm.G[Contest](m.db.DB).
		Preload("Region", nil).
		Where(&Contest{ID: m.ws.StrToUint(chi.URLParam(r, "id"))}).
		First(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	regions, err := gorm.G[team.Region](m.db.DB).Find(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	sort.Slice(regions, func(i, j int) bool {
		return regions[i].Name < regions[j].Name
	})

	m.ws.DoTemplate(w, r, "views/contest/form.p2", pongo2.Context{
		"contest": c,
		"regions": regions,
	})
}

// ErrContestFrozen is returned when an operation is refused because
// the contest has been frozen. Errors wrapping it are surfaced to the
// operator as the reason an action was rejected.
var ErrContestFrozen = errors.New("contest is frozen")

// frozenGuard loads the contest named in the URL and reports whether
// the request may continue. When the contest is frozen it renders the
// error page, so handlers return immediately.
func (m *Module) frozenGuard(w http.ResponseWriter, r *http.Request) bool {
	id := m.ws.StrToUint(chi.URLParam(r, "id"))

	c, err := m.loadContest(r.Context(), id)
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return false
	}
	if c.Frozen {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{
			"error": fmt.Errorf("%w: %s is frozen; unfreeze it to make changes", ErrContestFrozen, c.Name),
		})
		return false
	}
	return true
}

// uiViewToggleFrozen freezes or unfreezes a contest. The two routes
// share this handler: the path suffix decides the new state, so the
// action stays a plain POST form with no hidden fields.
func (m *Module) uiViewToggleFrozen(w http.ResponseWriter, r *http.Request) {
	id := m.ws.StrToUint(chi.URLParam(r, "id"))
	frozen := strings.HasSuffix(r.URL.Path, "/freeze")

	c, err := m.loadContest(r.Context(), id)
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	if _, err := gorm.G[Contest](m.db.DB).
		Where(&Contest{ID: id}).
		Update(r.Context(), "Frozen", frozen); err != nil {
		slog.Error("Error changing contest frozen state", "error", err, "contest", id)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	slog.Info("Contest frozen state changed",
		"contest", id, "name", c.Name, "frozen", frozen,
		"operator", m.operatorName(r))

	http.Redirect(w, r, m.basePath, http.StatusSeeOther)
}

// operatorName returns the login of the user performing a request,
// for audit logging of freeze transitions.
func (m *Module) operatorName(r *http.Request) string {
	if u, ok := r.Context().Value(authware.UserKey{}).(authware.User); ok {
		return u.Identity
	}
	return "anonymous"
}

func (m *Module) uiViewUpsert(w http.ResponseWriter, r *http.Request) {
	// An edit of a frozen contest is refused; creating a new contest
	// has nothing to be frozen yet.
	if m.ws.StrToUint(chi.URLParam(r, "id")) != 0 {
		if !m.frozenGuard(w, r) {
			return
		}
	}

	r.ParseForm()
	c := Contest{
		ID:       m.ws.StrToUint(chi.URLParam(r, "id")),
		Name:     r.FormValue("contest_name"),
		RegionID: m.ws.StrToUint(r.FormValue("contest_region")),
	}

	if err := db.InsertOrUpdate[Contest](r.Context(), m.db.DB, &c); err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	http.Redirect(w, r, m.basePath, http.StatusSeeOther)
}

func (m *Module) uiViewDelete(w http.ResponseWriter, r *http.Request) {
	id := m.ws.StrToUint(chi.URLParam(r, "id"))

	if !m.frozenGuard(w, r) {
		return
	}

	if _, err := gorm.G[Contest](m.db.DB).Where(&Contest{ID: id}).First(r.Context()); err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	if err := m.db.Delete(&Contest{ID: id}).Error; err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	if err := m.db.Where("contest_id = ?", id).Delete(&ContestRoster{}).Error; err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	http.Redirect(w, r, m.basePath, http.StatusSeeOther)
}

func (m *Module) rosterPath(id uint) string {
	return path.Join(m.basePath, strconv.FormatUint(uint64(id), 10), "/roster")
}

// clearErrorURL builds the list-page URL carrying a failed clear
// attempt, so the operator sees why nothing was deleted.
func (m *Module) clearErrorURL(reason string) string {
	return m.basePath + "?clear_error=" + reason
}

// uiViewClearData erases every row the other modules hold for one
// contest. The action is irreversible, so the operator must type the
// contest's name to confirm it; the check runs server-side, and the
// modal's disabled button is friction rather than security.
//
// A frozen contest is refused: freezing declares its results final,
// which is why editing and deleting it are already refused. Clearing
// becomes available again by unfreezing.
func (m *Module) uiViewClearData(w http.ResponseWriter, r *http.Request) {
	id := m.ws.StrToUint(chi.URLParam(r, "id"))

	if !m.frozenGuard(w, r) {
		return
	}

	c, err := m.loadContest(r.Context(), id)
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	r.ParseForm()
	if strings.TrimSpace(r.FormValue("confirm_name")) != c.Name {
		http.Redirect(w, r, m.clearErrorURL("name"), http.StatusSeeOther)
		return
	}

	// One transaction for every module, so a failure part-way through
	// leaves the contest's stored data untouched rather than
	// half-erased.
	type clearedModule struct {
		name string
		rows int64
	}
	var cleared []clearedModule
	err = m.db.Transaction(func(tx *gorm.DB) error {
		cleared = cleared[:0]
		for _, e := range m.clearers {
			rows, err := e.clearer.ClearContestData(r.Context(), tx, id)
			if err != nil {
				return fmt.Errorf("clearing %s data for contest %d: %w", e.name, id, err)
			}
			cleared = append(cleared, clearedModule{name: e.name, rows: rows})
		}
		return nil
	})
	if err != nil {
		slog.Error("Error clearing contest data", "error", err, "contest", id)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	// Logged only once committed, so a rolled back attempt never
	// claims data was removed.
	var total int64
	for _, done := range cleared {
		total += done.rows
		slog.Info("Contest data cleared",
			"contest", id, "name", c.Name,
			"module", done.name, "rows", done.rows,
			"operator", m.operatorName(r))
	}

	http.Redirect(w, r, m.basePath+"?cleared="+strconv.FormatInt(total, 10)+
		"&cleared_for="+strconv.FormatUint(uint64(id), 10), http.StatusSeeOther)
}

func (m *Module) loadContest(ctx context.Context, id uint) (Contest, error) {
	return gorm.G[Contest](m.db.DB).
		Preload("Region", nil).
		Where(&Contest{ID: id}).
		First(ctx)
}

func (m *Module) uiViewRoster(w http.ResponseWriter, r *http.Request) {
	id := m.ws.StrToUint(chi.URLParam(r, "id"))

	c, err := m.loadContest(r.Context(), id)
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	roster, err := gorm.G[ContestRoster](m.db.DB).
		Where(&ContestRoster{ContestID: id}).
		Preload("Team", nil).
		Preload("Team.Division", nil).
		Preload("Team.Region", nil).
		Find(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	sort.Slice(roster, func(i, j int) bool {
		return roster[i].Team.Number < roster[j].Team.Number
	})

	m.ws.DoTemplate(w, r, "views/contest/roster.p2", pongo2.Context{
		"contest":  c,
		"roster":   roster,
		"basePath": m.basePath,
	})
}

func (m *Module) uiViewRosterInit(w http.ResponseWriter, r *http.Request) {
	id := m.ws.StrToUint(chi.URLParam(r, "id"))

	if !m.frozenGuard(w, r) {
		return
	}

	c, err := m.loadContest(r.Context(), id)
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	teams, err := gorm.G[team.Team](m.db.DB).
		Where("region_id = ? AND disabled = ?", c.RegionID, false).
		Find(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	for _, t := range teams {
		existing, err := gorm.G[ContestRoster](m.db.DB).
			Where(&ContestRoster{ContestID: id, TeamID: t.ID}).
			Count(r.Context(), "*")
		if err != nil {
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return
		}
		if existing > 0 {
			continue
		}
		if err := m.db.Create(&ContestRoster{ContestID: id, TeamID: t.ID}).Error; err != nil {
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return
		}
	}

	http.Redirect(w, r, m.rosterPath(id), http.StatusSeeOther)
}

func (m *Module) uiViewRosterAddForm(w http.ResponseWriter, r *http.Request) {
	id := m.ws.StrToUint(chi.URLParam(r, "id"))

	c, err := m.loadContest(r.Context(), id)
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	roster, err := gorm.G[ContestRoster](m.db.DB).
		Where(&ContestRoster{ContestID: id}).
		Find(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	teamIDs := map[uint]bool{}
	for _, row := range roster {
		teamIDs[row.TeamID] = true
	}

	teams, err := gorm.G[team.Team](m.db.DB).
		Preload("Region", nil).
		Find(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	candidates := make([]team.Team, 0, len(teams))
	for _, t := range teams {
		if !teamIDs[t.ID] {
			candidates = append(candidates, t)
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Number < candidates[j].Number
	})

	m.ws.DoTemplate(w, r, "views/contest/roster_add.p2", pongo2.Context{
		"contest":    c,
		"teams":      candidates,
		"rosterPath": m.rosterPath(id),
	})
}

func (m *Module) uiViewRosterAdd(w http.ResponseWriter, r *http.Request) {
	id := m.ws.StrToUint(chi.URLParam(r, "id"))

	if !m.frozenGuard(w, r) {
		return
	}

	r.ParseForm()
	teamID := m.ws.StrToUint(r.FormValue("roster_team"))

	if _, err := gorm.G[team.Team](m.db.DB).Where(&team.Team{ID: teamID}).First(r.Context()); err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	existing, err := gorm.G[ContestRoster](m.db.DB).
		Where(&ContestRoster{ContestID: id, TeamID: teamID}).
		Count(r.Context(), "*")
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	if existing > 0 {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": errors.New("team already on roster")})
		return
	}

	if err := m.db.Create(&ContestRoster{ContestID: id, TeamID: teamID}).Error; err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	http.Redirect(w, r, m.rosterPath(id), http.StatusSeeOther)
}

func (m *Module) uiViewRosterRemove(w http.ResponseWriter, r *http.Request) {
	id := m.ws.StrToUint(chi.URLParam(r, "id"))
	teamID := m.ws.StrToUint(chi.URLParam(r, "team_id"))

	// frozenGuard also confirms the contest exists.
	if !m.frozenGuard(w, r) {
		return
	}

	if err := m.db.Where(&ContestRoster{ContestID: id, TeamID: teamID}).
		Delete(&ContestRoster{}).Error; err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	http.Redirect(w, r, m.rosterPath(id), http.StatusSeeOther)
}
