package contest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"strconv"
	"testing"

	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/go-chi/chi/v5"
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

	m := New("cloud", d, ws, nil)
	if m == nil {
		t.Fatal("New failed")
	}
	if err := m.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ws.AddTemplateLoader(m.TemplateLoader())
	m.NavList("/ui/mod/contest")

	return m, d
}

func adminContext() context.Context {
	prof := web.Profile{
		Username:    "tester",
		Permissions: []web.Permission{{Module: ModuleName, Grant: PermissionAdmin}},
	}
	return context.WithValue(context.Background(), web.ProfileKey{}, prof)
}

func routerReq(t *testing.T, m *Module, ctx context.Context, method, target string, form url.Values) (int, string) {
	t.Helper()

	mux := chi.NewRouter()
	mux.Mount("/ui/mod/contest", m.Router())

	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func contestCount(t *testing.T, d *db.DB) int64 {
	t.Helper()
	var count int64
	if err := d.Model(&Contest{}).Count(&count).Error; err != nil {
		t.Fatalf("count contests: %v", err)
	}
	return count
}

func fetchContest(t *testing.T, d *db.DB, id uint) Contest {
	t.Helper()
	c, err := gorm.G[Contest](d.DB).Where(&Contest{ID: id}).Preload("Region", nil).First(context.Background())
	if err != nil {
		t.Fatalf("fetch contest %d: %v", id, err)
	}
	return c
}

func seedRegion(t *testing.T, d *db.DB, name string) team.Region {
	t.Helper()
	reg := team.Region{Name: name}
	if err := d.Create(&reg).Error; err != nil {
		t.Fatalf("create region: %v", err)
	}
	return reg
}

func seedTeam(t *testing.T, d *db.DB, name string, number int, regionID uint, disabled bool) team.Team {
	t.Helper()
	tm := team.Team{Name: name, Number: number, RegionID: regionID, Disabled: disabled}
	if err := d.Create(&tm).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}
	return tm
}

func createContest(t *testing.T, d *db.DB, name string, regionID uint) Contest {
	t.Helper()
	c := Contest{Name: name, RegionID: regionID}
	if err := d.Create(&c).Error; err != nil {
		t.Fatalf("create contest: %v", err)
	}
	return c
}

func rosterCount(t *testing.T, d *db.DB, contestID uint) int64 {
	t.Helper()
	var count int64
	if err := d.Model(&ContestRoster{}).Where("contest_id = ?", contestID).Count(&count).Error; err != nil {
		t.Fatalf("count roster: %v", err)
	}
	return count
}

func rosterHasTeam(t *testing.T, d *db.DB, contestID, teamID uint) bool {
	t.Helper()
	var count int64
	if err := d.Model(&ContestRoster{}).
		Where("contest_id = ? AND team_id = ?", contestID, teamID).
		Count(&count).Error; err != nil {
		t.Fatalf("count roster row: %v", err)
	}
	return count > 0
}

func TestContestRosterManagement(t *testing.T) {
	m, d := newTestModule(t)

	north := seedRegion(t, d, "North")
	south := seedRegion(t, d, "South")

	alpha := seedTeam(t, d, "Alpha", 1, north.ID, false)
	beta := seedTeam(t, d, "Beta", 2, north.ID, false)
	disabled := seedTeam(t, d, "Gamma", 3, north.ID, true)
	outside := seedTeam(t, d, "Delta", 4, south.ID, false)

	c := createContest(t, d, "Winter FFA", north.ID)

	// Empty roster page renders.
	code, body := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/contest/1/roster", nil)
	if code != http.StatusOK {
		t.Fatalf("roster page = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "Winter FFA") {
		t.Errorf("roster page missing contest name: %s", body)
	}
	if count := rosterCount(t, d, c.ID); count != 0 {
		t.Fatalf("initial roster = %d rows, want 0", count)
	}

	// Start from region: seeds enabled region teams, skips disabled.
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/contest/1/roster/init", nil)
	if code != http.StatusSeeOther {
		t.Fatalf("roster init = %d, want 303: %s", code, body)
	}
	if count := rosterCount(t, d, c.ID); count != 2 {
		t.Fatalf("after init = %d rows, want 2", count)
	}
	if !rosterHasTeam(t, d, c.ID, alpha.ID) || !rosterHasTeam(t, d, c.ID, beta.ID) {
		t.Errorf("init did not seed region teams: has(alpha)=%v has(beta)=%v", rosterHasTeam(t, d, c.ID, alpha.ID), rosterHasTeam(t, d, c.ID, beta.ID))
	}
	if rosterHasTeam(t, d, c.ID, disabled.ID) {
		t.Errorf("init seeded a disabled team")
	}

	// Roster page now lists the seeded teams.
	code, body = routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/contest/1/roster", nil)
	if code != http.StatusOK {
		t.Fatalf("roster page = %d, want 200: %s", code, body)
	}
	for _, name := range []string{"Alpha", "Beta"} {
		if !strings.Contains(body, name) {
			t.Errorf("roster page missing team %s: %s", name, body)
		}
	}

	// Re-init is idempotent: no duplicates.
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/contest/1/roster/init", nil)
	if code != http.StatusSeeOther {
		t.Fatalf("re-init = %d, want 303: %s", code, body)
	}
	if count := rosterCount(t, d, c.ID); count != 2 {
		t.Fatalf("after re-init = %d rows, want 2", count)
	}

	// Add form offers teams not already on the roster.
	code, body = routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/contest/1/roster/add", nil)
	if code != http.StatusOK {
		t.Fatalf("roster add form = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "Delta") {
		t.Errorf("add form missing outside-region team: %s", body)
	}
	if strings.Contains(body, "Alpha") {
		t.Errorf("add form lists team already on roster: %s", body)
	}

	// Add a team from outside the region: the roster is independent.
	form := url.Values{}
	form.Set("roster_team", strconv.FormatUint(uint64(outside.ID), 10))
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/contest/1/roster/add", form)
	if code != http.StatusSeeOther {
		t.Fatalf("roster add = %d, want 303: %s", code, body)
	}
	if count := rosterCount(t, d, c.ID); count != 3 {
		t.Fatalf("after add = %d rows, want 3", count)
	}

	// Adding the same team again is rejected.
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/contest/1/roster/add", form)
	if code == http.StatusSeeOther {
		t.Errorf("duplicate add = 303, want error: %s", body)
	}
	if count := rosterCount(t, d, c.ID); count != 3 {
		t.Fatalf("after duplicate add = %d rows, want 3", count)
	}

	// Remove the outside team; the region teams survive.
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/contest/1/roster/"+strconv.FormatUint(uint64(outside.ID), 10)+"/delete", nil)
	if code != http.StatusSeeOther {
		t.Fatalf("roster remove = %d, want 303: %s", code, body)
	}
	if count := rosterCount(t, d, c.ID); count != 2 {
		t.Fatalf("after remove = %d rows, want 2", count)
	}
	if !rosterHasTeam(t, d, c.ID, alpha.ID) || !rosterHasTeam(t, d, c.ID, beta.ID) {
		t.Errorf("region teams missing after remove")
	}

	// Removing a team not on the roster is a no-op (still a 303).
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/contest/1/roster/"+strconv.FormatUint(uint64(outside.ID), 10)+"/delete", nil)
	if code != http.StatusSeeOther {
		t.Fatalf("no-op remove = %d, want 303: %s", code, body)
	}
	if count := rosterCount(t, d, c.ID); count != 2 {
		t.Fatalf("after no-op remove = %d rows, want 2", count)
	}

	// Deleting the contest cascades to its roster rows.
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/contest/1/delete", nil)
	if code != http.StatusSeeOther {
		t.Fatalf("contest delete = %d, want 303: %s", code, body)
	}
	if count := rosterCount(t, d, c.ID); count != 0 {
		t.Fatalf("roster rows after contest delete = %d, want 0", count)
	}
}

func TestContestRosterRequiresPermission(t *testing.T) {
	m, d := newTestModule(t)
	north := seedRegion(t, d, "North")
	c := createContest(t, d, "Winter FFA", north.ID)

	noPerm := context.Background()

	// The roster page itself is readable without the permission.
	code, body := routerReq(t, m, noPerm, http.MethodGet, "/ui/mod/contest/1/roster", nil)
	if code != http.StatusOK {
		t.Fatalf("roster page without permission = %d, want 200: %s", code, body)
	}

	// Mutations are guarded: the unauthorized template is rendered
	// with 401 (no authenticated user) and nothing is written.
	for _, target := range []string{
		"/ui/mod/contest/1/roster/init",
		"/ui/mod/contest/1/roster/add",
	} {
		code, body = routerReq(t, m, noPerm, http.MethodPost, target, nil)
		if code != http.StatusUnauthorized {
			t.Fatalf("%s without permission = %d, want 401: %s", target, code, body)
		}
		if !strings.Contains(body, "Unauthorized") {
			t.Errorf("%s without permission did not render unauthorized: %s", target, body)
		}
	}
	if count := rosterCount(t, d, c.ID); count != 0 {
		t.Fatalf("blocked mutations wrote %d rows, want 0", count)
	}
}

func TestContestCRUDRoundTrip(t *testing.T) {
	m, d := newTestModule(t)
	reg := seedRegion(t, d, "North")

	// List renders with the base template layout.
	code, body := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/contest/", nil)
	if code != http.StatusOK {
		t.Fatalf("list = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "Contests") {
		t.Errorf("list page missing title: %s", body)
	}

	// Add form renders the region select.
	code, body = routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/contest/add", nil)
	if code != http.StatusOK {
		t.Fatalf("add form = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "North") {
		t.Errorf("add form missing region: %s", body)
	}

	// Create.
	form := url.Values{}
	form.Set("contest_name", "Winter FFA")
	form.Set("contest_region", strconv.FormatUint(uint64(reg.ID), 10))
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/contest/add", form)
	if code != http.StatusSeeOther {
		t.Fatalf("create = %d, want 303: %s", code, body)
	}
	if count := contestCount(t, d); count != 1 {
		t.Fatalf("after create = %d contests, want 1", count)
	}
	if got := fetchContest(t, d, 1); got.Name != "Winter FFA" || got.RegionID != reg.ID {
		t.Errorf("created contest = %+v, want name Winter FFA region %d", got, reg.ID)
	}

	// Edit form renders with current values.
	code, body = routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/contest/1/edit", nil)
	if code != http.StatusOK {
		t.Fatalf("edit form = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "Winter FFA") {
		t.Errorf("edit form missing name: %s", body)
	}

	// Update in place.
	form = url.Values{}
	form.Set("contest_name", "Spring FFA")
	form.Set("contest_region", strconv.FormatUint(uint64(reg.ID), 10))
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/contest/1/edit", form)
	if code != http.StatusSeeOther {
		t.Fatalf("update = %d, want 303: %s", code, body)
	}
	if count := contestCount(t, d); count != 1 {
		t.Fatalf("after update = %d contests, want 1", count)
	}
	if got := fetchContest(t, d, 1); got.Name != "Spring FFA" {
		t.Errorf("updated name = %q, want Spring FFA", got.Name)
	}

	// Delete.
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/contest/1/delete", nil)
	if code != http.StatusSeeOther {
		t.Fatalf("delete = %d, want 303: %s", code, body)
	}
	if count := contestCount(t, d); count != 0 {
		t.Fatalf("after delete = %d contests, want 0", count)
	}
}
