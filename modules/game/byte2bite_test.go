package game

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/expr-lang/expr"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
)

// stubNotebook stands in for the best module's BESTNotebook filter,
// which this package cannot import.  It ranks the pool by team number
// and includes the first cutoff teams, which is enough to drive the
// byte2bite phase flow; the real notebook ranking is tested in the
// best module.
type stubNotebook struct{}

func (stubNotebook) Name() string { return "BESTNotebook" }

func (stubNotebook) Apply(sctx *AdvancementFilterContext, rule string, mode GamePhaseAdvancementFilterMode, sExpr string) error {
	out, err := expr.Eval(sExpr, sctx)
	if err != nil {
		return err
	}
	pool := make([]team.Team, 0, len(sctx.Pool()))
	for _, t := range sctx.Pool() {
		pool = append(pool, t)
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].Number < pool[j].Number })
	for i, t := range pool {
		if i >= out.(int) {
			break
		}
		sctx.Candidates[t.ID] = t
	}
	return nil
}

// loadByte2Bite imports test_data/byte2bite.yml through the shared
// cloud import path with the stub notebook filter installed for the
// duration of the test.  The stub always replaces any registered
// filter (lint_test registers the real one with a nil database) and
// the previous one is restored afterwards.
func loadByte2Bite(t *testing.T) (*Module, *db.DB) {
	t.Helper()

	prev, had := filters["BESTNotebook"]
	filters["BESTNotebook"] = stubNotebook{}
	t.Cleanup(func() {
		if had {
			filters["BESTNotebook"] = prev
		} else {
			delete(filters, "BESTNotebook")
		}
	})

	b, err := os.ReadFile(filepath.Join("..", "..", "test_data", "byte2bite.yml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := fakeCloudGameServer(t, b)
	t.Cleanup(srv.Close)
	t.Setenv("GAMEDAY_CLOUD", srv.URL)

	m, d := newTestModule(t)
	if code, body := importCloudGame(t, m); code != 303 {
		t.Fatalf("import = %d, want 303: %s", code, body)
	}
	return m, d
}

// playPhase records a completed placement and a distinct score for
// every team in order (first team scores highest), then freezes the
// phase so later phases may select from it.
func playPhase(t *testing.T, d *db.DB, phaseID uint, teams []team.Team) {
	t.Helper()
	for i, tm := range teams {
		mp := MatchPlacement{Round: 0, Match: i + 1, PhaseID: phaseID, TeamID: tm.ID, FieldID: 1, PositionID: 1, State: MatchStateComplete}
		if err := d.Create(&mp).Error; err != nil {
			t.Fatalf("create placement: %v", err)
		}
		ms := MatchScore{Score: (len(teams) - i) * 10, GamePhaseID: phaseID, TeamID: tm.ID, MatchPlacementID: mp.ID}
		if err := d.Create(&ms).Error; err != nil {
			t.Fatalf("create score: %v", err)
		}
	}
	if err := d.Model(&GamePhase{}).Where("id = ?", phaseID).Update("frozen", true).Error; err != nil {
		t.Fatalf("freeze phase: %v", err)
	}
}

// byte2biteState loads the phases (with filters) and their completion.
func byte2biteState(t *testing.T, m *Module) ([]GamePhase, map[uint]bool) {
	t.Helper()
	ctx := context.Background()
	phases, err := gorm.G[GamePhase](m.db.DB).Preload("AdvancementFilters", nil).Order("id").Find(ctx)
	if err != nil {
		t.Fatalf("load phases: %v", err)
	}
	complete, err := m.phaseCompletionStates(ctx, phases)
	if err != nil {
		t.Fatalf("phase completion: %v", err)
	}
	return phases, complete
}

func advancingTeams(t *testing.T, m *Module, phase GamePhase, phases []GamePhase, complete map[uint]bool, teams []team.Team) []team.Team {
	t.Helper()
	adv, _, err := m.runAdvancementFilters(context.Background(), phase, phases, complete, "", teams)
	if err != nil {
		t.Fatalf("runAdvancementFilters(%s): %v", phase.Name, err)
	}
	out := []team.Team{}
	for _, tm := range teams {
		if _, ok := adv[tm.ID]; ok {
			out = append(out, tm)
		}
	}
	return out
}

// TestByte2BiteSmallEvent verifies that with fewer than 8 teams the
// Wildcard is suppressed and the Semifinals are schedulable directly
// from Seeding, taking every team.
func TestByte2BiteSmallEvent(t *testing.T) {
	m, d := loadByte2Bite(t)
	ctx := context.Background()
	teams := seedTeams(t, d, 6)
	playPhase(t, d, 1, teams)

	phases, complete := byte2biteState(t, m)
	wildcard, semis := phases[1], phases[2]

	suppressed, err := m.whenSuppressed(ctx, wildcard, phases, complete, teams, "")
	if err != nil {
		t.Fatalf("whenSuppressed: %v", err)
	}
	if !suppressed {
		t.Error("Wildcard not suppressed with 6 teams")
	}

	can, err := m.phaseSchedulable(ctx, semis, phases, complete, teams)
	if err != nil {
		t.Fatalf("phaseSchedulable: %v", err)
	}
	if !can {
		t.Error("Semifinals not schedulable with 6 teams and no Wildcard")
	}

	if got := advancingTeams(t, m, semis, phases, complete, teams); len(got) != 6 {
		t.Errorf("Semifinals advance %d teams, want 6", len(got))
	}
}

// TestByte2BiteMidEvent verifies that with 10 teams the Wildcard runs
// with the 3 non-top-7 teams and the Semifinals take exactly 8 once the
// Wildcard is complete.
func TestByte2BiteMidEvent(t *testing.T) {
	m, d := loadByte2Bite(t)
	ctx := context.Background()
	teams := seedTeams(t, d, 10)
	playPhase(t, d, 1, teams)

	phases, complete := byte2biteState(t, m)
	wildcard, semis := phases[1], phases[2]

	suppressed, err := m.whenSuppressed(ctx, wildcard, phases, complete, teams, "")
	if err != nil {
		t.Fatalf("whenSuppressed: %v", err)
	}
	if suppressed {
		t.Error("Wildcard suppressed with 10 teams")
	}

	wc := advancingTeams(t, m, wildcard, phases, complete, teams)
	if len(wc) != 3 {
		t.Fatalf("Wildcard advances %d teams, want 3", len(wc))
	}

	can, err := m.phaseSchedulable(ctx, semis, phases, complete, teams)
	if err != nil {
		t.Fatalf("phaseSchedulable: %v", err)
	}
	if can {
		t.Error("Semifinals schedulable before the Wildcard is played")
	}

	playPhase(t, d, 2, wc)
	phases, complete = byte2biteState(t, m)
	if got := advancingTeams(t, m, phases[2], phases, complete, teams); len(got) != 8 {
		t.Errorf("Semifinals advance %d teams, want 8", len(got))
	}
}

// TestByte2BiteLargeEvent verifies that with 40 teams the Wildcard
// takes 8 teams (its cutoff sees the full roster, not the roster left
// after the top 14 are excluded) and the Semifinals take 16.
func TestByte2BiteLargeEvent(t *testing.T) {
	m, d := loadByte2Bite(t)
	teams := seedTeams(t, d, 40)
	playPhase(t, d, 1, teams)

	phases, complete := byte2biteState(t, m)
	wc := advancingTeams(t, m, phases[1], phases, complete, teams)
	if len(wc) != 8 {
		t.Fatalf("Wildcard advances %d teams, want 8", len(wc))
	}

	playPhase(t, d, 2, wc)
	phases, complete = byte2biteState(t, m)
	if got := advancingTeams(t, m, phases[2], phases, complete, teams); len(got) != 16 {
		t.Errorf("Semifinals advance %d teams, want 16", len(got))
	}
}
