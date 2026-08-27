package schedgen

import (
	"fmt"
	"testing"
)

func TestGenerateDivisionSchedule_NoDivisions(t *testing.T) {
	_, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    2,
		Positions: 2,
		Teams:     4,
		Rounds:    2,
	}, nil)
	if err == nil {
		t.Fatal("expected error for empty divisions")
	}
}

func TestGenerateDivisionSchedule_SingleDivision(t *testing.T) {
	divisions := []DivisionConfig{
		{
			ID:          0,
			TeamIndices: []int{0, 1, 2, 3},
		},
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    2,
		Positions: 2,
		Teams:     4,
		Rounds:    2,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Single division with auto-assigned fields should use all fields
	for _, round := range sched.Rounds {
		for _, m := range round.Matches {
			if m.DivisionID != 0 {
				t.Errorf("expected DivisionID 0, got %d", m.DivisionID)
			}
		}
	}
}

func TestGenerateDivisionSchedule_ExplicitFieldPinning(t *testing.T) {
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{0, 1},
			TeamIndices: []int{0, 1, 2, 3},
		},
		{
			ID:          1,
			Fields:      []int{2, 3},
			TeamIndices: []int{4, 5, 6, 7},
		},
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    4,
		Positions: 2,
		Teams:     8,
		Rounds:    2,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sched.Config.Fields != 4 {
		t.Errorf("expected 4 fields, got %d", sched.Config.Fields)
	}
	if sched.Config.Teams != 8 {
		t.Errorf("expected 8 teams, got %d", sched.Config.Teams)
	}

	// Verify division 0 teams only use fields 0-1
	// and division 1 teams only use fields 2-3.
	// After compaction, a single Match may contain teams from both
	// divisions, so we check per-team field usage.
	for _, round := range sched.Rounds {
		for _, m := range round.Matches {
			for loc, team := range m.Placements {
				if team < 4 {
					// Division 0 team
					if loc.Field < 0 || loc.Field > 1 {
						t.Errorf("division 0 team %d uses field %d, expected 0-1", team, loc.Field)
					}
				} else {
					// Division 1 team
					if loc.Field < 2 || loc.Field > 3 {
						t.Errorf("division 1 team %d uses field %d, expected 2-3", team, loc.Field)
					}
				}
			}
		}
	}
}

func TestGenerateDivisionSchedule_TeamRemapping(t *testing.T) {
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{0},
			TeamIndices: []int{0, 1, 2, 3},
		},
		{
			ID:          1,
			Fields:      []int{1},
			TeamIndices: []int{4, 5, 6, 7},
		},
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    2,
		Positions: 4,
		Teams:     8,
		Rounds:    1,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Collect all teams that appear in the schedule.
	seen := make(map[int]bool)
	for _, round := range sched.Rounds {
		for _, m := range round.Matches {
			for _, team := range m.Placements {
				seen[team] = true
			}
		}
	}

	// All 8 global team indices should appear.
	for i := 0; i < 8; i++ {
		if !seen[i] {
			t.Errorf("team %d never appears in schedule", i)
		}
	}

	// Division 0 should not reference teams 4-7, and vice versa.
	// After compaction, matches may contain teams from both divisions,
	// but each team should only appear on its division's fields.
	for _, round := range sched.Rounds {
		for _, m := range round.Matches {
			for loc, team := range m.Placements {
				if team < 4 && loc.Field != 0 {
					t.Errorf("division 0 team %d on field %d (expected field 0)", team, loc.Field)
				}
				if team >= 4 && loc.Field != 1 {
					t.Errorf("division 1 team %d on field %d (expected field 1)", team, loc.Field)
				}
			}
		}
	}
}

func TestGenerateDivisionSchedule_AutoAssignFields(t *testing.T) {
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{},
			TeamIndices: []int{0, 1, 2, 3},
		},
		{
			ID:          1,
			Fields:      []int{},
			TeamIndices: []int{4, 5, 6, 7},
		},
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    4,
		Positions: 2,
		Teams:     8,
		Rounds:    2,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify each division's teams use at least one field.
	// After compaction, matches may span divisions, so track by team.
	divTeamFields := make(map[int]map[int]bool) // team -> set of fields
	for _, round := range sched.Rounds {
		for _, m := range round.Matches {
			for loc, team := range m.Placements {
				if divTeamFields[team] == nil {
					divTeamFields[team] = make(map[int]bool)
				}
				divTeamFields[team][loc.Field] = true
			}
		}
	}

	// Check that teams from both divisions have field assignments.
	div0Teams := []int{0, 1, 2, 3}
	div1Teams := []int{4, 5, 6, 7}
	for _, team := range div0Teams {
		if len(divTeamFields[team]) == 0 {
			t.Errorf("division 0 team %d has no fields assigned", team)
		}
	}
	for _, team := range div1Teams {
		if len(divTeamFields[team]) == 0 {
			t.Errorf("division 1 team %d has no fields assigned", team)
		}
	}
}

func TestGenerateDivisionSchedule_DifferentRoundCounts(t *testing.T) {
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{0},
			Rounds:      3,
			TeamIndices: []int{0, 1, 2, 3},
		},
		{
			ID:          1,
			Fields:      []int{1},
			Rounds:      1,
			TeamIndices: []int{4, 5, 6, 7},
		},
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    2,
		Positions: 4,
		Teams:     8,
		Rounds:    3,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Schedule should have 3 rounds (max of all divisions).
	if len(sched.Rounds) != 3 {
		t.Errorf("expected 3 rounds, got %d", len(sched.Rounds))
	}

	// After round 1, division 1 should have no matches (idle field).
	for roundID := 1; roundID < 3; roundID++ {
		for _, m := range sched.Rounds[roundID].Matches {
			if m.DivisionID == 1 {
				t.Errorf("division 1 has match in round %d but only requested 1 round", roundID)
			}
		}
	}
}

func TestGenerateDivisionSchedule_FieldRemapping(t *testing.T) {
	// Division 0 pinned to fields [2, 3] — local index 0 → global 2, local 1 → global 3.
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{2, 3},
			TeamIndices: []int{0, 1, 2, 3},
		},
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    4,
		Positions: 2,
		Teams:     4,
		Rounds:    1,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All matches should use fields 2-3 only (not 0-1).
	for _, round := range sched.Rounds {
		for _, m := range round.Matches {
			for loc := range m.Placements {
				if loc.Field < 2 || loc.Field > 3 {
					t.Errorf("expected field 2-3, got %d", loc.Field)
				}
			}
		}
	}
}

func TestGenerateDivisionSchedule_DivisionIDs(t *testing.T) {
	divisions := []DivisionConfig{
		{
			ID:          10,
			Fields:      []int{0},
			TeamIndices: []int{0, 1, 2, 3},
		},
		{
			ID:          20,
			Fields:      []int{1},
			TeamIndices: []int{4, 5, 6, 7},
		},
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    2,
		Positions: 4,
		Teams:     8,
		Rounds:    1,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// After compaction, matches from divisions on non-overlapping fields
	// share the same Match slot. Verify teams from both divisions appear.
	seenTeams := make(map[int]bool)
	for _, round := range sched.Rounds {
		for _, m := range round.Matches {
			for _, team := range m.Placements {
				seenTeams[team] = true
			}
		}
	}

	div10Count := 0
	for _, team := range []int{0, 1, 2, 3} {
		if seenTeams[team] {
			div10Count++
		}
	}
	div20Count := 0
	for _, team := range []int{4, 5, 6, 7} {
		if seenTeams[team] {
			div20Count++
		}
	}

	if div10Count == 0 {
		t.Error("expected at least one team from division 10 in schedule")
	}
	if div20Count == 0 {
		t.Error("expected at least one team from division 20 in schedule")
	}
}

func TestAssignFieldsToDivisions_Explicit(t *testing.T) {
	divisions := []DivisionConfig{
		{ID: 0, Fields: []int{0, 1}},
		{ID: 1, Fields: []int{2, 3}},
	}

	result := assignFieldsToDivisions(divisions, 4, 2)

	if len(result[0]) != 2 {
		t.Errorf("division 0: expected 2 fields, got %d", len(result[0]))
	}
	if len(result[1]) != 2 {
		t.Errorf("division 1: expected 2 fields, got %d", len(result[1]))
	}

	// Check explicit pinning is preserved.
	for _, f := range result[0] {
		if f > 1 {
			t.Errorf("division 0 has unexpected field %d", f)
		}
	}
}

func TestAssignFieldsToDivisions_AutoAssign(t *testing.T) {
	divisions := []DivisionConfig{
		{ID: 0, Fields: []int{}, TeamIndices: []int{0, 1}},
		{ID: 1, Fields: []int{}, TeamIndices: []int{2, 3}},
	}

	result := assignFieldsToDivisions(divisions, 4, 2)

	// Both divisions have 2 teams and positions=2, so each needs 1 field.
	// Fields 0-3 are available; round-robin should assign field 0 to div 0, field 1 to div 1.
	if len(result[0]) < 1 {
		t.Error("division 0 got no auto-assigned fields")
	}
	if len(result[1]) < 1 {
		t.Error("division 1 got no auto-assigned fields")
	}

	// Fields should not overlap.
	div0Set := make(map[int]bool)
	for _, f := range result[0] {
		div0Set[f] = true
	}
	for _, f := range result[1] {
		if div0Set[f] {
			t.Errorf("field %d assigned to both divisions", f)
		}
	}
}

func TestAssignFieldsToDivisions_MixedExplicitAndAuto(t *testing.T) {
	divisions := []DivisionConfig{
		{ID: 0, Fields: []int{0}, TeamIndices: []int{0, 1}},
		{ID: 1, Fields: []int{}, TeamIndices: []int{2, 3}},
	}

	result := assignFieldsToDivisions(divisions, 3, 2)

	// Division 0 keeps field 0. Division 1 should get field 1 (auto-assigned).
	if len(result[0]) != 1 || result[0][0] != 0 {
		t.Errorf("division 0 fields: %v, expected [0]", result[0])
	}
	if len(result[1]) < 1 {
		t.Error("division 1 got no auto-assigned fields")
	}

	// Auto-assigned fields should not include field 0.
	for _, f := range result[1] {
		if f == 0 {
			t.Error("auto-assigned field 0 conflicts with explicit pinning")
		}
	}
}

func TestGenerateDivisionSchedule_TeamAppearances(t *testing.T) {
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{0},
			TeamIndices: []int{0, 1, 2, 3},
		},
		{
			ID:          1,
			Fields:      []int{1},
			TeamIndices: []int{4, 5, 6, 7},
		},
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    2,
		Positions: 4,
		Teams:     8,
		Rounds:    2,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify TeamAppearances map references global team indices.
	for roundID, round := range sched.Rounds {
		for team := range round.TeamAppearances {
			if team < 0 || team >= 8 {
				t.Errorf("round %d: team %d out of global range [0, 8)", roundID, team)
			}
		}

		// Verify each team appearance index is valid.
		for team, matchIdx := range round.TeamAppearances {
			if matchIdx < 0 || matchIdx >= len(round.Matches) {
				t.Errorf("round %d: team %d has invalid match index %d (matches: %d)",
					roundID, team, matchIdx, len(round.Matches))
			}
		}
	}
}

func TestGenerateDivisionSchedule_RoundsDefault(t *testing.T) {
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{0},
			Rounds:      0, // Should use global Rounds
			TeamIndices: []int{0, 1, 2, 3},
		},
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    1,
		Positions: 4,
		Teams:     4,
		Rounds:    3,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sched.Rounds) != 3 {
		t.Errorf("expected 3 rounds (from global config), got %d", len(sched.Rounds))
	}
}

func TestGenerateDivisionSchedule_BIDDivisions(t *testing.T) {
	// Two divisions using BIBD generator.
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{0},
			TeamIndices: []int{0, 1, 2, 3, 4, 5, 6},
		},
		{
			ID:          1,
			Fields:      []int{1},
			TeamIndices: []int{7, 8, 9, 10, 11, 12, 13},
		},
	}

	sched, err := GenerateDivisionSchedule("BID", Config{
		Fields:    2,
		Positions: 3,
		Teams:     14,
		Rounds:    1,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sched.Rounds) == 0 {
		t.Fatal("expected at least 1 round")
	}

	// Verify all 14 teams appear.
	seen := make(map[int]bool)
	for _, round := range sched.Rounds {
		for _, m := range round.Matches {
			for _, team := range m.Placements {
				seen[team] = true
			}
		}
	}

	for i := 0; i < 14; i++ {
		if !seen[i] {
			t.Errorf("team %d never appears in BIBD division schedule", i)
		}
	}
}

func TestGenerateDivisionSchedule_ConfigFields(t *testing.T) {
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{0, 1},
			TeamIndices: []int{0, 1, 2, 3},
		},
		{
			ID:          1,
			Fields:      []int{2, 3},
			TeamIndices: []int{4, 5, 6, 7},
		},
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    4,
		Positions: 2,
		Teams:     8,
		Rounds:    2,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Config should reflect global config, not sub-config.
	if sched.Config.Fields != 4 {
		t.Errorf("sched.Config.Fields = %d, want 4", sched.Config.Fields)
	}
	if sched.Config.Teams != 8 {
		t.Errorf("sched.Config.Teams = %d, want 8", sched.Config.Teams)
	}
	if sched.Config.Positions != 2 {
		t.Errorf("sched.Config.Positions = %d, want 2", sched.Config.Positions)
	}

	// Config.Rounds should be max rounds across divisions.
	if sched.Config.Rounds != 2 {
		t.Errorf("sched.Config.Rounds = %d, want 2", sched.Config.Rounds)
	}
}

func TestInterleave_IdleFieldsAfterShortDivision(t *testing.T) {
	// Division 0 has 1 round, Division 1 has 3 rounds.
	// After interleaving, round 1 and 2 should have only div 1 matches.
	div0 := makeDivisionSchedule(1, 1, 2, 4)
	div1 := makeDivisionSchedule(3, 1, 2, 4)

	results := []divResult{
		{config: DivisionConfig{ID: 0, TeamIndices: []int{0, 1, 2, 3}}, schedule: div0},
		{config: DivisionConfig{ID: 1, TeamIndices: []int{4, 5, 6, 7}}, schedule: div1},
	}

	fieldMap := map[int][]int{
		0: {0},
		1: {1},
	}

	sched := interleave(results, fieldMap, Config{Fields: 2, Positions: 2, Teams: 8, Rounds: 3})

	if len(sched.Rounds) != 3 {
		t.Errorf("expected 3 rounds, got %d", len(sched.Rounds))
	}

	// Round 0: both divisions have matches, compacted into fewer slots
	// since they use non-overlapping fields.
	div0Teams := teamsInRound(sched.Rounds[0], []int{0, 1, 2, 3})
	div1Teams := teamsInRound(sched.Rounds[0], []int{4, 5, 6, 7})
	if div0Teams == 0 {
		t.Error("round 0: expected division 0 teams")
	}
	if div1Teams == 0 {
		t.Error("round 0: expected division 1 teams")
	}

	// Round 1: only division 1 has matches.
	div0TeamsR1 := teamsInRound(sched.Rounds[1], []int{0, 1, 2, 3})
	div1TeamsR1 := teamsInRound(sched.Rounds[1], []int{4, 5, 6, 7})
	if div0TeamsR1 != 0 {
		t.Errorf("round 1: expected no division 0 teams, got %d", div0TeamsR1)
	}
	if div1TeamsR1 == 0 {
		t.Error("round 1: expected division 1 teams")
	}
}

// makeDivisionSchedule creates a simple schedule for testing.
func makeDivisionSchedule(rounds, fields, positions, teams int) *Schedule {
	s := &Schedule{
		Config: Config{
			Fields:    fields,
			Positions: positions,
			Teams:     teams,
			Rounds:    rounds,
		},
		Rounds: make([]Round, rounds),
	}

	teamIdx := 0
	for r := 0; r < rounds; r++ {
		var matches []Match
		appearances := make(map[int]int)
		for f := 0; f < fields; f++ {
			m := Match{Placements: make(map[Location]int)}
			for p := 0; p < positions; p++ {
				t := teamIdx % teams
				m.Placements[Location{Field: f, Position: p}] = t
				appearances[t] = f
				teamIdx++
			}
			matches = append(matches, m)
		}
		s.Rounds[r] = Round{Matches: matches, TeamAppearances: appearances}
	}
	return s
}

func countDivisionMatches(round Round, divID int) int {
	count := 0
	for _, m := range round.Matches {
		if m.DivisionID == divID {
			count++
		}
	}
	return count
}

// teamsInRound counts how many of the given teams appear in a round.
func teamsInRound(round Round, teams []int) int {
	teamSet := make(map[int]bool)
	for _, m := range round.Matches {
		for _, team := range m.Placements {
			teamSet[team] = true
		}
	}
	count := 0
	for _, t := range teams {
		if teamSet[t] {
			count++
		}
	}
	return count
}

func TestInterleave_AlternatingDivisions(t *testing.T) {
	// Two divisions sharing the same 2 fields. Each division produces
	// 2 matches per round (8 teams, 4 positions = 2 fields). Interleaving
	// should alternate (A,B,A,B) rather than batch (A,A,B,B).
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{0, 1},
			TeamIndices: []int{0, 1, 2, 3, 4, 5, 6, 7},
		},
		{
			ID:          1,
			Fields:      []int{0, 1},
			TeamIndices: []int{8, 9, 10, 11, 12, 13, 14, 15},
		},
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    2,
		Positions: 4,
		Teams:     16,
		Rounds:    3,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify matches within each round alternate by division ID.
	for ri, round := range sched.Rounds {
		prevDiv := -1
		for mi, m := range round.Matches {
			if mi > 0 && m.DivisionID == prevDiv {
				t.Errorf("round %d, match %d: consecutive matches from same division %d (expected alternating)", ri, mi, m.DivisionID)
			}
			prevDiv = m.DivisionID
		}
	}
}

func TestGenerateDivisionSchedule_SequentialTeamIndices(t *testing.T) {
	// Non-contiguous team indices.
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{0},
			TeamIndices: []int{0, 2, 4, 6},
		},
		{
			ID:          1,
			Fields:      []int{1},
			TeamIndices: []int{1, 3, 5, 7},
		},
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    2,
		Positions: 4,
		Teams:     8,
		Rounds:    1,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	seen := make(map[int]bool)
	for _, round := range sched.Rounds {
		for _, m := range round.Matches {
			for _, team := range m.Placements {
				seen[team] = true
			}
		}
	}

	// All 8 teams should appear, even though they're split non-contiguously.
	for i := 0; i < 8; i++ {
		if !seen[i] {
			t.Errorf("team %d never appears", i)
		}
	}
}

func TestGenerateDivisionSchedule_FewerTeamsThanFields(t *testing.T) {
	// Edge case: one team per position on one field.
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{0, 1},
			TeamIndices: []int{0, 1},
		},
	}

	_, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    2,
		Positions: 1,
		Teams:     2,
		Rounds:    1,
	}, divisions)
	// Should succeed — 2 teams, 2 fields, 1 position.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func ExampleGenerateDivisionSchedule() {
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{0, 1},
			TeamIndices: []int{0, 1, 2, 3},
		},
		{
			ID:          1,
			Fields:      []int{2, 3},
			TeamIndices: []int{4, 5, 6, 7},
		},
	}

	cfg := Config{
		Fields:    4,
		Positions: 2,
		Teams:     8,
		Rounds:    2,
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", cfg, divisions)
	if err != nil {
		fmt.Printf("error: %v", err)
		return
	}

	fmt.Printf("rounds: %d, fields: %d, teams: %d\n",
		len(sched.Rounds), sched.Config.Fields, sched.Config.Teams)
	// Output: rounds: 2, fields: 4, teams: 8
}

func TestInterleave_CompactNonConflictingFields(t *testing.T) {
	// Two divisions on non-overlapping fields should be compacted into
	// a single Match per round (the core compaction scenario).
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{0},
			TeamIndices: []int{0, 1, 2, 3},
		},
		{
			ID:          1,
			Fields:      []int{1},
			TeamIndices: []int{4, 5, 6, 7},
		},
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    2,
		Positions: 4,
		Teams:     8,
		Rounds:    1,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both divisions on non-overlapping fields → should be 1 compacted match.
	if len(sched.Rounds) != 1 {
		t.Fatalf("expected 1 round, got %d", len(sched.Rounds))
	}
	if len(sched.Rounds[0].Matches) != 1 {
		t.Errorf("expected 1 compacted match, got %d", len(sched.Rounds[0].Matches))
	}

	// Verify all 8 teams appear in that single match.
	teams := sched.Rounds[0].Matches[0].Placements
	if len(teams) != 8 {
		t.Errorf("expected 8 team placements in compacted match, got %d", len(teams))
	}
}

func TestInterleave_NoCompactPreventsMerging(t *testing.T) {
	// Two divisions on non-overlapping fields with NoCompact should
	// keep their Matches separate (2 Matches, not 1).
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{0},
			TeamIndices: []int{0, 1, 2, 3},
			NoCompact:   true,
		},
		{
			ID:          1,
			Fields:      []int{1},
			TeamIndices: []int{4, 5, 6, 7},
		},
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    2,
		Positions: 4,
		Teams:     8,
		Rounds:    1,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// NoCompact on division 0 → should keep 2 separate matches.
	if len(sched.Rounds) != 1 {
		t.Fatalf("expected 1 round, got %d", len(sched.Rounds))
	}
	if len(sched.Rounds[0].Matches) != 2 {
		t.Errorf("expected 2 separate matches (NoCompact), got %d", len(sched.Rounds[0].Matches))
	}

	// Verify both divisions are still present.
	seenTeams := make(map[int]bool)
	for _, m := range sched.Rounds[0].Matches {
		for _, team := range m.Placements {
			seenTeams[team] = true
		}
	}
	for i := 0; i < 8; i++ {
		if !seenTeams[i] {
			t.Errorf("team %d missing from schedule", i)
		}
	}
}

func TestInterleave_NoCompactBothDivisions(t *testing.T) {
	// When both divisions have NoCompact, they should also stay separate.
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{0},
			TeamIndices: []int{0, 1, 2, 3},
			NoCompact:   true,
		},
		{
			ID:          1,
			Fields:      []int{1},
			TeamIndices: []int{4, 5, 6, 7},
			NoCompact:   true,
		},
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    2,
		Positions: 4,
		Teams:     8,
		Rounds:    1,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(sched.Rounds[0].Matches) != 2 {
		t.Errorf("expected 2 separate matches (both NoCompact), got %d", len(sched.Rounds[0].Matches))
	}
}

func TestInterleave_NoCompactSameDivisionAllowsMerge(t *testing.T) {
	// A single NoCompact division with multiple matches per round should
	// still merge its own matches together (NoCompact only blocks cross-division merges).
	divisions := []DivisionConfig{
		{
			ID:          0,
			Fields:      []int{0, 1},
			TeamIndices: []int{0, 1, 2, 3, 4, 5, 6, 7},
			NoCompact:   true,
		},
	}

	sched, err := GenerateDivisionSchedule("RandomSeeding", Config{
		Fields:    2,
		Positions: 4,
		Teams:     8,
		Rounds:    1,
	}, divisions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With 2 fields and 4 positions per field, all 8 teams can fit in 1 match.
	if len(sched.Rounds[0].Matches) != 1 {
		t.Errorf("expected 1 match (single division, NoCompact still merges own matches), got %d", len(sched.Rounds[0].Matches))
	}
}
