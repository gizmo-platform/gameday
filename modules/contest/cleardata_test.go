package contest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/game"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
	"github.com/go-chi/chi/v5"
)

// newClearTestModule wires a cloud contest module to a real game
// module, so the clear action exercises the cross-module
// ContestDataClearer contract rather than a stub.
func newClearTestModule(t *testing.T) (*Module, *db.DB) {
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

	tm := team.New("cloud", d, ws, nil)
	if tm == nil {
		t.Fatal("team.New failed")
	}
	if err := tm.Migrate(); err != nil {
		t.Fatalf("team Migrate: %v", err)
	}

	gm := game.New("cloud", d, ws, modules.ModuleDeps{"team": tm})
	if gm == nil {
		t.Fatal("game.New failed")
	}
	if err := gm.Migrate(); err != nil {
		t.Fatalf("game Migrate: %v", err)
	}

	m := New("cloud", d, ws, modules.ModuleDeps{"team": tm, "game": gm})
	if m == nil {
		t.Fatal("New failed")
	}
	if err := m.Migrate(); err != nil {
		t.Fatalf("contest Migrate: %v", err)
	}
	ws.AddTemplateLoader(m.TemplateLoader())
	m.NavList("/ui/mod/contest")

	return m, d
}

// clearReq posts a clear request and returns the status, body, and
// redirect target.
func clearReq(t *testing.T, m *Module, ctx context.Context, id uint, form url.Values) (int, string, string) {
	t.Helper()

	mux := chi.NewRouter()
	mux.Mount("/ui/mod/contest", m.Router())

	target := "/ui/mod/contest/" + strconv.FormatUint(uint64(id), 10) + "/clear"
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(ctx))

	return rec.Code, rec.Body.String(), rec.Header().Get("Location")
}

// seedCloudResults stores placements, each with one scorecard value,
// for one contest through the game module's tables, and reports how
// many rows were written.
func seedCloudResults(t *testing.T, d *db.DB, contestID uint, matches int) int64 {
	t.Helper()

	for i := 1; i <= matches; i++ {
		p := game.CloudMatchPlacement{
			ContestID: contestID, Round: 1, Match: i, PhaseID: 1,
			TeamID: uint(100 + i), TeamName: "Robot", FieldID: 1, PositionID: 1,
		}
		if err := d.Create(&p).Error; err != nil {
			t.Fatalf("create cloud placement: %v", err)
		}
		v := game.CloudScorecardValue{CloudMatchPlacementID: p.ID, Element: "EID_1", Value: i}
		if err := d.Create(&v).Error; err != nil {
			t.Fatalf("create cloud value: %v", err)
		}
	}
	return int64(matches) * 2
}

// countCloudRows reports the placements and values still stored for a
// contest, including values whose placement has already gone (the
// orphan a bad delete order would leave behind).
func countCloudRows(t *testing.T, d *db.DB, contestID uint) (placements, values int64) {
	t.Helper()

	if err := d.Raw().Raw("SELECT COUNT(*) FROM cloud_match_placements WHERE contest_id = ?", contestID).Scan(&placements).Error; err != nil {
		t.Fatalf("count placements: %v", err)
	}
	if err := d.Raw().Raw(`SELECT COUNT(*) FROM cloud_scorecard_values v
		JOIN cloud_match_placements p ON p.id = v.cloud_match_placement_id
		WHERE p.contest_id = ?`, contestID).Scan(&values).Error; err != nil {
		t.Fatalf("count values: %v", err)
	}
	return placements, values
}

func TestClearContestData(t *testing.T) {
	m, d := newClearTestModule(t)
	reg := seedRegion(t, d, "North")
	target := createContest(t, d, "Regional Championship", reg.ID)
	other := createContest(t, d, "Other Contest", reg.ID)

	seedCloudResults(t, d, target.ID, 3)
	otherRows := seedCloudResults(t, d, other.ID, 2)

	code, body, loc := clearReq(t, m, adminContext(), target.ID, url.Values{"confirm_name": {target.Name}})
	if code != http.StatusSeeOther {
		t.Fatalf("clear = %d, want 303: %s", code, body)
	}
	if !strings.Contains(loc, "cleared=6") || !strings.Contains(loc, "cleared_for="+strconv.FormatUint(uint64(target.ID), 10)) {
		t.Errorf("redirect = %q, want cleared=6 and the contest ID", loc)
	}

	// Both game tables are empty for the target contest, with no
	// orphaned scorecard values anywhere.
	if p, v := countCloudRows(t, d, target.ID); p != 0 || v != 0 {
		t.Errorf("cleared contest still has %d placements, %d values", p, v)
	}
	var orphans int64
	if err := d.Raw().Raw("SELECT COUNT(*) FROM cloud_scorecard_values").Scan(&orphans).Error; err != nil {
		t.Fatalf("count values: %v", err)
	}
	if orphans != otherRows/2 {
		t.Errorf("stored values = %d, want %d (no orphans left behind)", orphans, otherRows/2)
	}
	if p, v := countCloudRows(t, d, other.ID); int64(p+v) != otherRows {
		t.Errorf("other contest lost data: %d placements, %d values, want %d rows", p, v, otherRows)
	}

	// The contest record itself survives.
	if c := fetchContest(t, d, target.ID); c.Name != target.Name {
		t.Errorf("contest record changed: %+v", c)
	}
}

func TestClearContestDataNameMismatch(t *testing.T) {
	m, d := newClearTestModule(t)
	reg := seedRegion(t, d, "North")
	c := createContest(t, d, "Regional Championship", reg.ID)
	rows := seedCloudResults(t, d, c.ID, 2)

	cases := map[string]url.Values{
		"wrong name":   {"confirm_name": {"Not The Name"}},
		"wrong case":   {"confirm_name": {"regional championship"}},
		"missing name": {},
		// Surrounding space is trimmed, but a name that still differs
		// is refused.
		"partial": {"confirm_name": {"Regional"}},
	}
	for name, form := range cases {
		code, body, loc := clearReq(t, m, adminContext(), c.ID, form)
		if code != http.StatusSeeOther {
			t.Fatalf("%s: clear = %d, want 303: %s", name, code, body)
		}
		if !strings.Contains(loc, "clear_error=name") {
			t.Errorf("%s: redirect = %q, want clear_error=name", name, loc)
		}
		if p, v := countCloudRows(t, d, c.ID); int64(p+v) != rows {
			t.Errorf("%s: rows after a refused clear = %d+%d, want %d unchanged", name, p, v, rows)
		}
	}
}

func TestClearContestDataNameWithSpaces(t *testing.T) {
	// The name travels form-encoded, so a contest whose name contains
	// spaces must still clear when it is typed correctly.
	m, d := newClearTestModule(t)
	reg := seedRegion(t, d, "North")
	c := createContest(t, d, "North Region #1 Finals", reg.ID)
	rows := seedCloudResults(t, d, c.ID, 1)

	code, body, loc := clearReq(t, m, adminContext(), c.ID, url.Values{"confirm_name": {c.Name}})
	if code != http.StatusSeeOther {
		t.Fatalf("clear = %d, want 303: %s", code, body)
	}
	if strings.Contains(loc, "clear_error") {
		t.Errorf("redirect = %q, want a successful clear", loc)
	}
	if p, v := countCloudRows(t, d, c.ID); p != 0 || v != 0 {
		t.Errorf("rows remain after clearing: %d %d (wrote %d)", p, v, rows)
	}
}

func TestClearContestDataFrozen(t *testing.T) {
	m, d := newClearTestModule(t)
	reg := seedRegion(t, d, "North")
	c := createContest(t, d, "Frozen FFA", reg.ID)
	setContestFrozen(t, d, c.ID, true)
	rows := seedCloudResults(t, d, c.ID, 2)

	code, body, _ := clearReq(t, m, adminContext(), c.ID, url.Values{"confirm_name": {c.Name}})
	if code == http.StatusSeeOther {
		t.Fatalf("clearing a frozen contest redirected, want refusal")
	}
	if !strings.Contains(body, "frozen") {
		t.Errorf("refusal does not name the frozen reason:\n%s", body)
	}
	if p, v := countCloudRows(t, d, c.ID); int64(p+v) != rows {
		t.Errorf("frozen contest lost rows: %d+%d, want %d unchanged", p, v, rows)
	}
}

func TestClearContestDataRequiresPermission(t *testing.T) {
	m, d := newClearTestModule(t)
	reg := seedRegion(t, d, "North")
	c := createContest(t, d, "Regional Championship", reg.ID)
	rows := seedCloudResults(t, d, c.ID, 1)

	code, body, _ := clearReq(t, m, context.Background(), c.ID, url.Values{"confirm_name": {c.Name}})
	if code != http.StatusUnauthorized {
		t.Fatalf("clear without permission = %d, want 401: %s", code, body)
	}
	if p, v := countCloudRows(t, d, c.ID); int64(p+v) != rows {
		t.Errorf("denied clear removed rows: %d+%d", p, v)
	}
}

func TestClearContestDataUnknownContest(t *testing.T) {
	m, _ := newClearTestModule(t)

	code, body, _ := clearReq(t, m, adminContext(), 9999, url.Values{"confirm_name": {"Anything"}})
	if code == http.StatusSeeOther {
		t.Fatalf("clearing an unknown contest redirected, want the error page: %s", body)
	}
}

func TestClearContestDataNoClearers(t *testing.T) {
	// A contest module built in isolation (nil deps) has nothing to
	// clear: the action succeeds reporting zero rows rather than
	// failing, so the screen behaves the same before other modules are
	// resolved.
	m, d := newTestModule(t)
	reg := seedRegion(t, d, "North")
	c := createContest(t, d, "Regional Championship", reg.ID)

	code, body, loc := clearReq(t, m, adminContext(), c.ID, url.Values{"confirm_name": {c.Name}})
	if code != http.StatusSeeOther {
		t.Fatalf("clear with no clearers = %d, want 303: %s", code, body)
	}
	if !strings.Contains(loc, "cleared=0") {
		t.Errorf("redirect = %q, want cleared=0", loc)
	}
}

func TestClearContestDataTwice(t *testing.T) {
	m, d := newClearTestModule(t)
	reg := seedRegion(t, d, "North")
	c := createContest(t, d, "Regional Championship", reg.ID)
	seedCloudResults(t, d, c.ID, 2)

	for i := 0; i < 2; i++ {
		code, body, loc := clearReq(t, m, adminContext(), c.ID, url.Values{"confirm_name": {c.Name}})
		if code != http.StatusSeeOther {
			t.Fatalf("clear #%d = %d, want 303: %s", i, code, body)
		}
		want := "cleared=4"
		if i == 1 {
			want = "cleared=0"
		}
		if !strings.Contains(loc, want) {
			t.Errorf("clear #%d redirect = %q, want %s", i, loc, want)
		}
	}
	if p, v := countCloudRows(t, d, c.ID); p != 0 || v != 0 {
		t.Errorf("rows remain after two clears: %d %d", p, v)
	}
}

func TestContestListClearControls(t *testing.T) {
	m, d := newClearTestModule(t)
	reg := seedRegion(t, d, "North")
	full := createContest(t, d, "Full Contest", reg.ID)
	empty := createContest(t, d, "Empty Contest", reg.ID)
	frozen := createContest(t, d, "Frozen Contest", reg.ID)
	seedCloudResults(t, d, full.ID, 2)
	seedCloudResults(t, d, frozen.ID, 2)
	setContestFrozen(t, d, frozen.ID, true)

	code, body := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/contest/", nil)
	if code != http.StatusOK {
		t.Fatalf("list = %d, want 200: %s", code, body)
	}

	if !strings.Contains(body, "clear-modal-"+strconv.FormatUint(uint64(full.ID), 10)) {
		t.Error("missing confirmation modal for the contest with stored results")
	}
	if !strings.Contains(body, "Erase all stored results for Full Contest?") {
		t.Error("missing confirmation heading")
	}
	if !strings.Contains(body, "<td>match placements</td>") {
		t.Error("missing match placements row of the itemization")
	}
	if !strings.Contains(body, "<td>scorecard values</td>") {
		t.Error("missing scorecard values row of the itemization")
	}
	if strings.Contains(body, `class="modal" id="clear-modal-`+strconv.FormatUint(uint64(empty.ID), 10)+`"`) {
		t.Error("a modal was rendered for the contest with no stored results")
	}
	if !strings.Contains(body, "is-danger is-disabled") {
		t.Error("the empty contest's Clear Data button is not disabled")
	}
	// A frozen contest has data but cannot be cleared, so its button is
	// disabled and offers no modal; the reason names freezing.
	if strings.Contains(body, `class="modal" id="clear-modal-`+strconv.FormatUint(uint64(frozen.ID), 10)+`"`) {
		t.Error("a modal was rendered for the frozen contest")
	}
	if !strings.Contains(body, "Frozen contests cannot be cleared") {
		t.Error("the frozen contest's Clear Data button does not explain why")
	}
}

func TestContestListClearNotifications(t *testing.T) {
	m, d := newClearTestModule(t)
	reg := seedRegion(t, d, "North")
	c := createContest(t, d, "Regional Championship", reg.ID)

	code, body := routerReq(t, m, adminContext(), http.MethodGet,
		"/ui/mod/contest/?cleared=6&cleared_for="+strconv.FormatUint(uint64(c.ID), 10), nil)
	if code != http.StatusOK {
		t.Fatalf("list = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "Cleared") || !strings.Contains(body, "Regional Championship") {
		t.Errorf("success notification missing or does not name the contest:\n%s", body)
	}

	// A cleared_for ID that no longer exists still renders, without a
	// name.
	code, body = routerReq(t, m, adminContext(), http.MethodGet,
		"/ui/mod/contest/?cleared=3&cleared_for=9999", nil)
	if code != http.StatusOK {
		t.Fatalf("list with an unknown cleared_for = %d, want 200: %s", code, body)
	}

	code, body = routerReq(t, m, adminContext(), http.MethodGet,
		"/ui/mod/contest/?clear_error=name", nil)
	if code != http.StatusOK {
		t.Fatalf("list = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "did not match") {
		t.Errorf("name-mismatch notification missing:\n%s", body)
	}
}
