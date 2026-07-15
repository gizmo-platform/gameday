package schedgen

import (
	"testing"
)

func TestBID_Smoke(t *testing.T) {
	// Classic BIBD parameters: v=7, k=3 → Steiner Triple System
	// r = (7-1)/(3-1) = 3, blocks = 7*3/3 = 7
	cfg := Config{
		Fields:    1,
		Positions: 3,
		Teams:     7,
		Rounds:    1,
	}

	gen := NewBID(cfg)
	sched := gen.Generate()

	if len(sched.Rounds) != 1 {
		t.Fatalf("expected 1 round, got %d", len(sched.Rounds))
	}

	// BIBD(7,3,1) needs exactly 7 blocks
	expectedBlocks := 7
	if len(sched.Rounds[0].Matches) != expectedBlocks {
		t.Errorf("expected %d matches, got %d", expectedBlocks, len(sched.Rounds[0].Matches))
	}

	// Every pair should appear together at least once.
	pairs := make(map[[2]int]bool)
	for _, m := range sched.Rounds[0].Matches {
		teams := []int{}
		for _, team := range m.Placements {
			if team >= 0 {
				teams = append(teams, team)
			}
		}
		for i := 0; i < len(teams); i++ {
			for j := i + 1; j < len(teams); j++ {
				a, b := teams[i], teams[j]
				if a > b {
					a, b = b, a
				}
				pairs[[2]int{a, b}] = true
			}
		}
	}

	totalPairs := 7 * 6 / 2 // 21 pairs for 7 teams
	if len(pairs) < totalPairs {
		t.Errorf("not all pairs covered: have %d, need %d", len(pairs), totalPairs)
	}
}

func TestBID_Larger(t *testing.T) {
	cfg := Config{
		Fields:    2,
		Positions: 3,
		Teams:     13,
		Rounds:    1,
	}

	gen := NewBID(cfg)
	sched := gen.Generate()

	if len(sched.Rounds) == 0 {
		t.Fatal("expected at least one round")
	}

	// Check all teams appear at least once.
	seen := make(map[int]bool)
	for _, m := range sched.Rounds[0].Matches {
		for _, team := range m.Placements {
			if team >= 0 {
				seen[team] = true
			}
		}
	}

	for i := 0; i < cfg.Teams; i++ {
		if !seen[i] {
			t.Errorf("team %d never appears", i)
		}
	}
}

func TestBID_TeamsLessThanBlock(t *testing.T) {
	// When teams <= block size, should fall back gracefully.
	cfg := Config{
		Fields:    2,
		Positions: 3,
		Teams:     5,
		Rounds:    1,
	}

	gen := NewBID(cfg)
	sched := gen.Generate()

	if len(sched.Rounds) == 0 {
		t.Fatal("expected at least one round")
	}
}
