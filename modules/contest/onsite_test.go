package contest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/game"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/cloud"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

// fakeGameYAML is the minimal game setup file the fake cloud serves:
// one position and one phase, enough for applyGameConfig to create a
// GamePhase row.
const fakeGameYAML = `
Field:
  Positions:
    - ID: 1
      Name: "Offense"
      Color1: "#ffffff"
      Color2: "#cccccc"
Game:
  Phases:
    - ID: 1
      Name: "Round 1"
`

// fakeCloudServer serves the cloud endpoints the onsite contest module
// pulls: the contest list, a per-contest roster, and the game setup
// file. Contests with no entry in rosters are served with an empty
// roster, so onsite tests never touch the network.
func fakeCloudServer(t *testing.T, contests []cloud.ContestInfo, rosters map[uint][]cloud.TeamInfo) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/ui/mod/contest/cloud/contests":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(contests)
		case strings.HasPrefix(r.URL.Path, "/ui/mod/contest/cloud/contests/") && strings.HasSuffix(r.URL.Path, "/roster"):
			idStr := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/ui/mod/contest/cloud/contests/"), "/roster")
			id, err := strconv.ParseUint(idStr, 10, 64)
			if err != nil {
				t.Errorf("bad roster path %q", r.URL.Path)
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rosters[uint(id)])
		case r.URL.Path == "/ui/mod/game/cloud/setup":
			w.Header().Set("Content-Type", "text/yaml")
			_, _ = w.Write([]byte(fakeGameYAML))
		default:
			t.Errorf("unexpected cloud path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

// newOnsiteTestModule builds an OnsiteModule against a fresh temp-dir
// database, wired to real team and game modules so the selection-time
// imports run for real. Callers must set GAMEDAY_CLOUD before calling
// this, since the cloud clients read it at construction time.
func newOnsiteTestModule(t *testing.T) (*OnsiteModule, *db.DB, *team.Module, *game.Module) {
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

	tm := team.New("onsite", d, ws, nil)
	if tm == nil {
		t.Fatal("team.New failed")
	}
	if err := tm.Migrate(); err != nil {
		t.Fatalf("team.Migrate: %v", err)
	}

	gm := game.New("onsite", d, ws, modules.ModuleDeps{"team": tm})
	if gm == nil {
		t.Fatal("game.New failed")
	}
	if err := gm.Migrate(); err != nil {
		t.Fatalf("game.Migrate: %v", err)
	}

	m := NewOnsite("onsite", d, ws, modules.ModuleDeps{"team": tm, "game": gm})
	if m == nil {
		t.Fatal("NewOnsite failed")
	}
	if err := m.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ws.AddTemplateLoader(m.TemplateLoader())
	m.NavList("/ui/mod/contest")

	return m, d, tm, gm
}

func onsiteRouterReq(t *testing.T, m *OnsiteModule, ctx context.Context, method, target string, form url.Values) (int, string) {
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

func onsiteContestCount(t *testing.T, d *db.DB) int64 {
	t.Helper()
	var count int64
	if err := d.Model(&OnsiteContest{}).Count(&count).Error; err != nil {
		t.Fatalf("count onsite contests: %v", err)
	}
	return count
}

func fetchOnsiteContest(t *testing.T, d *db.DB) OnsiteContest {
	t.Helper()
	c, err := gorm.G[OnsiteContest](d.DB).First(context.Background())
	if err != nil {
		t.Fatalf("fetch onsite contest: %v", err)
	}
	return c
}

func TestMyContestSelectsCloudContest(t *testing.T) {
	srv := fakeCloudServer(t, []cloud.ContestInfo{
		{ID: 7, Name: "Winter FFA", Region: "North"},
		{ID: 9, Name: "Spring FFA", Region: "South"},
	}, nil)
	defer srv.Close()
	t.Setenv("GAMEDAY_CLOUD", srv.URL)

	m, d, _, _ := newOnsiteTestModule(t)

	// The page lists the cloud contests and the cloud base URL.
	code, body := onsiteRouterReq(t, m, adminContext(), http.MethodGet, "/ui/mod/contest/", nil)
	if code != http.StatusOK {
		t.Fatalf("my contest page = %d, want 200: %s", code, body)
	}
	for _, want := range []string{"Winter FFA", "Spring FFA", srv.URL} {
		if !strings.Contains(body, want) {
			t.Errorf("my contest page missing %q: %s", want, body)
		}
	}
	if count := onsiteContestCount(t, d); count != 0 {
		t.Fatalf("initial onsite contest rows = %d, want 0", count)
	}

	// Selecting a contest stores it under the cloud contest ID.
	form := url.Values{}
	form.Set("contest_id", "7")
	code, body = onsiteRouterReq(t, m, adminContext(), http.MethodPost, "/ui/mod/contest/", form)
	if code != http.StatusSeeOther {
		t.Fatalf("select = %d, want 303: %s", code, body)
	}
	if count := onsiteContestCount(t, d); count != 1 {
		t.Fatalf("after select = %d rows, want 1", count)
	}
	got := fetchOnsiteContest(t, d)
	if got.ID != 7 || got.Name != "Winter FFA" || got.Region != "North" {
		t.Errorf("selected contest = %+v, want ID 7 Winter FFA North", got)
	}

	// The page now shows the current selection.
	code, body = onsiteRouterReq(t, m, adminContext(), http.MethodGet, "/ui/mod/contest/", nil)
	if code != http.StatusOK {
		t.Fatalf("my contest page after select = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "Currently handling contest") {
		t.Errorf("my contest page missing current selection: %s", body)
	}

	// Re-selecting replaces the previous row: exactly one row, new ID.
	form = url.Values{}
	form.Set("contest_id", "9")
	code, body = onsiteRouterReq(t, m, adminContext(), http.MethodPost, "/ui/mod/contest/", form)
	if code != http.StatusSeeOther {
		t.Fatalf("re-select = %d, want 303: %s", code, body)
	}
	if count := onsiteContestCount(t, d); count != 1 {
		t.Fatalf("after re-select = %d rows, want 1", count)
	}
	got = fetchOnsiteContest(t, d)
	if got.ID != 9 || got.Name != "Spring FFA" || got.Region != "South" {
		t.Errorf("re-selected contest = %+v, want ID 9 Spring FFA South", got)
	}
}

func TestMyContestRequiresPermission(t *testing.T) {
	srv := fakeCloudServer(t, []cloud.ContestInfo{
		{ID: 7, Name: "Winter FFA", Region: "North"},
	}, nil)
	defer srv.Close()
	t.Setenv("GAMEDAY_CLOUD", srv.URL)

	m, d, _, _ := newOnsiteTestModule(t)

	// The page is admin-only: the unauthorized template is rendered
	// with 401 (no authenticated user).
	code, body := onsiteRouterReq(t, m, context.Background(), http.MethodGet, "/ui/mod/contest/", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("my contest page without permission = %d, want 401: %s", code, body)
	}
	if !strings.Contains(body, "Unauthorized") {
		t.Errorf("my contest page without permission did not render unauthorized: %s", body)
	}

	// Selection is guarded: the unauthorized template is rendered
	// with 401 and nothing is written.
	form := url.Values{}
	form.Set("contest_id", "7")
	code, body = onsiteRouterReq(t, m, context.Background(), http.MethodPost, "/ui/mod/contest/", form)
	if code != http.StatusUnauthorized {
		t.Fatalf("select without permission = %d, want 401: %s", code, body)
	}
	if !strings.Contains(body, "Unauthorized") {
		t.Errorf("select without permission did not render unauthorized: %s", body)
	}
	if count := onsiteContestCount(t, d); count != 0 {
		t.Fatalf("blocked select wrote %d rows, want 0", count)
	}
}

func TestCloudContestsEndpoint(t *testing.T) {
	m, d := newTestModule(t)

	north := seedRegion(t, d, "North")
	south := seedRegion(t, d, "South")
	winter := createContest(t, d, "Winter FFA", north.ID)
	spring := createContest(t, d, "Spring FFA", south.ID)

	code, body := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/contest/cloud/contests", nil)
	if code != http.StatusOK {
		t.Fatalf("cloud contests = %d, want 200: %s", code, body)
	}

	var got []cloud.ContestInfo
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal cloud contests: %v: %s", err, body)
	}
	if len(got) != 2 {
		t.Fatalf("len(contests) = %d, want 2: %s", len(got), body)
	}
	// Ordered by name: Spring FFA before Winter FFA.
	if got[0].ID != spring.ID || got[0].Name != "Spring FFA" || got[0].Region != "South" {
		t.Errorf("contests[0] = %+v, want Spring FFA South", got[0])
	}
	if got[1].ID != winter.ID || got[1].Name != "Winter FFA" || got[1].Region != "North" {
		t.Errorf("contests[1] = %+v, want Winter FFA North", got[1])
	}
}

func TestSelectContestImportsRoster(t *testing.T) {
	srv := fakeCloudServer(t, []cloud.ContestInfo{
		{ID: 7, Name: "Winter FFA", Region: "North"},
	}, map[uint][]cloud.TeamInfo{
		7: {
			{Number: 1, Name: "Alpha", Division: "Open", Region: "North"},
			{Number: 2, Name: "Beta", Division: "Open", Region: "North"},
		},
	})
	defer srv.Close()
	t.Setenv("GAMEDAY_CLOUD", srv.URL)

	m, d, _, _ := newOnsiteTestModule(t)

	form := url.Values{}
	form.Set("contest_id", "7")
	code, body := onsiteRouterReq(t, m, adminContext(), http.MethodPost, "/ui/mod/contest/", form)
	if code != http.StatusSeeOther {
		t.Fatalf("select = %d, want 303: %s", code, body)
	}

	var count int64
	if err := d.Model(&team.Team{}).Count(&count).Error; err != nil {
		t.Fatalf("count teams: %v", err)
	}
	if count != 2 {
		t.Fatalf("team count after select = %d, want 2", count)
	}

	got, err := gorm.G[team.Team](d.DB).Where(&team.Team{Number: 1}).Preload("Division", nil).Preload("Region", nil).First(context.Background())
	if err != nil {
		t.Fatalf("fetch team 1: %v", err)
	}
	if got.Name != "Alpha" || got.Division.Name != "Open" || got.Region.Name != "North" {
		t.Errorf("team 1 = %+v, want Alpha Open North", got)
	}
}

func TestSelectContestSkipsRosterWhenTeamsExist(t *testing.T) {
	srv := fakeCloudServer(t, []cloud.ContestInfo{
		{ID: 7, Name: "Winter FFA", Region: "North"},
	}, map[uint][]cloud.TeamInfo{
		7: {
			{Number: 1, Name: "Alpha", Division: "Open", Region: "North"},
			{Number: 2, Name: "Beta", Division: "Open", Region: "North"},
		},
	})
	defer srv.Close()
	t.Setenv("GAMEDAY_CLOUD", srv.URL)

	m, d, tm, _ := newOnsiteTestModule(t)

	if _, err := tm.ImportTeams(context.Background(), []team.TeamRow{{Name: "Existing", Number: 50, Division: "Open", Region: "North"}}); err != nil {
		t.Fatalf("seed team: %v", err)
	}

	form := url.Values{}
	form.Set("contest_id", "7")
	code, body := onsiteRouterReq(t, m, adminContext(), http.MethodPost, "/ui/mod/contest/", form)
	if code != http.StatusSeeOther {
		t.Fatalf("select = %d, want 303: %s", code, body)
	}

	var count int64
	if err := d.Model(&team.Team{}).Count(&count).Error; err != nil {
		t.Fatalf("count teams: %v", err)
	}
	if count != 1 {
		t.Fatalf("team count after select = %d, want 1 (roster import should be skipped)", count)
	}
}

func TestSelectContestImportsGame(t *testing.T) {
	srv := fakeCloudServer(t, []cloud.ContestInfo{
		{ID: 7, Name: "Winter FFA", Region: "North"},
	}, nil)
	defer srv.Close()
	t.Setenv("GAMEDAY_CLOUD", srv.URL)

	m, d, _, _ := newOnsiteTestModule(t)

	var count int64
	if err := d.Model(&game.GamePhase{}).Count(&count).Error; err != nil {
		t.Fatalf("count phases: %v", err)
	}
	if count != 0 {
		t.Fatalf("initial phase count = %d, want 0", count)
	}

	form := url.Values{}
	form.Set("contest_id", "7")
	code, body := onsiteRouterReq(t, m, adminContext(), http.MethodPost, "/ui/mod/contest/", form)
	if code != http.StatusSeeOther {
		t.Fatalf("select = %d, want 303: %s", code, body)
	}

	if err := d.Model(&game.GamePhase{}).Count(&count).Error; err != nil {
		t.Fatalf("count phases: %v", err)
	}
	if count != 1 {
		t.Fatalf("phase count after select = %d, want 1", count)
	}
}

func TestSelectContestSkipsGameWhenLoaded(t *testing.T) {
	srv := fakeCloudServer(t, []cloud.ContestInfo{
		{ID: 7, Name: "Winter FFA", Region: "North"},
	}, nil)
	defer srv.Close()
	t.Setenv("GAMEDAY_CLOUD", srv.URL)

	m, d, _, _ := newOnsiteTestModule(t)

	if err := d.Create(&game.GamePhase{ID: 99, Name: "Existing Phase"}).Error; err != nil {
		t.Fatalf("seed phase: %v", err)
	}

	form := url.Values{}
	form.Set("contest_id", "7")
	code, body := onsiteRouterReq(t, m, adminContext(), http.MethodPost, "/ui/mod/contest/", form)
	if code != http.StatusSeeOther {
		t.Fatalf("select = %d, want 303: %s", code, body)
	}

	var count int64
	if err := d.Model(&game.GamePhase{}).Count(&count).Error; err != nil {
		t.Fatalf("count phases: %v", err)
	}
	if count != 1 {
		t.Fatalf("phase count after select = %d, want 1 (game import should be skipped)", count)
	}
	got, err := gorm.G[game.GamePhase](d.DB).First(context.Background())
	if err != nil {
		t.Fatalf("fetch phase: %v", err)
	}
	if got.Name != "Existing Phase" {
		t.Errorf("phase name = %q, want Existing Phase", got.Name)
	}
}
