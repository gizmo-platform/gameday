package game

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/goccy/go-yaml"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

// adminContext builds a request context carrying a profile with the
// GAME:ADMIN grant, so hasPermission template checks pass.
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
func routerReq(t *testing.T, m *Module, ctx context.Context, method, target, body string) (int, string, http.Header) {
	t.Helper()
	mux := chi.NewRouter()
	mux.Mount("/ui/mod/game", m.Router())

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
	return rec.Code, rec.Body.String(), rec.Header()
}

// fakeCloudGameServer serves the game setup file from a canned YAML
// body, so import tests never touch the network.
func fakeCloudGameServer(t *testing.T, yamlBody []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ui/mod/game/cloud/setup":
			w.Header().Set("Content-Type", "text/yaml")
			_, _ = w.Write(yamlBody)
		default:
			t.Errorf("unexpected cloud path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

// readGameFile loads the shared game setup fixture used by the
// clock and lint tests.
func readGameFile(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "test_data", "2p_with_objective.yml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

// importCloudGame POSTs the import form and returns the response
// code and Location header.
func importCloudGame(t *testing.T, m *Module) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/ui/mod/game/import-cloud", nil)
	req = req.WithContext(adminContext())

	mux := chi.NewRouter()
	mux.Mount("/ui/mod/game", m.Router())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Header().Get("Location")
}

func countGameRows(t *testing.T, d *db.DB, model any) int64 {
	t.Helper()
	var count int64
	if err := d.Model(model).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	return count
}

func TestImportCloudEndToEnd(t *testing.T) {
	srv := fakeCloudGameServer(t, readGameFile(t))
	defer srv.Close()
	t.Setenv("GAMEDAY_CLOUD", srv.URL)

	m, d := newTestModule(t)

	// The page shows the cloud base and the import button.
	code, body, _ := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/game/import-cloud", "")
	if code != http.StatusOK {
		t.Fatalf("import page = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, srv.URL) || !strings.Contains(body, "Import Game Setup") {
		t.Errorf("import page missing cloud base or import button:\n%s", body)
	}

	code, loc := importCloudGame(t, m)
	if code != http.StatusSeeOther {
		t.Fatalf("import POST = %d, want 303, location %q", code, loc)
	}
	if loc != "/ui/mod/game/import-cloud?imported=1" {
		t.Fatalf("Location = %q, want /ui/mod/game/import-cloud?imported=1", loc)
	}

	if got := countGameRows(t, d, &FieldPosition{}); got != 2 {
		t.Errorf("positions = %d, want 2", got)
	}
	if got := countGameRows(t, d, &GamePhase{}); got != 2 {
		t.Errorf("phases = %d, want 2", got)
	}
	if got := countGameRows(t, d, &GameElement{}); got != 1 {
		t.Errorf("elements = %d, want 1", got)
	}
	if got := countGameRows(t, d, &ScorecardElement{}); got != 2 {
		t.Errorf("scorecard elements = %d, want 2", got)
	}

	// The advancement filter was applied to the phase it belongs to.
	var filter GamePhaseAdvancementFilter
	if err := d.Model(&GamePhaseAdvancementFilter{}).Where("game_phase_id = ?", 2).First(&filter).Error; err != nil {
		t.Fatalf("phase 2 advancement filter missing: %v", err)
	}
	if filter.Rule != "PickTop4" || filter.Filter != "ScoreboardRanking" || filter.SelectFrom != 1 {
		t.Errorf("filter = %+v, want PickTop4/ScoreboardRanking/SelectFrom 1", filter)
	}

	// Scorecard expressions were regenerated from element and state.
	var sce ScorecardElement
	if err := d.Model(&ScorecardElement{}).Where("element = ?", "widgets_fabricated").First(&sce).Error; err != nil {
		t.Fatalf("widgets_fabricated scorecard element missing: %v", err)
	}
	if sce.Expr != "int(widgets_fabricated * 10)" {
		t.Errorf("widgets_fabricated expr = %q, want int(widgets_fabricated * 10)", sce.Expr)
	}

	// The clock section was applied.
	cs, err := m.clockSettings(context.Background())
	if err != nil {
		t.Fatalf("clock settings missing after import: %v", err)
	}
	if cs.Duration != 150*time.Second || cs.Hurry != 30*time.Second || !cs.Transitions {
		t.Errorf("clock = duration %v hurry %v transitions %v, want 2m30s/30s/true", cs.Duration, cs.Hurry, cs.Transitions)
	}
}

func TestImportCloudIdempotent(t *testing.T) {
	srv := fakeCloudGameServer(t, readGameFile(t))
	defer srv.Close()
	t.Setenv("GAMEDAY_CLOUD", srv.URL)

	m, d := newTestModule(t)

	for i := 0; i < 2; i++ {
		if code, loc := importCloudGame(t, m); code != http.StatusSeeOther {
			t.Fatalf("import %d = %d, want 303, location %q", i+1, code, loc)
		}
	}

	if got := countGameRows(t, d, &FieldPosition{}); got != 2 {
		t.Errorf("positions after re-import = %d, want 2 (no duplicates)", got)
	}
	if got := countGameRows(t, d, &GamePhase{}); got != 2 {
		t.Errorf("phases after re-import = %d, want 2 (no duplicates)", got)
	}
	if got := countGameRows(t, d, &GameElement{}); got != 1 {
		t.Errorf("elements after re-import = %d, want 1 (no duplicates)", got)
	}
	if got := countGameRows(t, d, &ScorecardElement{}); got != 2 {
		t.Errorf("scorecard elements after re-import = %d, want 2 (no duplicates)", got)
	}
}

func TestImportCloudFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "cloud is on fire", http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("GAMEDAY_CLOUD", srv.URL)

	m, d := newTestModule(t)

	// GET surfaces the error on the page without failing hard.
	code, body, _ := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/game/import-cloud", "")
	if code != http.StatusOK {
		t.Fatalf("import page = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "notification is-danger") {
		t.Errorf("import page missing error alert:\n%s", body)
	}

	// POST against a failing cloud writes nothing and reports the error.
	if code, _ := importCloudGame(t, m); code != http.StatusOK {
		t.Fatalf("failing import POST = %d, want 200 (error page)", code)
	}
	if got := countGameRows(t, d, &FieldPosition{}); got != 0 {
		t.Errorf("positions after failed import = %d, want 0", got)
	}
}

// uploadSetupFile POSTs a game setup YAML file to the setup form as a
// multipart upload, the way the browser form does.
func uploadSetupFile(t *testing.T, m *Module, y []byte) (int, string) {
	t.Helper()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("game_config", "game.yml")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(y); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/ui/mod/game/setup", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req = req.WithContext(adminContext())

	mux := chi.NewRouter()
	mux.Mount("/ui/mod/game", m.Router())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Header().Get("Location")
}

// newCloudTestModule builds a game module in the cloud scope on top of
// the shared test database, the way `gameday cloud serve` wires it up.
func newCloudTestModule(t *testing.T, d *db.DB, ws *web.Server, deps modules.ModuleDeps) *Module {
	t.Helper()

	cm := New("cloud", d, ws, deps)
	if cm == nil {
		t.Fatal("cloud game.New failed")
	}
	if err := cm.Migrate(); err != nil {
		t.Fatalf("cloud game Migrate: %v", err)
	}
	cm.basePath = "/ui/mod/game"
	return cm
}

// TestCloudSetupScreens verifies the cloud scope serves the
// configuration and setup screens and applies an uploaded game setup
// file, while leaving the onsite-only routes unregistered.
func TestCloudSetupScreens(t *testing.T) {
	srv := fakeCloudGameServer(t, readGameFile(t))
	defer srv.Close()
	t.Setenv("GAMEDAY_CLOUD", srv.URL)

	m, d := newTestModule(t)

	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	// The configuration screen renders the empty state.
	code, body, _ := routerReq(t, cm, adminContext(), http.MethodGet, "/ui/mod/game/", "")
	if code != http.StatusOK {
		t.Fatalf("config = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "No configuration has been found") {
		t.Errorf("config page missing empty state:\n%s", body)
	}

	// The setup form renders with its upload control.
	code, body, _ = routerReq(t, cm, adminContext(), http.MethodGet, "/ui/mod/game/setup", "")
	if code != http.StatusOK {
		t.Fatalf("setup form = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "enctype=\"multipart/form-data\"") {
		t.Errorf("setup form missing upload control:\n%s", body)
	}

	// Uploading a setup file through the cloud form applies it and
	// redirects back to the configuration screen.
	code, loc := uploadSetupFile(t, cm, readGameFile(t))
	if code != http.StatusSeeOther {
		t.Fatalf("setup POST = %d, want 303, location %q", code, loc)
	}
	if loc != "/ui/mod/game" {
		t.Fatalf("Location = %q, want /ui/mod/game", loc)
	}

	if got := countGameRows(t, d, &FieldPosition{}); got != 2 {
		t.Errorf("positions = %d, want 2", got)
	}
	if got := countGameRows(t, d, &GamePhase{}); got != 2 {
		t.Errorf("phases = %d, want 2", got)
	}
	if got := countGameRows(t, d, &GameElement{}); got != 1 {
		t.Errorf("elements = %d, want 1", got)
	}

	// The configuration screen now reflects the applied setup.
	code, body, _ = routerReq(t, cm, adminContext(), http.MethodGet, "/ui/mod/game/", "")
	if code != http.StatusOK {
		t.Fatalf("config after upload = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "Gameday has been configured with 2 field positions") {
		t.Errorf("config page missing applied positions:\n%s", body)
	}

	// Onsite-only routes stay unregistered on the cloud scope.
	if code, _, _ := routerReq(t, cm, adminContext(), http.MethodGet, "/ui/mod/game/import-cloud", ""); code != http.StatusNotFound {
		t.Errorf("import-cloud on cloud = %d, want 404", code)
	}
	if code, _, _ := routerReq(t, cm, adminContext(), http.MethodGet, "/ui/mod/game/schedule", ""); code != http.StatusNotFound {
		t.Errorf("schedule on cloud = %d, want 404", code)
	}
}

func TestCloudSetupDataEndpoint(t *testing.T) {
	srv := fakeCloudGameServer(t, readGameFile(t))
	defer srv.Close()
	t.Setenv("GAMEDAY_CLOUD", srv.URL)

	m, d := newTestModule(t)

	if code, loc := importCloudGame(t, m); code != http.StatusSeeOther {
		t.Fatalf("import = %d, want 303, location %q", code, loc)
	}

	// Mark one phase active and frozen so the test can assert the
	// flags are stripped from the export.
	if err := d.Model(&GamePhase{}).Where("id = ?", 1).Updates(map[string]any{"active": true, "frozen": true}).Error; err != nil {
		t.Fatalf("set phase active: %v", err)
	}

	// The public endpoint lives on the cloud-scoped router, so route
	// it through a cloud instance that shares the same database.  The
	// public endpoint needs no profile in context.
	cm := New("cloud", d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})
	if cm == nil {
		t.Fatal("cloud game.New failed")
	}
	code, body, hdr := routerReq(t, cm, nil, http.MethodGet, "/ui/mod/game/cloud/setup", "")
	if code != http.StatusOK {
		t.Fatalf("cloud setup = %d, want 200: %s", code, body)
	}
	if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "text/yaml") {
		t.Errorf("Content-Type = %q, want text/yaml", ct)
	}

	c := Config{}
	if err := yaml.Unmarshal([]byte(body), &c); err != nil {
		t.Fatalf("exported document does not parse: %v", err)
	}
	if len(c.Field.Positions) != 2 || len(c.Game.Phases) != 2 || len(c.Game.Elements) != 1 {
		t.Errorf("export = %d positions, %d phases, %d elements, want 2/2/1", len(c.Field.Positions), len(c.Game.Phases), len(c.Game.Elements))
	}
	for _, phase := range c.Game.Phases {
		if phase.Active || phase.Frozen || phase.HideScores {
			t.Errorf("phase %q exported with runtime flags active=%v frozen=%v hide=%v, want all false", phase.Name, phase.Active, phase.Frozen, phase.HideScores)
		}
	}
	if c.Clock == (ClockConfig{}) {
		t.Error("export missing clock section")
	}
}

// TestUiViewPhaseScoreStats verifies the dedicated per-phase stats
// page: an admin sees the rendered stats, and a profile without
// GAME:ADMIN is blocked by the guard.
func TestUiViewPhaseScoreStats(t *testing.T) {
	m, d := newTestModuleDSN(t)

	teams := seedTeams(t, d, 3)
	phase := GamePhase{Name: "qualifying", ScoreSummation: "Total"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	for i, tr := range teams {
		seedPhaseScoreField(t, d, phase.ID, tr, []int{10, 30, 20}[i], fmt.Sprintf("Field%d", i))
	}

	target := fmt.Sprintf("/ui/mod/game/schedule/phases/%d/stats", phase.ID)

	code, body, _ := routerReq(t, m, adminContext(), http.MethodGet, target, "")
	if code != http.StatusOK {
		t.Fatalf("admin GET = %d, want 200; body=%s", code, body)
	}
	if !strings.Contains(body, "Phase Score Stats") {
		t.Errorf("body missing page heading; body=%s", body)
	}
	if !strings.Contains(body, teams[0].Name) || !strings.Contains(body, teams[1].Name) {
		t.Errorf("body missing min/max team names; body=%s", body)
	}
	if !strings.Contains(body, "10") || !strings.Contains(body, "30") {
		t.Errorf("body missing min/max scores; body=%s", body)
	}
	// The min (10) and max (30) rows show the field their placement
	// was seeded on.
	if !strings.Contains(body, "Field0") {
		t.Errorf("body missing min placement field; body=%s", body)
	}
	if !strings.Contains(body, "Field1") {
		t.Errorf("body missing max placement field; body=%s", body)
	}

	// The page's back links are absolute (built from the module base
	// path); verify the rendered hrefs and that both targets serve.
	if !strings.Contains(body, fmt.Sprintf("href=\"/ui/mod/game/schedule/phases/%d\"", phase.ID)) {
		t.Errorf("body missing back link to phase schedule; body=%s", body)
	}
	if !strings.Contains(body, "href=\"/ui/mod/game/schedule\"") {
		t.Errorf("body missing back link to phase list; body=%s", body)
	}
	if code, body, _ := routerReq(t, m, adminContext(), http.MethodGet,
		fmt.Sprintf("/ui/mod/game/schedule/phases/%d", phase.ID), ""); code != http.StatusOK {
		t.Errorf("back link to phase schedule = %d, want 200; body=%s", code, body)
	}
	if code, body, _ := routerReq(t, m, adminContext(), http.MethodGet,
		"/ui/mod/game/schedule", ""); code != http.StatusOK {
		t.Errorf("back link to phase list = %d, want 200; body=%s", code, body)
	}

	noPerm := context.WithValue(context.Background(), web.ProfileKey{}, web.Profile{Username: "nobody"})
	code, body, _ = routerReq(t, m, noPerm, http.MethodGet, target, "")
	if code != http.StatusOK {
		t.Fatalf("non-admin GET = %d, want 200 (unauthorized page); body=%s", code, body)
	}
	if !strings.Contains(body, "Unauthorized") {
		t.Errorf("non-admin should see the unauthorized page; body=%s", body)
	}
}
