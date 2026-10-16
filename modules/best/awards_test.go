package best

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules/game"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
)

func divisionByName(t *testing.T, d *db.DB, name string) team.Division {
	t.Helper()

	div, err := gorm.G[team.Division](d.DB).Where(&team.Division{Name: name}).First(context.Background())
	if err != nil {
		t.Fatalf("lookup division %q: %v", name, err)
	}
	return div
}

func createDivision(t *testing.T, d *db.DB, name string) team.Division {
	t.Helper()

	div := team.Division{Name: name}
	if err := d.Create(&div).Error; err != nil {
		t.Fatalf("create division %q: %v", name, err)
	}
	return div
}

func createTeam(t *testing.T, d *db.DB, number int, name string, divisionID uint) team.Team {
	t.Helper()

	tm := team.Team{Name: name, Number: number, DivisionID: divisionID}
	if err := d.Create(&tm).Error; err != nil {
		t.Fatalf("create team %d: %v", number, err)
	}
	return tm
}

func setScore(t *testing.T, d *db.DB, teamID uint, key string, value float32) {
	t.Helper()

	st := scoreTypeForKey(t, d, key)
	v := TeamScoreValue{TeamID: teamID, ScoreTypeID: st.ID, Value: value}
	if err := d.Create(&v).Error; err != nil {
		t.Fatalf("set %s score for team %d: %v", key, teamID, err)
	}
}

func awardByName(t *testing.T, results []awardResult, key string) awardResult {
	t.Helper()

	for _, r := range results {
		if r.Award.Key == key {
			return r
		}
	}
	t.Fatalf("award %q not found in results", key)
	return awardResult{}
}

func groupLabel(t *testing.T, result awardResult, label string) awardGroup {
	t.Helper()

	for _, g := range result.Groups {
		if g.Label == label {
			return g
		}
	}
	t.Fatalf("group %q not found in award %q", label, result.Award.Key)
	return awardGroup{}
}

// enableDivisionAwarePhase creates a division-aware game phase so the
// awards are ranked per division (driven by the game module's
// DivisionAware flag).
func enableDivisionAwarePhase(t *testing.T, d *db.DB) {
	t.Helper()

	phase := game.GamePhase{Name: "finals", DivisionAware: true}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create division-aware phase: %v", err)
	}
}

func TestComputeAwardsPerDivision(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	enableDivisionAwarePhase(t, d)

	open := divisionByName(t, d, "Open")
	rookie := createDivision(t, d, "Rookie")

	t101 := createTeam(t, d, 101, "Alpha", open.ID)
	t102 := createTeam(t, d, 102, "Beta", open.ID)
	t201 := createTeam(t, d, 201, "Gamma", rookie.ID)

	setScore(t, d, t101.ID, "notebook", 200)
	setScore(t, d, t102.ID, "notebook", 150)
	setScore(t, d, t201.ID, "notebook", 180)

	results, err := m.computeAwards(ctx)
	if err != nil {
		t.Fatalf("computeAwards: %v", err)
	}

	nb := awardByName(t, results, "notebook")
	if len(nb.Groups) != 2 {
		t.Fatalf("expected 2 division groups, got %d", len(nb.Groups))
	}
	openGroup := groupLabel(t, nb, "Open")
	if len(openGroup.Complete) != 2 || openGroup.Complete[0].Team.Number != 101 {
		t.Errorf("Open notebook ranking = %+v, want team 101 first", openGroup.Complete)
	}
	rookieGroup := groupLabel(t, nb, "Rookie")
	if len(rookieGroup.Complete) != 1 || rookieGroup.Complete[0].Team.Number != 201 {
		t.Errorf("Rookie notebook ranking = %+v, want team 201 first", rookieGroup.Complete)
	}

	// Marketing has no scores, so no award groups.
	mkt := awardByName(t, results, "marketing")
	if len(mkt.Groups) != 0 {
		t.Errorf("expected no marketing groups, got %d", len(mkt.Groups))
	}
}

func TestComputeAwardsTieBreak(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	enableDivisionAwarePhase(t, d)

	open := divisionByName(t, d, "Open")
	rookie := createDivision(t, d, "Rookie")

	// 101 and 102 tie on notebook (200); 102 has the higher marketing
	// score, so 102 wins the notebook award.
	t101 := createTeam(t, d, 101, "Alpha", open.ID)
	t102 := createTeam(t, d, 102, "Beta", open.ID)
	t201 := createTeam(t, d, 201, "Gamma", rookie.ID)

	setScore(t, d, t101.ID, "notebook", 200)
	setScore(t, d, t102.ID, "notebook", 200)
	setScore(t, d, t102.ID, "marketing", 100)
	setScore(t, d, t201.ID, "notebook", 150)

	results, err := m.computeAwards(ctx)
	if err != nil {
		t.Fatalf("computeAwards: %v", err)
	}

	nb := awardByName(t, results, "notebook")
	openGroup := groupLabel(t, nb, "Open")
	if len(openGroup.Complete) != 2 || openGroup.Complete[0].Team.Number != 102 {
		t.Errorf("Open notebook ranking = %+v, want team 102 first (marketing tie-break)", openGroup.Complete)
	}

	// Marketing: only 102 has a value, so 102 wins outright.
	mkt := awardByName(t, results, "marketing")
	mktOpen := groupLabel(t, mkt, "Open")
	if len(mktOpen.Complete) != 1 || mktOpen.Complete[0].Team.Number != 102 {
		t.Errorf("Open marketing ranking = %+v, want team 102 first", mktOpen.Complete)
	}
}

func TestComputeAwardsUnassignedDivision(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	enableDivisionAwarePhase(t, d)

	// newTestModule creates team 42 with no division.
	tm := teamByNumber(t, d)
	setScore(t, d, tm.ID, "video", 75)

	results, err := m.computeAwards(ctx)
	if err != nil {
		t.Fatalf("computeAwards: %v", err)
	}

	video := awardByName(t, results, "video")
	g := groupLabel(t, video, "Unassigned")
	if len(g.Complete) != 1 || g.Complete[0].Team.Number != 42 {
		t.Errorf("Unassigned video ranking = %+v, want team 42 first", g.Complete)
	}
}

func TestComputeAwardsRanking(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	enableDivisionAwarePhase(t, d)

	open := divisionByName(t, d, "Open")

	// Five distinct scores: full 1-2-3-4-5 ranking.
	t101 := createTeam(t, d, 101, "Alpha", open.ID)
	t102 := createTeam(t, d, 102, "Beta", open.ID)
	t103 := createTeam(t, d, 103, "Gamma", open.ID)
	t104 := createTeam(t, d, 104, "Delta", open.ID)
	t105 := createTeam(t, d, 105, "Epsilon", open.ID)

	setScore(t, d, t101.ID, "notebook", 250)
	setScore(t, d, t102.ID, "notebook", 200)
	setScore(t, d, t103.ID, "notebook", 150)
	setScore(t, d, t104.ID, "notebook", 100)
	setScore(t, d, t105.ID, "notebook", 50)

	results, err := m.computeAwards(ctx)
	if err != nil {
		t.Fatalf("computeAwards: %v", err)
	}

	g := groupLabel(t, awardByName(t, results, "notebook"), "Open")
	if len(g.Complete) != 5 {
		t.Fatalf("expected 5 ranked teams, got %d", len(g.Complete))
	}
	wantRanks := []int{1, 2, 3, 4, 5}
	for i, entry := range g.Complete {
		if entry.Rank != wantRanks[i] {
			t.Errorf("rank %d = %d, want %d", i, entry.Rank, wantRanks[i])
		}
	}
	if len(g.Podium) != 3 {
		t.Errorf("expected 3 podium entries, got %d", len(g.Podium))
	}
	if !g.More {
		t.Errorf("expected More to be true with 5 ranked teams")
	}

	// A co-winner at the top shares rank 1 and the next rank is 2
	// (dense ranking).
	t106 := createTeam(t, d, 106, "Zeta", open.ID)
	setScore(t, d, t106.ID, "notebook", 250)

	results, err = m.computeAwards(ctx)
	if err != nil {
		t.Fatalf("computeAwards: %v", err)
	}

	g = groupLabel(t, awardByName(t, results, "notebook"), "Open")
	if len(g.Complete) != 6 {
		t.Fatalf("expected 6 ranked teams, got %d", len(g.Complete))
	}
	wantRanks = []int{1, 1, 2, 3, 4, 5}
	for i, entry := range g.Complete {
		if entry.Rank != wantRanks[i] {
			t.Errorf("rank %d = %d, want %d", i, entry.Rank, wantRanks[i])
		}
	}
	if len(g.Podium) != 4 {
		t.Errorf("expected 4 podium entries (two co-winners at rank 1), got %d", len(g.Podium))
	}
}

// TestComputeAwardsOverall verifies that when no phase in the game is
// division aware, the awards are ranked as a single overall field
// regardless of the teams' divisions.
func TestComputeAwardsOverall(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	// No division-aware phase: the game module reports false, so the
	// awards are overall.
	if m.divisionAware() {
		t.Fatal("expected awards to be overall with no division-aware phase")
	}

	open := divisionByName(t, d, "Open")
	rookie := createDivision(t, d, "Rookie")

	t101 := createTeam(t, d, 101, "Alpha", open.ID)
	t102 := createTeam(t, d, 102, "Beta", open.ID)
	t201 := createTeam(t, d, 201, "Gamma", rookie.ID)

	setScore(t, d, t101.ID, "notebook", 200)
	setScore(t, d, t102.ID, "notebook", 150)
	setScore(t, d, t201.ID, "notebook", 180)

	results, err := m.computeAwards(ctx)
	if err != nil {
		t.Fatalf("computeAwards: %v", err)
	}

	nb := awardByName(t, results, "notebook")
	if len(nb.Groups) != 1 {
		t.Fatalf("expected 1 overall group, got %d", len(nb.Groups))
	}
	g := nb.Groups[0]
	if g.Label != "Overall" {
		t.Errorf("group label = %q, want %q", g.Label, "Overall")
	}
	if len(g.Complete) != 3 || g.Complete[0].Team.Number != 101 {
		t.Errorf("overall notebook ranking = %+v, want team 101 first", g.Complete)
	}
}

func TestAwardsPageRenders(t *testing.T) {
	m, d := newTestModule(t)

	enableDivisionAwarePhase(t, d)

	open := divisionByName(t, d, "Open")
	t101 := createTeam(t, d, 101, "Alpha", open.ID)
	t102 := createTeam(t, d, 102, "Beta", open.ID)
	t103 := createTeam(t, d, 103, "Gamma", open.ID)
	t104 := createTeam(t, d, 104, "Delta", open.ID)
	setScore(t, d, t101.ID, "notebook", 250)
	setScore(t, d, t102.ID, "notebook", 200)
	setScore(t, d, t103.ID, "notebook", 150)
	setScore(t, d, t104.ID, "notebook", 100)

	req := httptest.NewRequest("GET", "http://test.ui/ui/mod/best/awards", nil)
	w := httptest.NewRecorder()
	m.uiViewAwards(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"Best Notebook", "Alpha", "250.00", "Open", "Full ranking", "<details>"} {
		if !strings.Contains(body, want) {
			t.Errorf("awards page missing %q", want)
		}
	}
}

func TestAwardsPageEmpty(t *testing.T) {
	m, _ := newTestModule(t)
	req := httptest.NewRequest("GET", "http://test.ui/ui/mod/best/awards", nil)
	w := httptest.NewRecorder()
	m.uiViewAwards(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "No scores recorded yet.") {
		t.Errorf("awards page missing empty state")
	}
}

// guardAwards exercises the route guard directly: a request with no
// authenticated profile must be denied (401) and never reach the
// handler.
func TestAwardsRouteGuard(t *testing.T) {
	m, _ := newTestModule(t)

	req := httptest.NewRequest("GET", "http://test.ui/ui/mod/best/awards", nil)
	w := httptest.NewRecorder()
	m.Router().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated awards request, got %d", w.Code)
	}
}
