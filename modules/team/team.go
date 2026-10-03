package team

import (
	"context"
	"embed"
	"encoding/csv"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/pkg/cloud"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

const (
	ModuleName = "TEAM"

	PermissionAdmin = "ADMIN"

	DynamicDivisionStartID = 100
)

//go:embed ui/*
var efs embed.FS

// Module struct.
type Module struct {
	r  chi.Router
	db *db.DB
	ws *web.Server

	basePath string

	staticDivisions []Division
	cloudClient     *cloud.Client
}

type Division struct {
	ID        uint
	Name      string `gorm:"uniqueIndex"`
	Static    bool
	NoCompact bool // When true, this division's matches will not be compacted with others
}

type Region struct {
	ID   uint
	Name string `gorm:"uniqueIndex"`
}

type Team struct {
	ID uint

	Name       string
	Number     int `gorm:"uniqueIndex"`
	DivisionID uint
	Division   Division
	RegionID   uint
	Region     Region

	// When true, the team is excluded from default roster queries.
	Disabled bool
}

func New(db *db.DB, ws *web.Server, _ modules.ModuleDeps) *Module {
	m := &Module{
		r:           chi.NewRouter(),
		db:          db,
		ws:          ws,
		cloudClient: cloud.New(),
	}

	if err := m.ws.InstallPermission(context.Background(), ModuleName, PermissionAdmin); err != nil {
		return nil
	}

	pAdmin := web.Permission{Module: ModuleName, Grant: PermissionAdmin}

	m.r.Route("/", func(r chi.Router) {
		r.Get("/", m.uiViewListTeams)
		r.Get("/import", m.ws.GuardRoute(pAdmin, m.uiViewImportTeams))
		r.Post("/import", m.ws.GuardRoute(pAdmin, m.uiViewImportTeamsSubmit))
		r.Get("/add", m.ws.GuardRoute(pAdmin, m.uiViewAddForm))
		r.Post("/add", m.ws.GuardRoute(pAdmin, m.uiViewUpsert))

		r.Get("/{id}/edit", m.ws.GuardRoute(pAdmin, m.uiViewEditForm))
		r.Post("/{id}/edit", m.ws.GuardRoute(pAdmin, m.uiViewUpsert))

		r.Get("/divisions", m.uiViewListDivisions)
		r.Get("/divisions/add", m.ws.GuardRoute(pAdmin, m.uiViewAddDivision))
		r.Post("/divisions/add", m.ws.GuardRoute(pAdmin, m.uiViewUpsertDivision))
		r.Get("/divisions/{division_id}/edit", m.ws.GuardRoute(pAdmin, m.uiViewEditDivision))
		r.Post("/divisions/{division_id}/edit", m.ws.GuardRoute(pAdmin, m.uiViewUpsertDivision))
		r.Post("/divisions/{division_id}/delete", m.ws.GuardRoute(pAdmin, m.uiViewDeleteDivision))

		r.Get("/regions", m.uiViewListRegions)
		r.Get("/regions/add", m.ws.GuardRoute(pAdmin, m.uiViewAddRegion))
		r.Post("/regions/add", m.ws.GuardRoute(pAdmin, m.uiViewUpsertRegion))
		r.Get("/regions/{region_id}/edit", m.ws.GuardRoute(pAdmin, m.uiViewEditRegion))
		r.Post("/regions/{region_id}/edit", m.ws.GuardRoute(pAdmin, m.uiViewUpsertRegion))
		r.Post("/regions/{region_id}/delete", m.ws.GuardRoute(pAdmin, m.uiViewDeleteRegion))

		r.Get("/import-cloud", m.ws.GuardRoute(pAdmin, m.uiViewImportCloud))
		r.Post("/import-cloud", m.ws.GuardRoute(pAdmin, m.uiViewImportCloudSubmit))

		r.Get("/cloud/regions", m.cloudRegionsData)
		r.Get("/cloud/teams", m.cloudTeamsData)
	})

	pongo2.RegisterFilter("teamList", filterTeamList)

	return m
}

func (m *Module) Router() chi.Router {
	return m.r
}

// RegisterDivision registers a static division with an ID < 100.
// Modules can use this to guarantee specific divisions exist before Migrate runs.
func (m *Module) RegisterDivision(div Division) {
	if div.ID >= DynamicDivisionStartID {
		return
	}
	m.staticDivisions = append(m.staticDivisions, div)
}

func (m *Module) Migrate() error {
	// Auto-migrate Division first
	if err := m.db.AutoMigrate(Division{}); err != nil {
		return err
	}

	// Auto-migrate Region
	if err := m.db.AutoMigrate(Region{}); err != nil {
		return err
	}

	// Seed all static divisions with their explicit IDs
	seeded := make(map[uint]struct{})
	for _, div := range m.staticDivisions {
		existing, err := gorm.G[Division](m.db.DB).Where(&Division{ID: div.ID}).First(context.Background())
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if existing.ID == 0 {
			div.Static = true
			if err := m.db.Create(&div).Error; err != nil {
				return err
			}
		} else if !existing.Static {
			// Mark existing record as static if it matched a registered division
			if err := m.db.Model(&Division{}).Where(&Division{ID: div.ID}).Updates(map[string]interface{}{"static": true}).Error; err != nil {
				return err
			}
		}
		seeded[div.ID] = struct{}{}
	}

	// Ensure "Open" division always exists (unless already seeded above)
	openDivision, err := gorm.G[Division](m.db.DB).Where(&Division{Name: "Open"}).First(context.Background())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if openDivision.ID == 0 {
		// Find the next available ID below DynamicDivisionStartID
		nextID := uint(1)
		for ; nextID < DynamicDivisionStartID; nextID++ {
			if _, ok := seeded[nextID]; !ok {
				break
			}
		}
		if err := m.db.Create(&Division{Name: "Open", Static: true, ID: nextID}).Error; err != nil {
			return err
		}
	} else if !openDivision.Static {
		// Backfill: if Open exists but isn't marked static and has ID < 100, mark it
		if openDivision.ID < DynamicDivisionStartID {
			if err := m.db.Model(&Division{}).Where(&Division{ID: openDivision.ID}).Updates(map[string]interface{}{"static": true}).Error; err != nil {
				return err
			}
		}
	}

	// Advance the SQLite auto-increment sequence so dynamic divisions start at 100
	m.db.Exec("INSERT OR IGNORE INTO divisions (id, name, static) VALUES (?, ?, ?)", DynamicDivisionStartID, "", false)
	m.db.Exec("DELETE FROM divisions WHERE id = ?", DynamicDivisionStartID)

	// Auto-migrate Team
	if err := m.db.AutoMigrate(Team{}); err != nil {
		return err
	}

	return nil
}

func (m *Module) TemplateLoader() pongo2.TemplateLoader {
	if m.ws != nil && m.ws.TemplateDebug() {
		return web.DebugTemplateLoader("modules/team/ui/p2", efs)
	}
	sub, _ := fs.Sub(efs, "ui/p2")
	return pongo2.NewFSLoader(sub)
}

func (m *Module) NavList(prefix string) []web.NavElement {
	m.basePath = prefix
	return []web.NavElement{{
		Text:   "Team",
		Weight: 80,
		Children: []web.NavChild{{
			Text:   "List",
			Target: path.Join(prefix, "/"),
		}, {
			Text:   "Divisions",
			Target: path.Join(prefix, "/divisions"),
		}, {
			Text:   "Regions",
			Target: path.Join(prefix, "/regions"),
		}, {
			Text:       "Bulk Import",
			Target:     path.Join(prefix, "/import"),
			Permission: web.Permission{Module: ModuleName, Grant: PermissionAdmin},
		}, {
			Text:       "Cloud Import",
			Target:     path.Join(prefix, "/import-cloud"),
			Permission: web.Permission{Module: ModuleName, Grant: PermissionAdmin},
		}},
	}}
}

// ListTeams returns a selection of teams matching the filter.
// Default (roster) queries, indicated by a zero ID, exclude disabled
// teams; a specific ID filter returns that team regardless of its
// disabled state.
func (m *Module) ListTeams(ctx context.Context, filter Team) ([]Team, error) {
	q := gorm.G[Team](m.db.DB).Preload("Division", nil).Preload("Region", nil).Where(filter)
	if filter.ID == 0 {
		q = q.Where("disabled = ?", false)
	}
	out, err := q.Find(ctx)
	return out, err
}

func filterTeamList(in, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	v, ok := in.Interface().([]Team)
	if !ok {
		return nil, &pongo2.Error{Sender: "teamList", OrigError: errors.New("team list was not a list")}
	}
	return pongo2.AsValue(v[param.Integer()].Name), nil
}

// uiViewListTeams renders the admin team list with server-side search,
// division/region facet filters, and pagination. Disabled teams remain
// visible so they can be re-enabled; this differs from ListTeams, which
// excludes them on a default (roster) filter.
func (m *Module) uiViewListTeams(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	divID := m.ws.StrToUint(r.URL.Query().Get("division_id"))
	regID := m.ws.StrToUint(r.URL.Query().Get("region_id"))

	// Compose the active filter predicates once so the total (Count) and the
	// paged rows (Find) are always derived from the same constraint set.
	var (
		cond string
		args []interface{}
	)
	if q != "" {
		p := "%" + q + "%"
		cond = "(name LIKE ? OR CAST(number AS TEXT) LIKE ?)"
		args = []interface{}{p, p}
	}
	if divID != 0 {
		if cond != "" {
			cond += " AND "
		}
		cond += "division_id = ?"
		args = append(args, divID)
	}
	if regID != 0 {
		if cond != "" {
			cond += " AND "
		}
		cond += "region_id = ?"
		args = append(args, regID)
	}

	// gorm's generic G[T] re-sessions with NewDB, so WHERE/LIMIT/OFFSET applied
	// to a raw *gorm.DB before G[T] are dropped. Apply the filters through the
	// generic chain (ChainInterface) so they survive; Count and Find share it.
	base := gorm.G[Team](m.db.DB).
		Preload("Division", nil).
		Preload("Region", nil)
	if cond != "" {
		base = base.Where(cond, args...)
	}

	// Count over the full filtered set (no limit) so the total and the page
	// always agree. Preload does not affect the count value.
	total, err := base.Count(ctx, "*")
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	// web.PageParams mirrors web.Paginate's clamping; the generic chain takes
	// explicit Offset/Limit rather than a *gorm.DB scope.
	page, pageSize := web.PageParams(r)
	teams, err := base.
		Order("number ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(ctx)
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	divisions, err := gorm.G[Division](m.db.DB).Find(ctx)
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	sort.Slice(divisions, func(i, j int) bool {
		return divisions[i].Name < divisions[j].Name
	})

	regions, err := gorm.G[Region](m.db.DB).Find(ctx)
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	sort.Slice(regions, func(i, j int) bool {
		return regions[i].Name < regions[j].Name
	})

	pages := 0
	if total > 0 {
		pages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	window := pageWindow(page, pages)

	// The filter form drops "page" (resets to page 1); pager links re-append
	// it. Preserve the other query params on the pager links via filtersQS.
	fq := r.URL.Query()
	fq.Del("page")

	prevPage, nextPage := page-1, page+1
	m.ws.DoTemplate(w, r, "views/team/list.p2", pongo2.Context{
		"teams":      teams,
		"divisions":  divisions,
		"regions":    regions,
		"q":          q,
		"divisionID": divID,
		"regionID":   regID,
		"filtersQS":  fq.Encode(),
		"total":      total,
		"page":       page,
		"pages":      pages,
		"pageSize":   pageSize,
		"prevPage":   prevPage,
		"nextPage":   nextPage,
		"pageWindow": window,
	})
}

// pageWindow returns the page numbers to display as links in the pager,
// centered on page and clamped to [1, pages]. At most (radius*2 + 1)
// numbers are shown; the ellipsis logic is left to the template which only
// renders the window we hand it.
func pageWindow(page, pages int) []int {
	if pages <= 0 {
		return nil
	}
	const radius = 2
	start := page - radius
	if start < 1 {
		start = 1
	}
	end := page + radius
	if end > pages {
		end = pages
	}
	out := make([]int, 0, end-start+1)
	for n := start; n <= end; n++ {
		out = append(out, n)
	}
	return out
}

func (m *Module) uiViewImportTeams(w http.ResponseWriter, r *http.Request) {
	m.ws.DoTemplate(w, r, "views/team/form_bulk.p2", nil)
}

func (m *Module) uiViewImportTeamsSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f, _, err := r.FormFile("teams_file")
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}
	defer f.Close()
	rd := csv.NewReader(f)
	teams := []map[string]string{}
	var header []string
	for {
		record, err := rd.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			slog.Error("Error decoding CSV", "error", err)
			continue
		}
		if header == nil {
			header = record
			for col := range header {
				header[col] = strings.ReplaceAll(header[col], "Team Name", "Name")
				header[col] = strings.ReplaceAll(header[col], "Team Number", "Number")
				header[col] = strings.ReplaceAll(header[col], "Hub Name", "Region")
			}
		} else {
			dict := map[string]string{}
			for i := range header {
				dict[header[i]] = record[i]
			}
			if dict["Division"] == "" {
				dict["Division"] = "Open"
			}
			teams = append(teams, dict)
		}
	}

	rows := make([]TeamRow, 0, len(teams))
	for _, team := range teams {
		n, _ := strconv.Atoi(team["Number"])
		rows = append(rows, TeamRow{
			Name:     team["Name"],
			Number:   n,
			Division: team["Division"],
			Region:   team["Region"],
		})
	}

	if _, err := m.upsertTeamRows(r.Context(), rows); err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	http.Redirect(w, r, m.basePath, http.StatusSeeOther)
}

func (m *Module) uiViewAddForm(w http.ResponseWriter, r *http.Request) {
	divisions, err := gorm.G[Division](m.db.DB).Find(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	sort.Slice(divisions, func(i, j int) bool {
		return divisions[i].Name < divisions[j].Name
	})

	regions, err := gorm.G[Region](m.db.DB).Find(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	sort.Slice(regions, func(i, j int) bool {
		return regions[i].Name < regions[j].Name
	})

	ctx := pongo2.Context{
		"divisions": divisions,
		"regions":   regions,
	}

	m.ws.DoTemplate(w, r, "views/team/form_single.p2", ctx)
}

func (m *Module) uiViewEditForm(w http.ResponseWriter, r *http.Request) {
	t, err := gorm.G[Team](m.db.DB).
		Preload("Region", nil).
		Where(&Team{ID: m.ws.StrToUint(chi.URLParam(r, "id"))}).
		First(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	divisions, err := gorm.G[Division](m.db.DB).Find(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	sort.Slice(divisions, func(i, j int) bool {
		return divisions[i].Name < divisions[j].Name
	})

	regions, err := gorm.G[Region](m.db.DB).Find(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	sort.Slice(regions, func(i, j int) bool {
		return regions[i].Name < regions[j].Name
	})

	ctx := pongo2.Context{
		"team":      t,
		"divisions": divisions,
		"regions":   regions,
	}

	m.ws.DoTemplate(w, r, "views/team/form_single.p2", ctx)
}

func (m *Module) uiViewUpsert(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	t := Team{
		ID:         m.ws.StrToUint(chi.URLParam(r, "id")),
		Name:       r.FormValue("team_name"),
		Number:     int(m.ws.StrToUint(r.FormValue("team_number"))),
		DivisionID: m.ws.StrToUint(r.FormValue("team_division")),
		RegionID:   m.ws.StrToUint(r.FormValue("team_region")),
		Disabled:   r.FormValue("team_disabled") != "",
	}

	// The default UpdateAll upsert rewrites every column, so the flag
	// is carried on both the insert (add) and update (edit) paths.
	if err := db.InsertOrUpdate[Team](r.Context(), m.db.DB, &t); err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	http.Redirect(w, r, m.basePath, http.StatusSeeOther)
}

// --- Division CRUD ---

func (m *Module) uiViewListDivisions(w http.ResponseWriter, r *http.Request) {
	divisions, err := gorm.G[Division](m.db.DB).Find(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	sort.Slice(divisions, func(i, j int) bool {
		return divisions[i].Name < divisions[j].Name
	})

	m.ws.DoTemplate(w, r, "views/team/divisions/list.p2", pongo2.Context{"divisions": divisions})
}

func (m *Module) uiViewAddDivision(w http.ResponseWriter, r *http.Request) {
	m.ws.DoTemplate(w, r, "views/team/divisions/form.p2", pongo2.Context{"division": Division{}})
}

func (m *Module) uiViewEditDivision(w http.ResponseWriter, r *http.Request) {
	d, err := gorm.G[Division](m.db.DB).Where(&Division{ID: m.ws.StrToUint(chi.URLParam(r, "division_id"))}).First(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	m.ws.DoTemplate(w, r, "views/team/divisions/form.p2", pongo2.Context{"division": d})
}

func (m *Module) uiViewUpsertDivision(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	d := Division{
		ID:   m.ws.StrToUint(chi.URLParam(r, "division_id")),
		Name: r.FormValue("division_name"),
	}

	if err := db.InsertOrUpdate[Division](r.Context(), m.db.DB, &d); err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	http.Redirect(w, r, path.Join(m.basePath, "/divisions"), http.StatusSeeOther)
}

func (m *Module) uiViewDeleteDivision(w http.ResponseWriter, r *http.Request) {
	id := m.ws.StrToUint(chi.URLParam(r, "division_id"))

	var d Division
	d, err := gorm.G[Division](m.db.DB).Where(&Division{ID: id}).First(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	if d.Static {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": errors.New("cannot delete static divisions")})
		return
	}

	var count int64
	m.db.Model(&Team{}).Where("division_id = ?", id).Count(&count)
	if count > 0 {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": errors.New("cannot delete division with associated teams")})
		return
	}

	if err := m.db.Delete(&Division{ID: id}).Error; err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	http.Redirect(w, r, path.Join(m.basePath, "/divisions"), http.StatusSeeOther)
}

// --- Region CRUD ---

func (m *Module) uiViewListRegions(w http.ResponseWriter, r *http.Request) {
	regions, err := gorm.G[Region](m.db.DB).Find(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	sort.Slice(regions, func(i, j int) bool {
		return regions[i].Name < regions[j].Name
	})

	m.ws.DoTemplate(w, r, "views/team/regions/list.p2", pongo2.Context{"regions": regions})
}

func (m *Module) uiViewAddRegion(w http.ResponseWriter, r *http.Request) {
	m.ws.DoTemplate(w, r, "views/team/regions/form.p2", pongo2.Context{"region": Region{}})
}

func (m *Module) uiViewEditRegion(w http.ResponseWriter, r *http.Request) {
	reg, err := gorm.G[Region](m.db.DB).Where(&Region{ID: m.ws.StrToUint(chi.URLParam(r, "region_id"))}).First(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	m.ws.DoTemplate(w, r, "views/team/regions/form.p2", pongo2.Context{"region": reg})
}

func (m *Module) uiViewUpsertRegion(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	reg := Region{
		ID:   m.ws.StrToUint(chi.URLParam(r, "region_id")),
		Name: r.FormValue("region_name"),
	}

	if err := db.InsertOrUpdate[Region](r.Context(), m.db.DB, &reg); err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	http.Redirect(w, r, path.Join(m.basePath, "/regions"), http.StatusSeeOther)
}

func (m *Module) uiViewDeleteRegion(w http.ResponseWriter, r *http.Request) {
	id := m.ws.StrToUint(chi.URLParam(r, "region_id"))

	_, err := gorm.G[Region](m.db.DB).Where(&Region{ID: id}).First(r.Context())
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	var count int64
	m.db.Model(&Team{}).Where("region_id = ?", id).Count(&count)
	if count > 0 {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": "cannot delete region with associated teams"})
		return
	}

	if err := m.db.Delete(&Region{ID: id}).Error; err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	http.Redirect(w, r, path.Join(m.basePath, "/regions"), http.StatusSeeOther)
}
