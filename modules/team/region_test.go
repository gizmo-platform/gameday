package team

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
	"gorm.io/gorm"
)

func newTestModule(t *testing.T) (*Module, *db.DB) {
	t.Helper()

	t.Setenv("GAMEDAY_DB", filepath.Join(t.TempDir(), "test.db"))

	d, err := db.New()
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}

	ws, err := web.NewServer(web.WithDB(d))
	if err != nil {
		t.Fatalf("web.NewServer: %v", err)
	}

	m := New(d, ws, nil)
	if m == nil {
		t.Fatal("New failed")
	}
	if err := m.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ws.AddTemplateLoader(m.TemplateLoader())
	m.NavList("/ui/mod/team")

	return m, d
}

// adminContext builds a request context carrying a profile with the
// TEAM:ADMIN grant, so hasPermission template checks pass.
func adminContext() context.Context {
	prof := web.Profile{
		Username:    "tester",
		Permissions: []web.Permission{{Module: ModuleName, Grant: PermissionAdmin}},
	}
	return context.WithValue(context.Background(), web.ProfileKey{}, prof)
}

// routerReq runs a request through the module's chi router so URL
// params, guards, and templates behave exactly as they do in
// production.
func routerReq(t *testing.T, m *Module, ctx context.Context, method, target, body string) (int, string) {
	t.Helper()
	// Mount the module router at /ui/mod/team, the same prefix
	// onsite_serve uses, so URL params, guards, and templates behave
	// exactly as they do in production.
	mux := chi.NewRouter()
	mux.Mount("/ui/mod/team", m.Router())

	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func seedRegion(t *testing.T, d *db.DB, name string) Region {
	t.Helper()
	reg := Region{Name: name}
	if err := d.Create(&reg).Error; err != nil {
		t.Fatalf("create region: %v", err)
	}
	return reg
}

func regionExists(t *testing.T, d *db.DB, id uint) bool {
	t.Helper()
	var count int64
	if err := d.Model(&Region{}).Where("id = ?", id).Count(&count).Error; err != nil {
		t.Fatalf("count regions: %v", err)
	}
	return count > 0
}

func TestRegionListRenders(t *testing.T) {
	m, d := newTestModule(t)
	seedRegion(t, d, "Zeta")
	seedRegion(t, d, "Alpha")

	code, body := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/team/regions", "")
	if code != http.StatusOK {
		t.Fatalf("GET /regions = %d, want 200: %s", code, body)
	}
	for _, want := range []string{"Alpha", "Zeta", "/regions/add", "/edit", "/delete"} {
		if !strings.Contains(body, want) {
			t.Errorf("region list missing %q", want)
		}
	}
	if strings.Index(body, "Alpha") > strings.Index(body, "Zeta") {
		t.Error("region list not sorted by name")
	}

	// Without TEAM:ADMIN the add/delete controls are hidden.
	code, body = routerReq(t, m, context.Background(), http.MethodGet, "/ui/mod/team/regions", "")
	if code != http.StatusOK {
		t.Fatalf("GET /regions (no admin) = %d, want 200", code)
	}
	if strings.Contains(body, "Add Region") || strings.Contains(body, "/edit") || strings.Contains(body, "/delete") {
		t.Error("region list shows admin controls without TEAM:ADMIN")
	}
}

func TestRegionCreateAndEdit(t *testing.T) {
	m, d := newTestModule(t)

	// Add form
	code, body := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/team/regions/add", "")
	if code != http.StatusOK {
		t.Fatalf("GET /regions/add = %d, want 200", code)
	}
	if !strings.Contains(body, `name="region_name"`) {
		t.Error("add form missing region_name input")
	}

	// Create
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/team/regions/add", "region_name=Pacific")
	if code != http.StatusSeeOther {
		t.Fatalf("POST /regions/add = %d, want 303: %s", code, body)
	}
	var created Region
	created, err := gorm.G[Region](d.DB).Where(&Region{Name: "Pacific"}).First(context.Background())
	if err != nil {
		t.Fatalf("Pacific not created: %v", err)
	}

	// Edit form shows the current name
	code, body = routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/team/regions/1/edit", "")
	if code != http.StatusOK {
		t.Fatalf("GET /regions/{id}/edit = %d, want 200", code)
	}
	if !strings.Contains(body, `value="Pacific"`) {
		t.Errorf("edit form missing current region name: %s", body)
	}

	// Rename
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/team/regions/1/edit", "region_name=West")
	if code != http.StatusSeeOther {
		t.Fatalf("POST /regions/{id}/edit = %d, want 303: %s", code, body)
	}
	renamed, err := gorm.G[Region](d.DB).Where(&Region{ID: created.ID}).First(context.Background())
	if err != nil {
		t.Fatalf("region missing after rename: %v", err)
	}
	if renamed.Name != "West" {
		t.Errorf("renamed region = %q, want West", renamed.Name)
	}
}

func TestRegionEditMissing(t *testing.T) {
	m, _ := newTestModule(t)

	code, body := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/team/regions/99/edit", "")
	if code == http.StatusOK && strings.Contains(body, `name="region_name"`) && !strings.Contains(body, "record not found") {
		t.Fatal("edit form rendered for a missing region")
	}
}

func TestRegionDelete(t *testing.T) {
	m, d := newTestModule(t)
	reg := seedRegion(t, d, "Pacific")

	// A team referencing the region must block deletion.
	team := Team{Name: "Testers", Number: 1, RegionID: reg.ID}
	if err := d.Create(&team).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}

	code, body := routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/team/regions/1/delete", "")
	if code != http.StatusOK {
		t.Fatalf("delete with teams = %d, want 200 error page", code)
	}
	if !strings.Contains(body, "cannot delete region with associated teams") {
		t.Errorf("delete error page missing message: %s", body)
	}
	if !regionExists(t, d, reg.ID) {
		t.Fatal("region deleted while teams still referenced it")
	}

	// Detach the team; deletion must then succeed.
	if err := d.Model(&Team{}).Where("id = ?", team.ID).Update("region_id", 0).Error; err != nil {
		t.Fatalf("detach team: %v", err)
	}
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/team/regions/1/delete", "")
	if code != http.StatusSeeOther {
		t.Fatalf("delete after detach = %d, want 303: %s", code, body)
	}
	if regionExists(t, d, reg.ID) {
		t.Fatal("region still present after delete")
	}
}

func TestRegionDeleteMissing(t *testing.T) {
	m, _ := newTestModule(t)

	code, _ := routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/team/regions/99/delete", "")
	if code == http.StatusSeeOther {
		t.Fatal("delete of missing region succeeded")
	}
}

func TestTeamUpsertRegion(t *testing.T) {
	m, d := newTestModule(t)
	reg := seedRegion(t, d, "West")

	code, body := routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/team/add",
		"team_name=Testers&team_number=7&team_division=0&team_region="+strconv.FormatUint(uint64(reg.ID), 10))
	if code != http.StatusSeeOther {
		t.Fatalf("POST /add = %d, want 303: %s", code, body)
	}
	var team Team
	team, err := gorm.G[Team](d.DB).Where("name = ?", "Testers").First(context.Background())
	if err != nil {
		t.Fatalf("team not created: %v", err)
	}
	if team.RegionID != reg.ID {
		t.Fatalf("team.RegionID = %d, want %d", team.RegionID, reg.ID)
	}

	// Edit clears the region.
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/team/1/edit",
		"team_name=Testers&team_number=7&team_division=0&team_region=0")
	if code != http.StatusSeeOther {
		t.Fatalf("POST /{id}/edit = %d, want 303: %s", code, body)
	}
	updated, err := gorm.G[Team](d.DB).Where("id = ?", team.ID).First(context.Background())
	if err != nil {
		t.Fatalf("team missing after edit: %v", err)
	}
	if updated.RegionID != 0 {
		t.Fatalf("team.RegionID = %d, want 0", updated.RegionID)
	}
}

func TestListTeamsPreloadsRegion(t *testing.T) {
	m, d := newTestModule(t)
	reg := seedRegion(t, d, "West")
	if err := d.Create(&Team{Name: "Testers", Number: 1, RegionID: reg.ID}).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}

	teams, err := m.ListTeams(context.Background(), Team{})
	if err != nil {
		t.Fatalf("ListTeams: %v", err)
	}
	if len(teams) != 1 {
		t.Fatalf("ListTeams returned %d teams, want 1", len(teams))
	}
	if teams[0].Region.Name != "West" {
		t.Errorf("Region not preloaded: %+v", teams[0].Region)
	}
}

func TestTeamFormsShowRegions(t *testing.T) {
	m, d := newTestModule(t)
	seedRegion(t, d, "Pacific")

	// Add form: None option plus one option per region.
	code, body := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/team/add", "")
	if code != http.StatusOK {
		t.Fatalf("GET /add = %d, want 200", code)
	}
	for _, want := range []string{`name="team_region"`, `<option value="0">None</option>`, `>Pacific</option>`} {
		if !strings.Contains(body, want) {
			t.Errorf("add form missing %q", want)
		}
	}

	// Edit form: the team's region is selected.
	var reg Region
	reg, err := gorm.G[Region](d.DB).Where(&Region{Name: "Pacific"}).First(context.Background())
	if err != nil {
		t.Fatalf("find region: %v", err)
	}
	team := Team{Name: "Testers", Number: 1, RegionID: reg.ID}
	if err := d.Create(&team).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}
	code, body = routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/team/1/edit", "")
	if code != http.StatusOK {
		t.Fatalf("GET /{id}/edit = %d, want 200", code)
	}
	if !strings.Contains(body, "selected>Pacific</option>") {
		t.Errorf("edit form does not select the team's region: %s", body)
	}
}
