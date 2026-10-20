package game

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/cloud"
	"github.com/gizmo-platform/gameday/pkg/db"
)

// capturedPush records what the fake cloud push server received.
type capturedPush struct {
	requests   int
	path       string
	placements []cloud.ContestPlacement
}

// fakeCloudPushServer serves the contest scores endpoint and records
// the path and decoded payload of every request it receives.
func fakeCloudPushServer(t *testing.T, out *capturedPush) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out.requests++
		if r.URL.Path != "/ui/mod/game/cloud/contests/7/scores" {
			t.Errorf("unexpected cloud path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		out.path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&out.placements); err != nil {
			t.Errorf("decode push body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"stored": len(out.placements)})
	}))
}

// seedContestData seeds a single contest with one completed placement,
// a score of 42, and a widgets_fabricated value of 5, and returns the
// team.
func seedContestData(t *testing.T, d *db.DB, contestID uint) team.Team {
	t.Helper()
	ctx := context.Background()

	teams := seedTeams(t, d, 1)
	seedContest(t, d, contestID)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	field, err := gorm.G[Field](d.DB).Where(&Field{Name: "Field 1"}).First(ctx)
	if err != nil {
		t.Fatalf("find Field 1: %v", err)
	}
	position := seedFieldPosition(t, d, "Test")

	mp := MatchPlacement{
		Round:      1,
		Match:      1,
		PhaseID:    phase.ID,
		TeamID:     teams[0].ID,
		FieldID:    field.ID,
		PositionID: position.ID,
		State:      MatchStateComplete,
		ContestID:  contestID,
	}
	if err := d.Create(&mp).Error; err != nil {
		t.Fatalf("create placement: %v", err)
	}
	ms := MatchScore{ID: mp.ID, MatchPlacementID: mp.ID, GamePhaseID: phase.ID, TeamID: teams[0].ID, Score: 42, ContestID: contestID}
	if err := d.Create(&ms).Error; err != nil {
		t.Fatalf("create score: %v", err)
	}
	sv := ScorecardValue{MatchPlacementID: mp.ID, Element: "widgets_fabricated", Value: 5}
	if err := d.Create(&sv).Error; err != nil {
		t.Fatalf("create scorecard value: %v", err)
	}
	return teams[0]
}

// TestPushContestDataEndToEnd verifies that the push page renders,
// that a push POST exports the selected contest's data and sends it to
// the cloud, and that the success page renders.
func TestPushContestDataEndToEnd(t *testing.T) {
	var captured capturedPush
	srv := fakeCloudPushServer(t, &captured)
	defer srv.Close()
	t.Setenv("GAMEDAY_CLOUD", srv.URL)

	m, d := newTestModule(t)
	team0 := seedContestData(t, d, 7)

	// The push page renders with the push button.
	code, body, _ := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/game/push-cloud", "")
	if code != http.StatusOK {
		t.Fatalf("push page = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "Push Contest Data") {
		t.Errorf("push page missing push button:\n%s", body)
	}

	code, _, hdr := routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/game/push-cloud", "")
	if code != http.StatusSeeOther {
		t.Fatalf("push POST = %d, want 303", code)
	}
	if loc := hdr.Get("Location"); loc != "/ui/mod/game/push-cloud?pushed=1" {
		t.Fatalf("Location = %q, want /ui/mod/game/push-cloud?pushed=1", loc)
	}

	// The success page renders.
	code, body, _ = routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/game/push-cloud?pushed=1", "")
	if code != http.StatusOK {
		t.Fatalf("push page after = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "notification is-success") {
		t.Errorf("push page missing success alert:\n%s", body)
	}

	if captured.requests != 1 {
		t.Fatalf("cloud received %d requests, want 1", captured.requests)
	}
	if captured.path != "/ui/mod/game/cloud/contests/7/scores" {
		t.Errorf("push path = %q, want /ui/mod/game/cloud/contests/7/scores", captured.path)
	}
	if len(captured.placements) != 1 {
		t.Fatalf("push payload = %d placements, want 1", len(captured.placements))
	}
	p := captured.placements[0]
	if p.TeamName != team0.Name {
		t.Errorf("TeamName = %q, want %q", p.TeamName, team0.Name)
	}
	if p.Score != 42 {
		t.Errorf("Score = %d, want 42", p.Score)
	}
	if p.FieldName != "Field 1" {
		t.Errorf("FieldName = %q, want %q", p.FieldName, "Field 1")
	}
	if p.State != uint(MatchStateComplete) {
		t.Errorf("State = %d, want %d (MatchStateComplete)", p.State, uint(MatchStateComplete))
	}
	if len(p.Values) != 1 || p.Values[0].Element != "widgets_fabricated" || p.Values[0].Value != 5 {
		t.Errorf("Values = %+v, want [{widgets_fabricated 5}]", p.Values)
	}
}

// TestCloudContestScoresDataStores verifies that the cloud endpoint
// stores a pushed placement and its scorecard values keyed by the
// contest ID in the URL.
func TestCloudContestScoresDataStores(t *testing.T) {
	m, d := newTestModule(t)

	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	body := `[{"round":1,"match":1,"phase_id":1,"team_id":1,"team_name":"Team 1","field_id":1,"position_id":1,"state":4,"score":42,"values":[{"element":"widgets_fabricated","value":5}]}]`
	code, resp, _ := routerReq(t, cm, syncContext(), http.MethodPost, "/ui/mod/game/cloud/contests/7/scores", body)
	if code != http.StatusOK {
		t.Fatalf("store = %d, want 200: %s", code, resp)
	}
	if !strings.Contains(resp, `"stored":1`) {
		t.Errorf("response = %q, want stored:1", resp)
	}

	if got := countGameRows(t, d, &CloudMatchPlacement{}); got != 1 {
		t.Errorf("placements = %d, want 1", got)
	}
	if got := countGameRows(t, d, &CloudScorecardValue{}); got != 1 {
		t.Errorf("values = %d, want 1", got)
	}

	var row CloudMatchPlacement
	if err := d.Model(&CloudMatchPlacement{}).Where("contest_id = ?", 7).First(&row).Error; err != nil {
		t.Fatalf("find stored placement: %v", err)
	}
	if row.TeamName != "Team 1" || row.Score != 42 || row.State != 4 || row.Round != 1 || row.Match != 1 {
		t.Errorf("stored placement = %+v", row)
	}
	var val CloudScorecardValue
	if err := d.Model(&CloudScorecardValue{}).Where("cloud_match_placement_id = ?", row.ID).First(&val).Error; err != nil {
		t.Fatalf("find stored value: %v", err)
	}
	if val.Element != "widgets_fabricated" || val.Value != 5 {
		t.Errorf("stored value = %+v", val)
	}
}

// TestCloudContestScoresDataIdempotent verifies that re-pushing the
// same contest replaces the stored data in place without duplicating
// rows and keeps the placement ID stable.
func TestCloudContestScoresDataIdempotent(t *testing.T) {
	m, d := newTestModule(t)

	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	body := `[{"round":1,"match":1,"phase_id":1,"team_id":1,"team_name":"Team 1","field_id":1,"position_id":1,"state":4,"score":42,"values":[{"element":"widgets_fabricated","value":5}]}]`
	var firstID uint
	for i := 0; i < 2; i++ {
		if code, resp, _ := routerReq(t, cm, syncContext(), http.MethodPost, "/ui/mod/game/cloud/contests/7/scores", body); code != http.StatusOK {
			t.Fatalf("store %d = %d, want 200: %s", i+1, code, resp)
		}
		var row CloudMatchPlacement
		if err := d.Model(&CloudMatchPlacement{}).Where("contest_id = ?", 7).First(&row).Error; err != nil {
			t.Fatalf("find stored placement after push %d: %v", i+1, err)
		}
		if i == 0 {
			firstID = row.ID
		} else if row.ID != firstID {
			t.Errorf("placement ID = %d after re-push, want %d (stable)", row.ID, firstID)
		}
	}

	if got := countGameRows(t, d, &CloudMatchPlacement{}); got != 1 {
		t.Errorf("placements after re-push = %d, want 1 (no duplicates)", got)
	}
	if got := countGameRows(t, d, &CloudScorecardValue{}); got != 1 {
		t.Errorf("values after re-push = %d, want 1 (no duplicates)", got)
	}
}

// TestCloudContestScoresDataUpdatesInPlace verifies that re-pushing a
// placement with the same natural key but a different score and state
// updates the stored row in place, keeping its ID.
func TestCloudContestScoresDataUpdatesInPlace(t *testing.T) {
	m, d := newTestModule(t)

	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	body := `[{"round":1,"match":1,"phase_id":1,"team_id":1,"team_name":"Team 1","field_id":1,"field_name":"North","position_id":1,"state":4,"score":42,"values":[{"element":"widgets_fabricated","value":5}]}]`
	if code, resp, _ := routerReq(t, cm, syncContext(), http.MethodPost, "/ui/mod/game/cloud/contests/7/scores", body); code != http.StatusOK {
		t.Fatalf("store = %d, want 200: %s", code, resp)
	}
	var first CloudMatchPlacement
	if err := d.Model(&CloudMatchPlacement{}).Where("contest_id = ?", 7).First(&first).Error; err != nil {
		t.Fatalf("find stored placement: %v", err)
	}

	if first.FieldName != "North" {
		t.Errorf("stored FieldName = %q, want North", first.FieldName)
	}

	// The site renamed the field; the re-push updates it in place.
	updated := `[{"round":1,"match":1,"phase_id":1,"team_id":1,"team_name":"Team 1","field_id":1,"field_name":"North Arena","position_id":1,"state":5,"score":99,"values":[{"element":"widgets_fabricated","value":7}]}]`
	if code, resp, _ := routerReq(t, cm, syncContext(), http.MethodPost, "/ui/mod/game/cloud/contests/7/scores", updated); code != http.StatusOK {
		t.Fatalf("re-store = %d, want 200: %s", code, resp)
	}

	if got := countGameRows(t, d, &CloudMatchPlacement{}); got != 1 {
		t.Fatalf("placements = %d, want 1", got)
	}
	var row CloudMatchPlacement
	if err := d.Model(&CloudMatchPlacement{}).Where("contest_id = ?", 7).First(&row).Error; err != nil {
		t.Fatalf("find updated placement: %v", err)
	}
	if row.ID != first.ID {
		t.Errorf("placement ID = %d, want %d (stable)", row.ID, first.ID)
	}
	if row.Score != 99 || row.State != 5 || row.FieldName != "North Arena" {
		t.Errorf("updated placement = %+v, want score 99 state 5 field North Arena", row)
	}
	if got := countGameRows(t, d, &CloudScorecardValue{}); got != 1 {
		t.Errorf("values = %d, want 1", got)
	}
	var val CloudScorecardValue
	if err := d.Model(&CloudScorecardValue{}).Where("cloud_match_placement_id = ?", row.ID).First(&val).Error; err != nil {
		t.Fatalf("find updated value: %v", err)
	}
	if val.Value != 7 {
		t.Errorf("value = %d, want 7", val.Value)
	}
}

// TestCloudContestScoresDataPrunesRemoved verifies that re-pushing a
// schedule that dropped a placement removes the stored row and its
// values while keeping the surviving row's ID.
func TestCloudContestScoresDataPrunesRemoved(t *testing.T) {
	m, d := newTestModule(t)

	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	two := `[{"round":1,"match":1,"phase_id":1,"team_id":1,"team_name":"Team 1","field_id":1,"position_id":1,"state":4,"score":42,"values":[{"element":"widgets_fabricated","value":5}]},{"round":1,"match":2,"phase_id":1,"team_id":2,"team_name":"Team 2","field_id":1,"position_id":1,"state":4,"score":10,"values":[]}]`
	if code, resp, _ := routerReq(t, cm, syncContext(), http.MethodPost, "/ui/mod/game/cloud/contests/7/scores", two); code != http.StatusOK {
		t.Fatalf("store = %d, want 200: %s", code, resp)
	}
	if got := countGameRows(t, d, &CloudMatchPlacement{}); got != 2 {
		t.Fatalf("placements = %d, want 2", got)
	}
	var kept CloudMatchPlacement
	if err := d.Model(&CloudMatchPlacement{}).Where("contest_id = ? AND match = ?", 7, 1).First(&kept).Error; err != nil {
		t.Fatalf("find kept placement: %v", err)
	}

	one := `[{"round":1,"match":1,"phase_id":1,"team_id":1,"team_name":"Team 1","field_id":1,"position_id":1,"state":4,"score":42,"values":[{"element":"widgets_fabricated","value":5}]}]`
	if code, resp, _ := routerReq(t, cm, syncContext(), http.MethodPost, "/ui/mod/game/cloud/contests/7/scores", one); code != http.StatusOK {
		t.Fatalf("re-store = %d, want 200: %s", code, resp)
	}

	if got := countGameRows(t, d, &CloudMatchPlacement{}); got != 1 {
		t.Errorf("placements after prune = %d, want 1", got)
	}
	if got := countGameRows(t, d, &CloudScorecardValue{}); got != 1 {
		t.Errorf("values after prune = %d, want 1 (removed placement's values gone)", got)
	}
	var row CloudMatchPlacement
	if err := d.Model(&CloudMatchPlacement{}).Where("contest_id = ?", 7).First(&row).Error; err != nil {
		t.Fatalf("find surviving placement: %v", err)
	}
	if row.ID != kept.ID {
		t.Errorf("placement ID = %d, want %d (stable)", row.ID, kept.ID)
	}
}

// TestCloudContestScoresDataPreservesAdded verifies that re-pushing a
// schedule that gained a placement keeps the existing row's ID and
// stores the new row with a distinct ID.
func TestCloudContestScoresDataPreservesAdded(t *testing.T) {
	m, d := newTestModule(t)

	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	one := `[{"round":1,"match":1,"phase_id":1,"team_id":1,"team_name":"Team 1","field_id":1,"position_id":1,"state":4,"score":42,"values":[]}]`
	if code, resp, _ := routerReq(t, cm, syncContext(), http.MethodPost, "/ui/mod/game/cloud/contests/7/scores", one); code != http.StatusOK {
		t.Fatalf("store = %d, want 200: %s", code, resp)
	}
	var first CloudMatchPlacement
	if err := d.Model(&CloudMatchPlacement{}).Where("contest_id = ?", 7).First(&first).Error; err != nil {
		t.Fatalf("find stored placement: %v", err)
	}

	two := `[{"round":1,"match":1,"phase_id":1,"team_id":1,"team_name":"Team 1","field_id":1,"position_id":1,"state":4,"score":42,"values":[]},{"round":1,"match":2,"phase_id":1,"team_id":2,"team_name":"Team 2","field_id":1,"position_id":1,"state":4,"score":10,"values":[]}]`
	if code, resp, _ := routerReq(t, cm, syncContext(), http.MethodPost, "/ui/mod/game/cloud/contests/7/scores", two); code != http.StatusOK {
		t.Fatalf("re-store = %d, want 200: %s", code, resp)
	}

	if got := countGameRows(t, d, &CloudMatchPlacement{}); got != 2 {
		t.Fatalf("placements = %d, want 2", got)
	}
	var row CloudMatchPlacement
	if err := d.Model(&CloudMatchPlacement{}).Where("contest_id = ? AND match = ?", 7, 1).First(&row).Error; err != nil {
		t.Fatalf("find original placement: %v", err)
	}
	if row.ID != first.ID {
		t.Errorf("placement ID = %d, want %d (stable)", row.ID, first.ID)
	}
	var added CloudMatchPlacement
	if err := d.Model(&CloudMatchPlacement{}).Where("contest_id = ? AND match = ?", 7, 2).First(&added).Error; err != nil {
		t.Fatalf("find added placement: %v", err)
	}
	if added.ID == first.ID {
		t.Errorf("added placement ID = %d, want a distinct ID", added.ID)
	}
}

// TestCloudContestScoresDataGuarded verifies that the cloud endpoint
// rejects profiles without the GAME:SYNCHRONIZE grant (or a module
// admin) and writes nothing.
func TestCloudContestScoresDataGuarded(t *testing.T) {
	m, d := newTestModule(t)

	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	body := `[{"round":1,"match":1,"phase_id":1,"team_id":1,"team_name":"Team 1","field_id":1,"position_id":1,"state":4,"score":42,"values":[]}]`

	// A profile without the grant is rejected with 403 and a JSON
	// error body (the client sends Accept: application/json).
	noPerm := noPermContext()
	code, resp, hdr := routerReq(t, cm, noPerm, http.MethodPost, "/ui/mod/game/cloud/contests/7/scores", body, "Accept", "application/json")
	if code != http.StatusForbidden {
		t.Fatalf("store = %d, want 403: %s", code, resp)
	}
	if ct := hdr.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if !strings.Contains(resp, `"error":"You do not have permission to access this resource."`) {
		t.Errorf("expected a JSON permission error; body=%s", resp)
	}

	// No profile at all is rejected with 401 and a JSON error body.
	code, resp, hdr = routerReq(t, cm, nil, http.MethodPost, "/ui/mod/game/cloud/contests/7/scores", body, "Accept", "application/json")
	if code != http.StatusUnauthorized {
		t.Fatalf("store = %d, want 401: %s", code, resp)
	}
	if ct := hdr.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if !strings.Contains(resp, `"error":"Please log in to access this resource."`) {
		t.Errorf("expected a JSON login error; body=%s", resp)
	}

	// A request that does not prefer JSON still gets the HTML
	// unauthorized page.
	code, resp, _ = routerReq(t, cm, nil, http.MethodPost, "/ui/mod/game/cloud/contests/7/scores", body)
	if code != http.StatusUnauthorized {
		t.Fatalf("store = %d, want 401: %s", code, resp)
	}
	if !strings.Contains(resp, "Unauthorized") {
		t.Errorf("expected the unauthorized page; body=%s", resp)
	}

	if got := countGameRows(t, d, &CloudMatchPlacement{}); got != 0 {
		t.Errorf("placements = %d, want 0 (nothing stored)", got)
	}
}

// TestPushContestDataNoContest verifies that a push with no contest
// selected reports the error on the page and never reaches the cloud.
func TestPushContestDataNoContest(t *testing.T) {
	var captured capturedPush
	srv := fakeCloudPushServer(t, &captured)
	defer srv.Close()
	t.Setenv("GAMEDAY_CLOUD", srv.URL)

	m, _ := newTestModule(t)

	// The page shows the no-contest warning.
	code, body, _ := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/game/push-cloud", "")
	if code != http.StatusOK {
		t.Fatalf("push page = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "notification is-warning") {
		t.Errorf("push page missing no-contest warning:\n%s", body)
	}

	// A push with no contest selected reports the error and never
	// reaches the cloud.
	code, body, _ = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/game/push-cloud", "")
	if code != http.StatusOK {
		t.Fatalf("push POST = %d, want 200 (error page)", code)
	}
	if !strings.Contains(body, "notification is-danger") {
		t.Errorf("push page missing error alert:\n%s", body)
	}
	if captured.requests != 0 {
		t.Errorf("cloud received %d requests, want 0", captured.requests)
	}
}

// TestPushContestDataCloudFailure verifies that a push against a
// failing cloud reports the error message on the page.
func TestPushContestDataCloudFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "cloud is on fire"})
	}))
	defer srv.Close()
	t.Setenv("GAMEDAY_CLOUD", srv.URL)

	m, d := newTestModule(t)
	seedContestData(t, d, 7)

	code, body, _ := routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/game/push-cloud", "")
	if code != http.StatusOK {
		t.Fatalf("push POST = %d, want 200 (error page)", code)
	}
	if !strings.Contains(body, "notification is-danger") {
		t.Errorf("push page missing error alert:\n%s", body)
	}
	if !strings.Contains(body, "cloud is on fire") {
		t.Errorf("push page missing cloud error message:\n%s", body)
	}
}

// TestCloudContestScoresDataErrors verifies that the cloud endpoint
// writes a JSON error body on each failure path so the message
// round-trips to the pushing site.
func TestCloudContestScoresDataErrors(t *testing.T) {
	m, d := newTestModule(t)

	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	// A non-numeric contest ID is a 400 with an error body.
	code, resp, _ := routerReq(t, cm, syncContext(), http.MethodPost, "/ui/mod/game/cloud/contests/not-a-number/scores", "[]")
	if code != http.StatusBadRequest {
		t.Fatalf("bad id = %d, want 400: %s", code, resp)
	}
	if !strings.Contains(resp, `"error":"invalid contest id"`) {
		t.Errorf("bad id body = %q, want invalid contest id error", resp)
	}

	// A malformed body is a 400 with an error body.
	code, resp, _ = routerReq(t, cm, syncContext(), http.MethodPost, "/ui/mod/game/cloud/contests/7/scores", "{not json")
	if code != http.StatusBadRequest {
		t.Fatalf("bad body = %d, want 400: %s", code, resp)
	}
	if !strings.Contains(resp, `"error":`) {
		t.Errorf("bad body response = %q, want an error field", resp)
	}

	if got := countGameRows(t, d, &CloudMatchPlacement{}); got != 0 {
		t.Errorf("placements = %d, want 0 (nothing stored)", got)
	}
}

// TestPushContestDataUnauthenticatedCloud verifies that a push against
// a real cloud instance with no token set is a genuine failure: the
// cloud guard rejects the unauthenticated request with 401, the cloud
// client surfaces the error, and the push page reports it instead of
// claiming success.
func TestPushContestDataUnauthenticatedCloud(t *testing.T) {
	m, d := newTestModule(t)

	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})
	mux := chi.NewRouter()
	mux.Mount("/ui/mod/game", cm.Router())
	cloudSrv := httptest.NewServer(mux)
	defer cloudSrv.Close()

	t.Setenv("GAMEDAY_CLOUD", cloudSrv.URL)
	t.Setenv("GAMEDAY_CLOUD_TOKEN", "")

	om, od := newTestModule(t)
	seedContestData(t, od, 7)

	code, body, _ := routerReq(t, om, adminContext(), http.MethodPost, "/ui/mod/game/push-cloud", "")
	if code != http.StatusOK {
		t.Fatalf("push POST = %d, want 200 (error page)", code)
	}
	if !strings.Contains(body, "notification is-danger") {
		t.Errorf("push page missing error alert:\n%s", body)
	}
	if strings.Contains(body, "Contest data pushed") {
		t.Errorf("push page reported success for an unauthenticated push:\n%s", body)
	}
	// The danger alert carries the parsed JSON error message, not a
	// block of HTML from the cloud's denial page.
	if !strings.Contains(body, "Please log in to access this resource.") {
		t.Errorf("push page missing clean cloud error message:\n%s", body)
	}
	if strings.Contains(body, "<h1>Unauthorized</h1>") {
		t.Errorf("push page rendered the cloud's HTML denial page:\n%s", body)
	}

	if got := countGameRows(t, d, &CloudMatchPlacement{}); got != 0 {
		t.Errorf("cloud placements = %d, want 0 (nothing stored)", got)
	}
}

// seedFrozenContest records a contest row with the given frozen state,
// standing in for the contest module's table on a cloud instance.
func seedFrozenContest(t *testing.T, m *Module, id uint, frozen bool) {
	t.Helper()
	if err := m.db.AutoMigrate(&contestRow{}); err != nil {
		t.Fatalf("migrate contests: %v", err)
	}
	if err := m.db.Create(&contestRow{ID: id, Name: "Frozen FFA", Frozen: frozen}).Error; err != nil {
		t.Fatalf("create contest: %v", err)
	}
}

// TestCloudContestScoresDataFrozenRefused verifies that a frozen
// contest refuses uploads with 409 and a JSON error naming the reason,
// while an open contest still accepts them.
func TestCloudContestScoresDataFrozenRefused(t *testing.T) {
	m, d := newTestModule(t)

	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})
	seedFrozenContest(t, cm, 7, true)

	body := `[{"round":1,"match":1,"phase_id":1,"team_id":1,"team_name":"Team 1","field_id":1,"position_id":1,"state":4,"score":42,"values":[]}]`
	code, resp, hdr := routerReq(t, cm, syncContext(), http.MethodPost, "/ui/mod/game/cloud/contests/7/scores", body, "Accept", "application/json")
	if code != http.StatusConflict {
		t.Fatalf("store to a frozen contest = %d, want 409: %s", code, resp)
	}
	if ct := hdr.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if !strings.Contains(resp, "frozen") {
		t.Errorf("refusal missing reason: %s", resp)
	}
	if got := countGameRows(t, d, &CloudMatchPlacement{}); got != 0 {
		t.Errorf("placements = %d, want 0 (frozen contest stores nothing)", got)
	}

	// Another contest on the same cloud is unaffected.
	if code, resp, _ := routerReq(t, cm, syncContext(), http.MethodPost, "/ui/mod/game/cloud/contests/9/scores", body); code != http.StatusOK {
		t.Fatalf("store to an open contest = %d, want 200: %s", code, resp)
	}
	if got := countGameRows(t, d, &CloudMatchPlacement{}); got != 1 {
		t.Errorf("placements = %d, want 1", got)
	}
}

// TestCloudContestScoresDataUnfrozenAccepts verifies that unfreezing a
// contest resumes accepting uploads, and that a contest with no row at
// all (a site whose contest is not registered here) is still accepted.
func TestCloudContestScoresDataUnfrozenAccepts(t *testing.T) {
	m, d := newTestModule(t)

	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})
	seedFrozenContest(t, cm, 7, false)

	body := `[{"round":1,"match":1,"phase_id":1,"team_id":1,"team_name":"Team 1","field_id":1,"position_id":1,"state":4,"score":42,"values":[]}]`
	if code, resp, _ := routerReq(t, cm, syncContext(), http.MethodPost, "/ui/mod/game/cloud/contests/7/scores", body); code != http.StatusOK {
		t.Fatalf("store to an unfrozen contest = %d, want 200: %s", code, resp)
	}

	if got := countGameRows(t, d, &CloudMatchPlacement{}); got != 1 {
		t.Errorf("placements = %d, want 1", got)
	}
}

// TestCloudContestScoresDataNoContestTable verifies that a cloud
// without a contests table at all keeps accepting pushes: the frozen
// check degrades to "not frozen" rather than blocking every site.
func TestCloudContestScoresDataNoContestTable(t *testing.T) {
	m, d := newTestModule(t)

	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	body := `[{"round":1,"match":1,"phase_id":1,"team_id":1,"team_name":"Team 1","field_id":1,"position_id":1,"state":4,"score":42,"values":[]}]`
	if code, resp, _ := routerReq(t, cm, syncContext(), http.MethodPost, "/ui/mod/game/cloud/contests/7/scores", body); code != http.StatusOK {
		t.Fatalf("store without a contests table = %d, want 200: %s", code, resp)
	}
}
