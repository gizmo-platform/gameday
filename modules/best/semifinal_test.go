package best

import (
	"testing"

	"github.com/gizmo-platform/gameday/pkg/schedgen"
)

// checkTableIntegrity verifies the invariants of a semifinal
// rotation table: every seed 1..teams appears exactly 3 times, and
// each row (match) has 4 distinct seeds.
func checkTableIntegrity(t *testing.T, name string, table [][4]int, teams int) {
	t.Helper()

	counts := make(map[int]int)
	for _, row := range table {
		seen := make(map[int]bool)
		for _, seed := range row {
			if seed < 1 || seed > teams {
				t.Errorf("%s: seed %d out of range [1,%d]", name, seed, teams)
			}
			if seen[seed] {
				t.Errorf("%s: seed %d appears twice in one match", name, seed)
			}
			seen[seed] = true
			counts[seed]++
		}
	}
	for seed := 1; seed <= teams; seed++ {
		if counts[seed] != 3 {
			t.Errorf("%s: seed %d appears %d times, want 3", name, seed, counts[seed])
		}
	}
}

func TestSemifinalTableIntegrity(t *testing.T) {
	checkTableIntegrity(t, "semiFinal8", semiFinal8[:], 8)
	checkTableIntegrity(t, "semiFinal16", semiFinal16[:], 16)
}

func checkSchedule(t *testing.T, s *schedgen.Schedule, teams, matches int, spot []struct {
	match     int
	positions []int
}) {
	t.Helper()

	if len(s.Rounds) != 1 {
		t.Fatalf("expected 1 round, got %d", len(s.Rounds))
	}
	r := s.Rounds[0]
	if len(r.Matches) != matches {
		t.Fatalf("expected %d matches, got %d", matches, len(r.Matches))
	}

	counts := make(map[int]int)
	for m, match := range r.Matches {
		seen := make(map[int]bool)
		if len(match.Placements) != 4 {
			t.Errorf("match %d: expected 4 placements, got %d", m, len(match.Placements))
		}
		for pos := 0; pos < 4; pos++ {
			teamIndex := match.Team(0, pos)
			if teamIndex < 0 || teamIndex >= teams {
				t.Errorf("match %d position %d: team index %d out of range", m, pos, teamIndex)
				continue
			}
			if seen[teamIndex] {
				t.Errorf("match %d: team %d placed in more than one position", m, teamIndex)
			}
			seen[teamIndex] = true
			counts[teamIndex]++
		}
	}
	for team := 0; team < teams; team++ {
		if counts[team] != 3 {
			t.Errorf("team %d appears %d times, want 3", team, counts[team])
		}
	}

	for _, want := range spot {
		for pos, seed := range want.positions {
			if got := r.Matches[want.match].Team(0, pos); got != seed-1 {
				t.Errorf("match %d position %d: team index %d, want %d (seed %d)", want.match, pos, got, seed-1, seed)
			}
		}
	}

	if err := s.Validate(); err != nil {
		t.Errorf("Validate() failed: %v", err)
	}
}

func TestSemifinal8(t *testing.T) {
	cfg := schedgen.Config{Fields: 1, Positions: 4, Teams: 8, Rounds: 3}
	gen := NewBESTSemifinal(cfg)
	s, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate() returned error: %v", err)
	}

	// M0 -> seeds {4, 6, 3, 2}, M5 -> seeds {8, 2, 1, 6}.
	checkSchedule(t, s, 8, 6, []struct {
		match     int
		positions []int
	}{
		{0, []int{4, 6, 3, 2}},
		{5, []int{8, 2, 1, 6}},
	})
}

func TestSemifinal16(t *testing.T) {
	cfg := schedgen.Config{Fields: 1, Positions: 4, Teams: 16, Rounds: 3}
	gen := NewBESTSemifinal(cfg)
	s, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate() returned error: %v", err)
	}

	// M0 -> seeds {4, 13, 1, 16}.
	checkSchedule(t, s, 16, 12, []struct {
		match     int
		positions []int
	}{
		{0, []int{4, 13, 1, 16}},
	})
}

func TestSemifinalRejection(t *testing.T) {
	for _, teams := range []int{0, 7, 10, 12} {
		cfg := schedgen.Config{Fields: 1, Positions: 4, Teams: teams, Rounds: 3}
		gen := NewBESTSemifinal(cfg)
		s, err := gen.Generate()
		if err == nil {
			t.Errorf("teams=%d: expected error, got nil", teams)
		}
		if s != nil {
			t.Errorf("teams=%d: expected nil schedule, got %v", teams, s)
		}
	}
}

// checkMultiFieldSchedule verifies the invariants of a
// multi-field semifinal schedule: every match spans the expected
// number of fields with 4 distinct seeds each, no team is placed
// in two positions of the same match, every team appears in exactly
// 3 distinct matches, and Validate() passes.
func checkMultiFieldSchedule(t *testing.T, s *schedgen.Schedule, teams, fields, matches int) {
	t.Helper()

	if len(s.Rounds) != 1 {
		t.Fatalf("expected 1 round, got %d", len(s.Rounds))
	}
	r := s.Rounds[0]
	if len(r.Matches) != matches {
		t.Fatalf("expected %d matches, got %d", matches, len(r.Matches))
	}

	perTeam := make(map[int]map[int]bool)
	for m, match := range r.Matches {
		seen := make(map[int]bool)
		if len(match.Placements) != fields*4 {
			t.Errorf("match %d: expected %d placements, got %d", m, fields*4, len(match.Placements))
			continue
		}
		for f := 0; f < fields; f++ {
			for pos := 0; pos < 4; pos++ {
				teamIndex := match.Team(f, pos)
				if teamIndex < 0 || teamIndex >= teams {
					t.Errorf("match %d field %d position %d: team index %d out of range", m, f, pos, teamIndex)
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
	}
	for team := 0; team < teams; team++ {
		if got := len(perTeam[team]); got != 3 {
			t.Errorf("team %d appears in %d matches, want 3", team, got)
		}
	}

	if err := s.Validate(); err != nil {
		t.Errorf("Validate() failed: %v", err)
	}
}

func TestSemifinal16TwoFields(t *testing.T) {
	cfg := schedgen.Config{Fields: 2, Positions: 4, Teams: 16, Rounds: 3}
	gen := NewBESTSemifinal(cfg)
	s, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate() returned error: %v", err)
	}
	checkMultiFieldSchedule(t, s, 16, 2, 6)

	// First slot: M0 -> seeds {4, 13, 1, 16} on field 0,
	// M1 -> seeds {5, 10, 3, 15} on field 1.
	for pos, seed := range []int{4, 13, 1, 16} {
		if got := s.Rounds[0].Matches[0].Team(0, pos); got != seed-1 {
			t.Errorf("slot 0 field 0 position %d: team index %d, want %d (seed %d)", pos, got, seed-1, seed)
		}
	}
	for pos, seed := range []int{5, 10, 3, 15} {
		if got := s.Rounds[0].Matches[0].Team(1, pos); got != seed-1 {
			t.Errorf("slot 0 field 1 position %d: team index %d, want %d (seed %d)", pos, got, seed-1, seed)
		}
	}
}

func TestSemifinal16ThreeFields(t *testing.T) {
	cfg := schedgen.Config{Fields: 3, Positions: 4, Teams: 16, Rounds: 3}
	gen := NewBESTSemifinal(cfg)
	s, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate() returned error: %v", err)
	}
	checkMultiFieldSchedule(t, s, 16, 3, 4)
}

func TestSemifinal8TwoFields(t *testing.T) {
	cfg := schedgen.Config{Fields: 2, Positions: 4, Teams: 8, Rounds: 3}
	gen := NewBESTSemifinal(cfg)
	s, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate() returned error: %v", err)
	}
	checkMultiFieldSchedule(t, s, 8, 2, 3)
}

func TestSemifinalFieldRejection(t *testing.T) {
	cases := []struct {
		teams  int
		fields int
	}{
		{16, 4},  // matches 1 and 4 share seeds 4 and 16
		{16, 6},  // matches 1 and 7 share seed 3
		{16, 12}, // matches 1 and 7 share seed 3
		{8, 3},   // matches 1 and 3 share seeds 3 and 4
		{8, 6},   // matches 1 and 3 share seeds 3 and 4
	}
	for _, tc := range cases {
		cfg := schedgen.Config{Fields: tc.fields, Positions: 4, Teams: tc.teams, Rounds: 3}
		gen := NewBESTSemifinal(cfg)
		s, err := gen.Generate()
		if err == nil {
			t.Errorf("teams=%d fields=%d: expected error, got nil", tc.teams, tc.fields)
		}
		if s != nil {
			t.Errorf("teams=%d fields=%d: expected nil schedule, got %v", tc.teams, tc.fields, s)
		}
	}
}

func TestSemifinalConfig(t *testing.T) {
	var c schedgen.GeneratorConfig = BESTSemifinalConfig{}
	if got := c.MaxRounds(); got != 3 {
		t.Errorf("MaxRounds() = %d, want 3", got)
	}
	if got := c.DefaultRounds(); got != 3 {
		t.Errorf("DefaultRounds() = %d, want 3", got)
	}
	if got := c.RoundsDynamic(); got != false {
		t.Errorf("RoundsDynamic() = %v, want false", got)
	}
}

func TestSemifinalRegistration(t *testing.T) {
	cfg := schedgen.Config{Fields: 1, Positions: 4, Teams: 8, Rounds: 3}
	s, err := schedgen.GenerateSchedule("Semifinal", cfg)
	if err != nil {
		t.Fatalf("GenerateSchedule returned error: %v", err)
	}
	if s == nil {
		t.Fatal("GenerateSchedule returned nil schedule")
	}

	if _, err := schedgen.GenerateSchedule("Semifinal", schedgen.Config{Fields: 1, Positions: 4, Teams: 9, Rounds: 3}); err == nil {
		t.Error("expected error for teams=9 via GenerateSchedule")
	}

	cfgProvider, err := schedgen.GetConfig("Semifinal")
	if err != nil {
		t.Fatalf("GetConfig returned error: %v", err)
	}
	if cfgProvider.DefaultRounds() != 3 {
		t.Errorf("registered config DefaultRounds() = %d, want 3", cfgProvider.DefaultRounds())
	}
}
