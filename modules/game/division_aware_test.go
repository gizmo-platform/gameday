package game

import (
	"testing"
)

// TestDivisionAware verifies that Module.DivisionAware reports whether
// any phase in the game is division aware.
func TestDivisionAware(t *testing.T) {
	m, d := newTestModule(t)

	if m.DivisionAware() {
		t.Fatal("expected DivisionAware to be false with no phases")
	}

	if err := d.Create(&GamePhase{Name: "qualifying", DivisionAware: false}).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	if m.DivisionAware() {
		t.Fatal("expected DivisionAware to be false with only non-division-aware phases")
	}

	if err := d.Create(&GamePhase{Name: "finals", DivisionAware: true}).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	if !m.DivisionAware() {
		t.Fatal("expected DivisionAware to be true once a division-aware phase exists")
	}
}
