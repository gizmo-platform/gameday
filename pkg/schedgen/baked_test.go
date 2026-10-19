package schedgen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBakedRoundTrip(t *testing.T) {
	c := Config{Teams: 12, Fields: 1, Positions: 4, Rounds: 5}
	baked, err := BakeSchedule(c, AnnealOptions{Iterations: 20_000, Seed: 9})
	if err != nil {
		t.Fatal(err)
	}
	if baked.FileName() != "teams12-fields1-positions4-rounds5.yml" {
		t.Errorf("unexpected file name %q", baked.FileName())
	}

	data, err := baked.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(t.TempDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	reparsed, err := ParseBaked(data)
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, data)
	}
	if reparsed.Teams != baked.Teams || reparsed.Rounds != baked.Rounds {
		t.Errorf("shape changed across round trip")
	}
	if len(reparsed.Grid) != 5 || len(reparsed.Grid[0]) != 12 {
		t.Errorf("grid shape wrong: %d rows", len(reparsed.Grid))
	}
}

func TestBakedValidateRejectsBadShapes(t *testing.T) {
	good := func() *BakedSchedule {
		return &BakedSchedule{
			Teams: 4, Fields: 1, Positions: 2, Rounds: 2,
			Grid: [][]int{{0, 1, 2, 3}, {3, 2, 1, 0}},
		}
	}
	if err := good().Validate(); err != nil {
		t.Fatalf("valid schedule rejected: %v", err)
	}

	cases := map[string]func(*BakedSchedule){
		"round count mismatch": func(b *BakedSchedule) { b.Rounds = 3 },
		"short round":          func(b *BakedSchedule) { b.Grid[1] = []int{3, 2, 1} },
		"duplicate team":       func(b *BakedSchedule) { b.Grid[1] = []int{3, 3, 1, 0} },
		"team out of range":    func(b *BakedSchedule) { b.Grid[1] = []int{3, 2, 1, 9} },
	}
	for name, mutate := range cases {
		b := good()
		mutate(b)
		if err := b.Validate(); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestBakedValidateEmptySlots(t *testing.T) {
	// 5 teams in waves of 4: one empty slot per round.
	b := &BakedSchedule{
		Teams: 5, Fields: 1, Positions: 4, Rounds: 2,
		Grid: [][]int{{0, 1, 2, 3, 4, -1, -1, -1}, {4, -1, -1, -1, 0, 1, 2, 3}},
	}
	if err := b.Validate(); err != nil {
		t.Fatalf("empty slots rejected: %v", err)
	}
}

func TestParseBakedFileName(t *testing.T) {
	cases := []struct {
		name            string
		teams, fields   int
		positions, rnds int
		ok              bool
	}{
		{"teams40-fields2-positions4-rounds6.yml", 40, 2, 4, 6, true},
		{"teams8-fields1-positions2-rounds3.yml", 8, 1, 2, 3, true},
		{"README.md", 0, 0, 0, 0, false},
		{"nonsense.yml", 0, 0, 0, 0, false},
		{"teams40-fields2-positions4.yml", 0, 0, 0, 0, false},
		{"teamsX-fields2-positions4-rounds6.yml", 0, 0, 0, 0, false},
	}
	for _, c := range cases {
		teams, fields, positions, rounds, ok := parseBakedFileName(c.name)
		if ok != c.ok {
			t.Errorf("%s: ok=%v want %v", c.name, ok, c.ok)
		}
		if ok && (teams != c.teams || fields != c.fields || positions != c.positions || rounds != c.rnds) {
			t.Errorf("%s: parsed %d/%d/%d/%d", c.name, teams, fields, positions, rounds)
		}
	}
}

// TestMaterializeTruncation verifies a baked design honors a lower
// round cap: each team plays exactly the requested number of matches.
func TestMaterializeTruncation(t *testing.T) {
	c := Config{Teams: 12, Fields: 1, Positions: 4, Rounds: 5}
	baked, err := BakeSchedule(c, AnnealOptions{Iterations: 20_000, Seed: 4})
	if err != nil {
		t.Fatal(err)
	}

	for _, rounds := range []int{1, 3, 5} {
		req := c
		req.Rounds = rounds
		s, err := baked.Materialize(req, 99)
		if err != nil {
			t.Fatalf("rounds=%d: %v", rounds, err)
		}
		if len(s.Rounds) != rounds {
			t.Fatalf("got %d rounds, want %d", len(s.Rounds), rounds)
		}
		if err := s.Validate(); err != nil {
			t.Fatalf("rounds=%d: %v", rounds, err)
		}
		counts := make(map[int]int)
		for _, r := range s.Rounds {
			for _, m := range r.Matches {
				for _, team := range m.Placements {
					counts[team]++
				}
			}
		}
		for team, n := range counts {
			if n != rounds {
				t.Fatalf("rounds=%d team %d plays %d matches", rounds, team, n)
			}
		}
	}

	// Requesting more rounds than the design covers must fail rather
	// than silently under-scheduling.
	req := c
	req.Rounds = 6
	if _, err := baked.Materialize(req, 1); err == nil {
		t.Error("expected an error requesting more rounds than baked")
	}
}

// TestMaterializeRelabels verifies each materialization gets a fresh
// team labeling while preserving schedule quality.
func TestMaterializeRelabels(t *testing.T) {
	c := Config{Teams: 16, Fields: 1, Positions: 4, Rounds: 4}
	baked, err := BakeSchedule(c, AnnealOptions{Iterations: 30_000, Seed: 21})
	if err != nil {
		t.Fatal(err)
	}

	first, err := baked.Materialize(c, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := baked.Materialize(c, 2)
	if err != nil {
		t.Fatal(err)
	}

	first.Score()
	second.Score()
	if first.WorstCompetitorDiversity != second.WorstCompetitorDiversity {
		t.Errorf("relabeling changed quality: %d vs %d",
			first.WorstCompetitorDiversity, second.WorstCompetitorDiversity)
	}

	// The labeling must actually differ across seeds.
	if first.Rounds[0].Matches[0].Placements[Location{0, 0}] ==
		second.Rounds[0].Matches[0].Placements[Location{0, 0}] &&
		first.Rounds[0].Matches[0].Placements[Location{0, 1}] ==
			second.Rounds[0].Matches[0].Placements[Location{0, 1}] {
		t.Error("two seeds produced the same labeling")
	}

	// A zero seed means "no preference": each materialization must
	// still vary, so two events of the same shape do not all receive
	// the identical team assignment.
	blindA, err := baked.Materialize(c, 0)
	if err != nil {
		t.Fatal(err)
	}
	blindB, err := baked.Materialize(c, 0)
	if err != nil {
		t.Fatal(err)
	}
	if blindA.Rounds[0].Matches[0].Placements[Location{0, 0}] ==
		blindB.Rounds[0].Matches[0].Placements[Location{0, 0}] &&
		blindA.Rounds[0].Matches[0].Placements[Location{0, 1}] ==
			blindB.Rounds[0].Matches[0].Placements[Location{0, 1}] {
		t.Error("two zero-seed materializations produced the same labeling")
	}
	blindA.Score()
	if blindA.WorstCompetitorDiversity != first.WorstCompetitorDiversity {
		t.Errorf("zero-seed relabeling changed quality: %d vs %d",
			blindA.WorstCompetitorDiversity, first.WorstCompetitorDiversity)
	}
}

func TestBakedFileNameRoundTrip(t *testing.T) {
	name := BakedFileName(23, 3, 4, 7)
	teams, fields, positions, rounds, ok := parseBakedFileName(name)
	if !ok {
		t.Fatalf("could not parse generated name %q", name)
	}
	if teams != 23 || fields != 3 || positions != 4 || rounds != 7 {
		t.Errorf("round trip: got %d/%d/%d/%d", teams, fields, positions, rounds)
	}
}

func TestBakedGenerationFallsBackToLiveSearch(t *testing.T) {
	// This shape is almost certainly not baked in the repo archive;
	// the generator must fall back to a live search and still produce
	// a valid schedule.
	c := Config{Teams: 9, Fields: 1, Positions: 3, Rounds: 2}
	gen := NewAnnealed(c).(*AnnealedSchedule)
	gen.Options = AnnealOptions{Iterations: 10_000, Seed: 5}
	s, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestBakedArtifactFromDiskIsLoadable writes an artifact with the same
// code path the bake command uses, then loads and materializes it,
// without relying on the embedded archive.
func TestBakedArtifactFromDiskIsLoadable(t *testing.T) {
	dir := t.TempDir()
	c := Config{Teams: 10, Fields: 1, Positions: 2, Rounds: 3}
	baked, err := BakeSchedule(c, AnnealOptions{Iterations: 15_000, Seed: 8})
	if err != nil {
		t.Fatal(err)
	}
	data, err := baked.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, baked.FileName())
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := ParseBaked(onDisk)
	if err != nil {
		t.Fatal(err)
	}
	s, err := loaded.Materialize(c, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}
