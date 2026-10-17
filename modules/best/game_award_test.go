package best

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gizmo-platform/gameday/modules/game"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
)

// seedFinalPhase creates a frozen phase carrying the Final role and
// records one completed match per team, scored so that teams appear in
// the order given (first = highest score).
func seedFinalPhase(t *testing.T, d *db.DB, teams []team.Team, divisionAware bool) game.GamePhase {
	t.Helper()

	phase := game.GamePhase{
		Name:           "Finals",
		Role:           game.RoleFinal,
		ScoreSummation: "Total",
		Frozen:         true,
		DivisionAware:  divisionAware,
	}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create final phase: %v", err)
	}

	for i, tm := range teams {
		mp := game.MatchPlacement{Match: i + 1, PhaseID: phase.ID, TeamID: tm.ID, FieldID: 1, State: game.MatchStateComplete}
		if err := d.Create(&mp).Error; err != nil {
			t.Fatalf("create placement: %v", err)
		}
		ms := game.MatchScore{Score: (len(teams) - i) * 10, GamePhaseID: phase.ID, TeamID: tm.ID, MatchPlacementID: mp.ID}
		if err := d.Create(&ms).Error; err != nil {
			t.Fatalf("create score: %v", err)
		}
	}
	return phase
}

func gameGroupRanking(t *testing.T, result awardResult, label string) []awardEntry {
	t.Helper()

	g := groupLabel(t, result, label)
	return g.Complete
}

// TestGameAwardPlaces verifies the Game Award ranks the final phase's
// teams 1 through 4 and shows all four places inline, the way the score
// awards show their top places.
func TestGameAwardPlaces(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	open := divisionByName(t, d, "Open")
	teams := []team.Team{
		createTeam(t, d, 101, "Alpha", open.ID),
		createTeam(t, d, 102, "Beta", open.ID),
		createTeam(t, d, 103, "Gamma", open.ID),
		createTeam(t, d, 104, "Delta", open.ID),
	}
	seedFinalPhase(t, d, teams, false)

	results, err := m.computeAwards(ctx)
	if err != nil {
		t.Fatalf("computeAwards: %v", err)
	}

	res := awardByName(t, results, "game")
	g := groupLabel(t, res, "Overall")
	if len(g.Complete) != 4 {
		t.Fatalf("expected 4 ranked finalists, got %d", len(g.Complete))
	}
	for i, entry := range g.Complete {
		if entry.Team.Number != teams[i].Number {
			t.Errorf("rank %d = team %d, want %d", i, entry.Team.Number, teams[i].Number)
		}
		if entry.Rank != i+1 {
			t.Errorf("entry %d rank = %d, want %d", i, entry.Rank, i+1)
		}
	}

	// All four places show inline, so there is nothing to expand.
	if len(g.Podium) != 4 {
		t.Errorf("podium = %d entries, want all 4 places", len(g.Podium))
	}
	if g.More {
		t.Error("expected More to be false when every place shows inline")
	}
}

// TestGameAwardBeyondFourthPlace verifies a deeper final keeps only the
// awarded places inline, with the rest available in the full ranking.
func TestGameAwardBeyondFourthPlace(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	open := divisionByName(t, d, "Open")
	teams := []team.Team{
		createTeam(t, d, 101, "Alpha", open.ID),
		createTeam(t, d, 102, "Beta", open.ID),
		createTeam(t, d, 103, "Gamma", open.ID),
		createTeam(t, d, 104, "Delta", open.ID),
		createTeam(t, d, 105, "Epsilon", open.ID),
	}
	seedFinalPhase(t, d, teams, false)

	results, err := m.computeAwards(ctx)
	if err != nil {
		t.Fatalf("computeAwards: %v", err)
	}

	g := groupLabel(t, awardByName(t, results, "game"), "Overall")
	if len(g.Complete) != 5 {
		t.Fatalf("expected 5 ranked teams, got %d", len(g.Complete))
	}
	if len(g.Podium) != 4 {
		t.Errorf("podium = %d entries, want the awarded 4 places", len(g.Podium))
	}
	if !g.More {
		t.Error("expected More to be true with a team outside the awarded places")
	}
}

// TestGameAwardNoFinalPhase verifies the award reports no places when
// no phase carries the Final role.
func TestGameAwardNoFinalPhase(t *testing.T) {
	m, _ := newTestModule(t)

	results, err := m.computeAwards(context.Background())
	if err != nil {
		t.Fatalf("computeAwards: %v", err)
	}

	res := awardByName(t, results, "game")
	if len(res.Groups) != 0 {
		t.Errorf("expected no groups without a final phase, got %d", len(res.Groups))
	}
}

// TestGameAwardRequiresFrozenPhase verifies an unfrozen final yields no
// places: the results are not official until the phase is frozen.
func TestGameAwardRequiresFrozenPhase(t *testing.T) {
	m, d := newTestModule(t)

	open := divisionByName(t, d, "Open")
	t101 := createTeam(t, d, 101, "Alpha", open.ID)

	phase := game.GamePhase{Name: "Finals", Role: game.RoleFinal, ScoreSummation: "Total"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create unfrozen final: %v", err)
	}
	mp := game.MatchPlacement{Match: 1, PhaseID: phase.ID, TeamID: t101.ID, FieldID: 1, State: game.MatchStateComplete}
	if err := d.Create(&mp).Error; err != nil {
		t.Fatalf("create placement: %v", err)
	}
	ms := game.MatchScore{Score: 30, GamePhaseID: phase.ID, TeamID: t101.ID, MatchPlacementID: mp.ID}
	if err := d.Create(&ms).Error; err != nil {
		t.Fatalf("create score: %v", err)
	}

	results, err := m.computeAwards(context.Background())
	if err != nil {
		t.Fatalf("computeAwards: %v", err)
	}

	res := awardByName(t, results, "game")
	if len(res.Groups) != 0 {
		t.Errorf("expected no groups for an unfrozen final, got %d", len(res.Groups))
	}
}

// TestGameAwardDivisionAware verifies a division aware final yields one
// group per division that has ranked teams, each restarting at rank 1,
// regardless of the contest-wide division awareness.
func TestGameAwardDivisionAware(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	open := divisionByName(t, d, "Open")
	rookie := createDivision(t, d, "Rookie")

	openTeams := []team.Team{
		createTeam(t, d, 101, "Alpha", open.ID),
		createTeam(t, d, 102, "Beta", open.ID),
	}
	rookieTeams := []team.Team{
		createTeam(t, d, 201, "Gamma", rookie.ID),
		createTeam(t, d, 202, "Delta", rookie.ID),
	}
	seedFinalPhase(t, d, append(append([]team.Team{}, openTeams...), rookieTeams...), true)

	results, err := m.computeAwards(ctx)
	if err != nil {
		t.Fatalf("computeAwards: %v", err)
	}

	res := awardByName(t, results, "game")
	if len(res.Groups) != 2 {
		t.Fatalf("expected 2 division groups, got %d", len(res.Groups))
	}
	for _, label := range []string{"Open", "Rookie"} {
		complete := gameGroupRanking(t, res, label)
		if len(complete) != 2 {
			t.Fatalf("%s final ranking = %+v, want 2 teams", label, complete)
		}
		if complete[0].Rank != 1 || complete[1].Rank != 2 {
			t.Errorf("%s final ranks = %d, %d, want 1 and 2",
				label, complete[0].Rank, complete[1].Rank)
		}
	}
}

// TestGameAwardPageRenders verifies the awards page leads with the Game
// Award, rendering its places exactly like the score awards: numeric
// ranks with no special titles.
func TestGameAwardPageRenders(t *testing.T) {
	m, d := newTestModule(t)

	open := divisionByName(t, d, "Open")
	teams := []team.Team{
		createTeam(t, d, 101, "Alpha", open.ID),
		createTeam(t, d, 102, "Beta", open.ID),
		createTeam(t, d, 103, "Gamma", open.ID),
		createTeam(t, d, 104, "Delta", open.ID),
	}
	seedFinalPhase(t, d, teams, false)
	setScore(t, d, teams[0].ID, "notebook", 250)

	req := httptest.NewRequest("GET", "http://test.ui/ui/mod/best/awards", nil)
	w := httptest.NewRecorder()
	m.uiViewAwards(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"Game Award", "Best Notebook", "<td>1</td>", "<td>4</td>", "Delta"} {
		if !strings.Contains(body, want) {
			t.Errorf("awards page missing %q", want)
		}
	}
	for _, notWant := range []string{"1st Place", "2nd Place", "3rd Place", "Game Finalist"} {
		if strings.Contains(body, notWant) {
			t.Errorf("awards page unexpectedly contains %q", notWant)
		}
	}
	if strings.Index(body, "Game Award") > strings.Index(body, "Best Notebook") {
		t.Error("expected the Game Award to render before the score awards")
	}
	// Four places show inline and nothing else, so no expandable
	// ranking appears for the Game Award.
	if strings.Contains(body, "Full ranking") {
		t.Error("expected no full-ranking expander when all four places show inline")
	}
}
