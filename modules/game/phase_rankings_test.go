package game

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/the-maldridge/authware"

	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
)

// adminLoggedInContext is adminContext plus an authware.User, for
// pages that check both a permission and that a user is logged in.
func adminLoggedInContext() context.Context {
	return context.WithValue(adminContext(), authware.UserKey{}, authware.User{Identity: "tester"})
}

// seedRankedPhase creates a phase and records one placement and one
// distinct score per team (first team scores highest).
func seedRankedPhase(t *testing.T, d *db.DB, phase GamePhase, teams []team.Team) GamePhase {
	t.Helper()
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	for i, tm := range teams {
		mp := MatchPlacement{Match: i + 1, PhaseID: phase.ID, TeamID: tm.ID, FieldID: 1, State: MatchStateComplete}
		if err := d.Create(&mp).Error; err != nil {
			t.Fatalf("create placement: %v", err)
		}
		ms := MatchScore{Score: (len(teams) - i) * 10, GamePhaseID: phase.ID, TeamID: tm.ID, MatchPlacementID: mp.ID}
		if err := d.Create(&ms).Error; err != nil {
			t.Fatalf("create score: %v", err)
		}
	}
	return phase
}

// TestPhaseRankingsPage verifies a frozen phase renders one unpaginated
// table ranked by the configured score summation.
func TestPhaseRankingsPage(t *testing.T) {
	m, d := newTestModule(t)
	teams := seedTeams(t, d, 12)
	phase := seedRankedPhase(t, d, GamePhase{Name: "seeding", ScoreSummation: "Total", Frozen: true}, teams)

	code, body, _ := routerReq(t, m, noPermContext(), http.MethodGet, fmt.Sprintf("/ui/mod/game/schedule/phases/%d/rankings", phase.ID), "")
	if code != http.StatusOK {
		t.Fatalf("rankings = %d, want 200: %s", code, body)
	}

	// All 12 teams render (no pagination), in score order.
	last := -1
	for i, tm := range teams {
		cell := fmt.Sprintf("<td>%d - %s</td>", tm.Number, tm.Name)
		idx := strings.Index(body, cell)
		if idx < 0 {
			t.Fatalf("team %d missing from rankings", tm.Number)
		}
		if idx < last {
			t.Errorf("team %d (rank %d) rendered out of order", tm.Number, i+1)
		}
		last = idx
	}
	if !strings.Contains(body, "<td>120</td>") {
		t.Error("top Total score 120 missing")
	}
	if strings.Contains(body, "<h3>") {
		t.Error("non-division-aware phase rendered a division heading")
	}
}

// TestPhaseRankingsDivisionAware verifies a division aware phase
// renders one table per division, each ranked from 1.
func TestPhaseRankingsDivisionAware(t *testing.T) {
	m, d := newTestModule(t)
	teams := seedTeams(t, d, 4)

	junior := team.Division{Name: "Junior"}
	if err := d.Create(&junior).Error; err != nil {
		t.Fatalf("create division: %v", err)
	}
	for _, tm := range teams[2:] {
		if err := d.Model(&team.Team{}).Where("id = ?", tm.ID).Update("division_id", junior.ID).Error; err != nil {
			t.Fatalf("move team: %v", err)
		}
	}
	phase := seedRankedPhase(t, d, GamePhase{Name: "seeding", ScoreSummation: "Total", Frozen: true, DivisionAware: true}, teams)

	code, body, _ := routerReq(t, m, noPermContext(), http.MethodGet, fmt.Sprintf("/ui/mod/game/schedule/phases/%d/rankings", phase.ID), "")
	if code != http.StatusOK {
		t.Fatalf("rankings = %d, want 200: %s", code, body)
	}
	if got := strings.Count(body, "<table"); got != 2 {
		t.Errorf("tables = %d, want 2 (one per division with teams)", got)
	}
	open, jr := strings.Index(body, "<h3>Open</h3>"), strings.Index(body, "<h3>Junior</h3>")
	if open < 0 || jr < 0 {
		t.Fatalf("division headings missing:\n%s", body)
	}
	// The top team of each division is ranked 1 within its table.
	if got := strings.Count(body, "<td>1</td>"); got < 2 {
		t.Errorf("rank 1 appears %d times, want once per division", got)
	}
}

// TestPhaseRankingsGuards verifies anonymous users are denied with 401
// and a phase that is not frozen is refused.
func TestPhaseRankingsGuards(t *testing.T) {
	m, d := newTestModule(t)
	teams := seedTeams(t, d, 2)
	frozen := seedRankedPhase(t, d, GamePhase{Name: "frozen", ScoreSummation: "Total", Frozen: true}, teams)
	open := seedRankedPhase(t, d, GamePhase{Name: "open", ScoreSummation: "Total"}, teams)

	code, body, _ := routerReq(t, m, nil, http.MethodGet, fmt.Sprintf("/ui/mod/game/schedule/phases/%d/rankings", frozen.ID), "")
	if code != http.StatusUnauthorized {
		t.Errorf("anonymous rankings = %d, want 401", code)
	}
	if strings.Contains(body, "required permission") {
		t.Error("login-only denial names a permission")
	}

	_, body, _ = routerReq(t, m, noPermContext(), http.MethodGet, fmt.Sprintf("/ui/mod/game/schedule/phases/%d/rankings", open.ID), "")
	if !strings.Contains(body, "is not frozen") {
		t.Errorf("unfrozen phase not refused:\n%s", body)
	}
}

// TestPhaseListRankingsButton verifies the Rankings button appears for
// a frozen phase only when a user is logged in, after the Stats button.
func TestPhaseListRankingsButton(t *testing.T) {
	m, d := newTestModule(t)
	teams := seedTeams(t, d, 2)
	frozen := seedRankedPhase(t, d, GamePhase{Name: "frozen", ScoreSummation: "Total", Frozen: true}, teams)
	open := seedRankedPhase(t, d, GamePhase{Name: "open", ScoreSummation: "Total"}, teams)

	frozenLink := fmt.Sprintf(`href="schedule/phases/%d/rankings"`, frozen.ID)
	openLink := fmt.Sprintf(`href="schedule/phases/%d/rankings"`, open.ID)

	_, body, _ := routerReq(t, m, noPermContext(), http.MethodGet, "/ui/mod/game/schedule/", "")
	if !strings.Contains(body, frozenLink) {
		t.Error("Rankings button missing for frozen phase")
	}
	if strings.Contains(body, openLink) {
		t.Error("Rankings button shown for unfrozen phase")
	}

	_, body, _ = routerReq(t, m, nil, http.MethodGet, "/ui/mod/game/schedule/", "")
	if strings.Contains(body, frozenLink) {
		t.Error("Rankings button shown to anonymous user")
	}

	// A logged in admin sees Stats then Rankings.
	_, body, _ = routerReq(t, m, adminLoggedInContext(), http.MethodGet, "/ui/mod/game/schedule/", "")
	stats := strings.Index(body, fmt.Sprintf(`href="schedule/phases/%d/stats"`, frozen.ID))
	rank := strings.Index(body, frozenLink)
	if stats < 0 || rank < 0 || rank < stats {
		t.Errorf("want Stats (%d) before Rankings (%d)", stats, rank)
	}
}
