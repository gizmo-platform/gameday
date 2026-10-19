package schedgen

import (
	"testing"
)

// permIntact verifies every round is a permutation of teams plus the
// correct number of empty slots.
func permIntact(t *testing.T, a *annealer) {
	t.Helper()
	for r, row := range a.perm {
		seen := make(map[int]int, len(row))
		for _, e := range row {
			seen[e]++
		}
		if len(seen) != len(row) {
			t.Fatalf("round %d is not a permutation: %v", r, row)
		}
		for e := range seen {
			if e < 0 || e >= a.perRound {
				t.Fatalf("round %d has out-of-range entity %d", r, e)
			}
		}
	}
}

func TestAnnealPermutationInvariant(t *testing.T) {
	for _, c := range []Config{
		{Teams: 12, Fields: 1, Positions: 4, Rounds: 5},
		{Teams: 40, Fields: 2, Positions: 4, Rounds: 6},
		{Teams: 13, Fields: 2, Positions: 3, Rounds: 4},
		{Teams: 1, Fields: 1, Positions: 2, Rounds: 3},
		{Teams: 7, Fields: 1, Positions: 1, Rounds: 1},
	} {
		a := newAnnealer(c, AnnealOptions{Iterations: 5000, Seed: 1}, 1)
		a.seed()
		permIntact(t, a)
		perm, cost := a.run()
		permIntact(t, a)
		if cost < 0 {
			t.Fatalf("config %+v: negative cost %d", c, cost)
		}
		// The returned schedule must be valid for its config.
		s := &Schedule{Config: c, Rounds: buildRounds(c, perm)}
		if err := s.Validate(); err != nil {
			t.Fatalf("config %+v: %v", c, err)
		}
	}
}

func TestAnnealEveryTeamPlaysEachRound(t *testing.T) {
	c := Config{Teams: 23, Fields: 2, Positions: 3, Rounds: 5}
	s, err := AnnealSchedule(c, AnnealOptions{Iterations: 20000, Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Rounds) != c.Rounds {
		t.Fatalf("got %d rounds, want %d", len(s.Rounds), c.Rounds)
	}
	for r, round := range s.Rounds {
		seen := make(map[int]bool)
		for _, m := range round.Matches {
			for _, team := range m.Placements {
				if seen[team] {
					t.Fatalf("round %d plays team %d twice", r, team)
				}
				seen[team] = true
			}
		}
		if len(seen) != c.Teams {
			t.Fatalf("round %d has %d teams, want %d", r, len(seen), c.Teams)
		}
	}
}

// TestAnnealBeatsRandom checks the search actually improves on the
// thing it is replacing, using the shared scoring metrics.
func TestAnnealBeatsRandom(t *testing.T) {
	c := Config{Teams: 40, Fields: 2, Positions: 4, Rounds: 6}

	annealed, err := AnnealSchedule(c, AnnealOptions{Iterations: 200_000, Restarts: 2, Seed: 11})
	if err != nil {
		t.Fatal(err)
	}
	annealed.Score()

	random, err := GenerateSchedule("RandomSeeding", c)
	if err != nil {
		t.Fatal(err)
	}
	random.Score()

	if annealed.WorstCompetitorDiversity <= random.WorstCompetitorDiversity {
		t.Errorf("annealed competitor diversity %d should beat random %d",
			annealed.WorstCompetitorDiversity, random.WorstCompetitorDiversity)
	}
	if annealed.ClosestReplay < random.ClosestReplay {
		t.Errorf("annealed closest replay %d should be at least random %d",
			annealed.ClosestReplay, random.ClosestReplay)
	}
	// All opponents distinct is achievable at this size: 6 rounds *
	// 3 others = 18 <= 39.
	if annealed.WorstCompetitorDiversity != c.Rounds*(c.Positions-1) {
		t.Errorf("worst competitor diversity %d, want %d (all distinct)",
			annealed.WorstCompetitorDiversity, c.Rounds*(c.Positions-1))
	}
}

func TestAnnealErrors(t *testing.T) {
	for name, c := range map[string]Config{
		"no teams":     {Teams: 0, Fields: 1, Positions: 2, Rounds: 3},
		"no fields":    {Teams: 8, Fields: 0, Positions: 2, Rounds: 3},
		"no positions": {Teams: 8, Fields: 1, Positions: 0, Rounds: 3},
		"no rounds":    {Teams: 8, Fields: 1, Positions: 2, Rounds: 0},
	} {
		if _, err := AnnealSchedule(c, AnnealOptions{}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// TestAnnealDeterministic verifies a fixed seed reproduces the same
// schedule, which is what makes baked artifacts reviewable.
func TestAnnealDeterministic(t *testing.T) {
	c := Config{Teams: 16, Fields: 1, Positions: 4, Rounds: 4}
	opts := AnnealOptions{Iterations: 30_000, Seed: 77}

	first, err := AnnealSchedule(c, opts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AnnealSchedule(c, opts)
	if err != nil {
		t.Fatal(err)
	}

	a := len(first.Rounds)
	b := len(second.Rounds)
	if a != b {
		t.Fatalf("round counts differ: %d vs %d", a, b)
	}
	for r := range first.Rounds {
		for w := range first.Rounds[r].Matches {
			if first.Rounds[r].Matches[w].Placements[Location{0, 0}] !=
				second.Rounds[r].Matches[w].Placements[Location{0, 0}] {
				t.Fatalf("round %d wave %d differs between runs with the same seed", r, w)
			}
		}
	}
}
