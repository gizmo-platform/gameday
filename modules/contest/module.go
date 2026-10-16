package contest

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strconv"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
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

type Module struct {
	r  chi.Router
	db *db.DB
	ws *web.Server

	scope string

	basePath string
}

func New(scope string, db *db.DB, ws *web.Server, _ modules.ModuleDeps) *Module {
	m := Module{
		r:  chi.NewRouter(),
		db: db,
		ws: ws,

		scope: scope,
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

	m.ws.DoTemplate(w, r, "views/contest/list.p2", pongo2.Context{"contests": contests})
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

func (m *Module) uiViewUpsert(w http.ResponseWriter, r *http.Request) {
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

	if _, err := m.loadContest(r.Context(), id); err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
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

	if _, err := m.loadContest(r.Context(), id); err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	if err := m.db.Where(&ContestRoster{ContestID: id, TeamID: teamID}).
		Delete(&ContestRoster{}).Error; err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	http.Redirect(w, r, m.rosterPath(id), http.StatusSeeOther)
}
