package best

import (
	"testing"

	"github.com/gizmo-platform/gameday/pkg/schedgen"
)

// checkFinalsTableIntegrity verifies the invariants of the finals
// rotation table (a Latin square): every rank 1..4 appears exactly
// once per match (row) and exactly once per position (column).
func checkFinalsTableIntegrity(t *testing.T, name string, table [][4]int) {
	t.Helper()
	const teams = 4

	counts := make(map[int]int)
	for rowIdx, row := range table {
		for col, rank := range row {
			if rank < 1 || rank > teams {
				t.Errorf("%s: rank %d out of range [1,%d]", name, rank, teams)
			}
			if containsInt(row[:col], rank) {
				t.Errorf("%s: rank %d appears twice in match %d", name, rank, rowIdx)
			}
			for other := range table {
				if other == rowIdx {
					continue
				}
				if table[other][col] == rank {
					t.Errorf("%s: rank %d at position %d in both match %d and match %d", name, rank, col, rowIdx, other)
				}
			}
			counts[rank]++
		}
	}
	for rank := 1; rank <= teams; rank++ {
		if counts[rank] != 4 {
			t.Errorf("%s: rank %d appears %d times, want 4", name, rank, counts[rank])
		}
	}
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestFinalsTableIntegrity(t *testing.T) {
	checkFinalsTableIntegrity(t, "finals4", finals4[:])
}

// checkFinalsSchedule verifies the invariants of a finals schedule:
// a single round of four matches, each match places 4 distinct teams,
// every team appears in exactly 4 distinct matches, and Validate()
// passes.
func checkFinalsSchedule(t *testing.T, s *schedgen.Schedule, spot []struct {
	match     int
	positions []int
}) {
	t.Helper()

	if len(s.Rounds) != 1 {
		t.Fatalf("expected 1 round, got %d", len(s.Rounds))
	}
	r := s.Rounds[0]
	if len(r.Matches) != 4 {
		t.Fatalf("expected 4 matches, got %d", len(r.Matches))
	}

	perTeam := make(map[int]map[int]bool)
	for m, match := range r.Matches {
		seen := make(map[int]bool)
		if len(match.Placements) != 4 {
			t.Errorf("match %d: expected 4 placements, got %d", m, len(match.Placements))
			continue
		}
		for pos := 0; pos < 4; pos++ {
			teamIndex := match.Team(0, pos)
			if teamIndex < 0 || teamIndex >= 4 {
				t.Errorf("match %d position %d: team index %d out of range", m, pos, teamIndex)
				continue
			}
			if seen[teamIndex] {
				t.Errorf("match %d: team %d placed in more than one position", m, teamIndex)
			}
			seen[teamIndex] = true
			if perTeam[teamIndex] == nil {
				perTeam[teamIndex] = make(map[int]bool)
			}
			perTeam[teamIndex][m] = true
		}
	}
	for team := 0; team < 4; team++ {
		if got := len(perTeam[team]); got != 4 {
			t.Errorf("team %d appears in %d matches, want 4", team, got)
		}
	}

	for _, want := range spot {
		for pos, rank := range want.positions {
			if got := r.Matches[want.match].Team(0, pos); got != rank-1 {
				t.Errorf("match %d position %d: team index %d, want %d (rank %d)", want.match, pos, got, rank-1, rank)
			}
		}
	}

	if err := s.Validate(); err != nil {
		t.Errorf("Validate() failed: %v", err)
	}
}

func TestFinals4(t *testing.T) {
	cfg := schedgen.Config{Fields: 1, Positions: 4, Teams: 4, Rounds: 4}
	gen := NewBESTFinal(cfg)
	s, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate() returned error: %v", err)
	}

	// F0 -> ranks {1, 2, 3, 4}, F1 -> ranks {4, 3, 2, 1}.
	checkFinalsSchedule(t, s, []struct {
		match     int
		positions []int
	}{
		{0, []int{1, 2, 3, 4}},
		{1, []int{4, 3, 2, 1}},
	})
}

func TestFinalsRejection(t *testing.T) {
	for _, teams := range []int{0, 3, 5} {
		cfg := schedgen.Config{Fields: 1, Positions: 4, Teams: teams, Rounds: 4}
		gen := NewBESTFinal(cfg)
		s, err := gen.Generate()
		if err == nil {
			t.Errorf("teams=%d: expected error, got nil", teams)
		}
		if s != nil {
			t.Errorf("teams=%d: expected nil schedule, got %v", teams, s)
		}
	}

	cfg := schedgen.Config{Fields: 1, Positions: 3, Teams: 4, Rounds: 4}
	gen := NewBESTFinal(cfg)
	s, err := gen.Generate()
	if err == nil {
		t.Error("positions=3: expected error, got nil")
	}
	if s != nil {
		t.Errorf("positions=3: expected nil schedule, got %v", s)
	}
}

func TestFinalsConfig(t *testing.T) {
	var c schedgen.GeneratorConfig = BESTFinalsConfig{}
	if got := c.MaxRounds(); got != 4 {
		t.Errorf("MaxRounds() = %d, want 4", got)
	}
	if got := c.DefaultRounds(); got != 4 {
		t.Errorf("DefaultRounds() = %d, want 4", got)
	}
	if got := c.RoundsDynamic(); got != false {
		t.Errorf("RoundsDynamic() = %v, want false", got)
	}
}

func TestFinalsRegistration(t *testing.T) {
	cfg := schedgen.Config{Fields: 1, Positions: 4, Teams: 4, Rounds: 4}
	s, err := schedgen.GenerateSchedule("BESTFinals", cfg)
	if err != nil {
		t.Fatalf("GenerateSchedule returned error: %v", err)
	}
	if s == nil {
		t.Fatal("GenerateSchedule returned nil schedule")
	}

	if _, err := schedgen.GenerateSchedule("BESTFinals", schedgen.Config{Fields: 1, Positions: 4, Teams: 8, Rounds: 4}); err == nil {
		t.Error("expected error for teams=8 via GenerateSchedule")
	}

	cfgProvider, err := schedgen.GetConfig("BESTFinals")
	if err != nil {
		t.Fatalf("GetConfig returned error: %v", err)
	}
	if cfgProvider == nil {
		t.Fatal("GetConfig returned nil provider")
	}
	if cfgProvider.DefaultRounds() != 4 {
		t.Errorf("registered config DefaultRounds() = %d, want 4", cfgProvider.DefaultRounds())
	}
}
