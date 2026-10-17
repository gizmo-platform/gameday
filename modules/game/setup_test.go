package game

import (
	"context"
	"testing"

	"gorm.io/gorm"
)

// setupConfig builds a minimal setup document with the given phases.
func setupConfig(phases ...GamePhase) Config {
	return Config{
		Field: ConfigField{
			Positions: []FieldPosition{{ID: 1, Name: "red", Color1: "ec5e5e", Color2: "f4a2a2"}},
		},
		Game: Game{Phases: phases},
	}
}

func storedPhase(t *testing.T, ctx context.Context, m *Module, id uint) GamePhase {
	t.Helper()
	p, err := gorm.G[GamePhase](m.db.DB).Where(&GamePhase{ID: id}).First(ctx)
	if err != nil {
		t.Fatalf("load phase %d: %v", id, err)
	}
	return p
}

// TestApplyGameConfigPreservesRuntimeFlags verifies a re-upload of
// already-loaded phases keeps the operations state (Active, Frozen,
// HideScores) that the schedule page owns, while still applying the
// file's design fields.
func TestApplyGameConfigPreservesRuntimeFlags(t *testing.T) {
	m, _ := newTestModule(t)
	ctx := context.Background()

	file := GamePhase{
		ID:             4,
		Name:           "Finals",
		Role:           RoleFinal,
		ScheduleType:   "BESTFinals",
		ScoreSummation: "Total",
	}
	if err := m.applyGameConfig(ctx, setupConfig(file)); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	// Live operations: make the phase active, freeze it, hide scores.
	for _, update := range []struct {
		column string
		value  any
	}{{"active", true}, {"frozen", true}, {"hide_scores", true}} {
		if err := m.db.Model(&GamePhase{}).Where("id = ?", 4).
			Update(update.column, update.value).Error; err != nil {
			t.Fatalf("set %s: %v", update.column, err)
		}
	}

	// Re-upload the same file: the cloud export (and any plain file
	// re-upload) carries these flags as false.
	if err := m.applyGameConfig(ctx, setupConfig(file)); err != nil {
		t.Fatalf("re-apply: %v", err)
	}

	got := storedPhase(t, ctx, m, 4)
	if !got.Active || !got.Frozen || !got.HideScores {
		t.Errorf("runtime flags after re-upload = active:%v frozen:%v hideScores:%v, want all true",
			got.Active, got.Frozen, got.HideScores)
	}
	if got.Role != RoleFinal {
		t.Errorf("role = %q, want %q", got.Role, RoleFinal)
	}

	// Design edits still land: a changed summation and tie-breaker are
	// applied while the runtime flags stay put.
	file.ScoreSummation = "AverageWithMulligan"
	file.TieBreaker = "BESTUnifiedTieBreaker"
	if err := m.applyGameConfig(ctx, setupConfig(file)); err != nil {
		t.Fatalf("design re-apply: %v", err)
	}

	got = storedPhase(t, ctx, m, 4)
	if got.ScoreSummation != "AverageWithMulligan" || got.TieBreaker != "BESTUnifiedTieBreaker" {
		t.Errorf("design fields not applied: summation=%q tieBreaker=%q",
			got.ScoreSummation, got.TieBreaker)
	}
	if !got.Active || !got.Frozen || !got.HideScores {
		t.Error("runtime flags lost while applying a design edit")
	}
}

// TestApplyGameConfigNewPhaseKeepsFileFlags verifies a phase that is
// new to the database still takes the flags from the uploaded file, so
// a game file can introduce an active phase.
func TestApplyGameConfigNewPhaseKeepsFileFlags(t *testing.T) {
	m, _ := newTestModule(t)
	ctx := context.Background()

	file := GamePhase{ID: 9, Name: "Exhibition", Role: RoleExhibition, Active: true}
	if err := m.applyGameConfig(ctx, setupConfig(file)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	got := storedPhase(t, ctx, m, 9)
	if !got.Active {
		t.Errorf("new phase active = false, want true from the file")
	}
}
