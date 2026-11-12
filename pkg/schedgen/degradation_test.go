package schedgen

import "testing"

func TestTierOf(t *testing.T) {
	cases := []struct {
		teams, slots int
		want         WidthTier
	}{
		{12, 4, TierExact},
		{10, 4, TierShortWave},
		{9, 4, TierSoloWave},
		{4, 4, TierExact},
		{1, 4, TierSoloWave},
		{0, 4, TierExact},
		{5, 0, TierSoloWave},
	}
	for _, c := range cases {
		if got := TierOf(c.teams, c.slots); got != c.want {
			t.Errorf("TierOf(%d, %d) = %v, want %v", c.teams, c.slots, got, c.want)
		}
	}
}

func TestOpponentCeiling(t *testing.T) {
	cases := []struct {
		rounds, positions, teams int
		want                     int
	}{
		{6, 4, 9, 8},
		{2, 4, 13, 6},
		{6, 3, 13, 12},
		{0, 4, 9, 0},
		{6, 1, 9, 0},
		{3, 2, 2, 1},
	}
	for _, c := range cases {
		if got := OpponentCeiling(c.rounds, c.positions, c.teams); got != c.want {
			t.Errorf("OpponentCeiling(%d, %d, %d) = %d, want %d",
				c.rounds, c.positions, c.teams, got, c.want)
		}
	}
}

func TestCandidateWidthsOrder(t *testing.T) {
	opts := CandidateWidths(9, 1, 4)
	if len(opts) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(opts))
	}
	for i, want := range []int{4, 3, 2} {
		if opts[i].Positions != want {
			t.Errorf("candidate %d: Positions = %d, want %d", i, opts[i].Positions, want)
		}
	}
	if opts[0].Tier != TierSoloWave || opts[1].Tier != TierExact {
		t.Errorf("unexpected tiers for 9 teams: %v, %v", opts[0].Tier, opts[1].Tier)
	}
	if opts[1].Waves != 3 || opts[1].LastWaveTeams != 3 {
		t.Errorf("9 teams at 3 slots: waves = %d, last = %d, want 3 and 3",
			opts[1].Waves, opts[1].LastWaveTeams)
	}
	if opts[0].Waves != 3 || opts[0].LastWaveTeams != 1 {
		t.Errorf("9 teams at 4 slots: waves = %d, last = %d, want 3 and 1",
			opts[0].Waves, opts[0].LastWaveTeams)
	}
}

func TestSelectWidthDegradesSoloToExact(t *testing.T) {
	// 9 teams / 1 field / 4 positions: the 4-wide wave leaves a solo
	// team, 3-wide divides exactly, so the width degrades.
	rep := SelectWidth(9, 1, 4, 6, nil)
	if !rep.Degraded {
		t.Fatalf("expected degradation for 9 teams, chose %+v", rep.Chosen)
	}
	if rep.Chosen.Positions != 3 {
		t.Errorf("chosen positions = %d, want 3", rep.Chosen.Positions)
	}
	if rep.Chosen.Tier != TierExact {
		t.Errorf("chosen tier = %v, want exact", rep.Chosen.Tier)
	}
	if rep.Chosen.Reason == "" {
		t.Error("expected an operator-facing reason")
	}
}

func TestSelectWidthStaysWhenAllSolo(t *testing.T) {
	// 13 teams: every width leaves a solo wave, so the physical
	// maximum wins on the widest-first tie-break and cost (which is
	// not comparable across shapes) never enters.
	rep := SelectWidth(13, 1, 4, 6, nil)
	if rep.Degraded {
		t.Errorf("expected no degradation for 13 teams, chose %+v", rep.Chosen)
	}
	if rep.Chosen.Positions != 4 {
		t.Errorf("chosen positions = %d, want 4", rep.Chosen.Positions)
	}
}

func TestSelectWidthStaysWhenExact(t *testing.T) {
	rep := SelectWidth(12, 1, 4, 6, nil)
	if rep.Degraded {
		t.Errorf("expected no degradation for 12 teams, chose %+v", rep.Chosen)
	}
	if rep.Chosen.Positions != 4 {
		t.Errorf("chosen positions = %d, want 4", rep.Chosen.Positions)
	}
}

func TestSelectWidthStaysWhenSoloAvoidableOnlyByCeilingLoss(t *testing.T) {
	// 10 teams / 6 rounds: 4-wide is a short wave, 2-wide is exact
	// but caps opponents at 6 instead of 9, so it is rejected and the
	// widest candidate stays.
	rep := SelectWidth(10, 1, 4, 6, nil)
	if rep.Degraded {
		t.Errorf("expected no degradation for 10 teams, chose %+v", rep.Chosen)
	}
	if rep.Chosen.Positions != 4 {
		t.Errorf("chosen positions = %d, want 4", rep.Chosen.Positions)
	}
}

func TestSelectWidthRejectsCeilingLoss(t *testing.T) {
	// 9 teams / 2 rounds: even though 4-wide leaves a solo wave, no
	// narrower width keeps the matchup ceiling, so nothing degrades.
	rep := SelectWidth(9, 1, 4, 2, nil)
	if rep.Degraded {
		t.Errorf("expected no degradation at 2 rounds, chose %+v", rep.Chosen)
	}
	if rep.Chosen.Positions != 4 {
		t.Errorf("chosen positions = %d, want 4", rep.Chosen.Positions)
	}
}

func TestSelectWidthMultiField(t *testing.T) {
	// 9 teams / 2 fields / 4 positions = 8 slots: remainder 1, while
	// 3 positions (6 slots) gives a short wave that still has
	// opponents for everyone.
	rep := SelectWidth(9, 2, 4, 6, nil)
	if rep.Chosen.Positions != 3 {
		t.Errorf("chosen positions = %d, want 3 (chose %+v)", rep.Chosen.Positions, rep.Chosen)
	}
	if !rep.Degraded {
		t.Error("expected degradation")
	}
}

func TestSelectWidthDegenerateInputs(t *testing.T) {
	t.Run("max positions one", func(t *testing.T) {
		rep := SelectWidth(9, 1, 1, 6, nil)
		if len(rep.Considered) != 0 {
			t.Errorf("expected no candidates, got %d", len(rep.Considered))
		}
		if rep.Chosen.Positions != 1 || rep.Degraded {
			t.Errorf("chose %+v degraded %v", rep.Chosen, rep.Degraded)
		}
	})
	t.Run("single team", func(t *testing.T) {
		rep := SelectWidth(1, 1, 4, 6, nil)
		if rep.Degraded {
			t.Errorf("expected no degradation for a single team, chose %+v", rep.Chosen)
		}
		if rep.Chosen.Positions != 4 {
			t.Errorf("chosen positions = %d, want 4", rep.Chosen.Positions)
		}
	})
	t.Run("teams below width", func(t *testing.T) {
		rep := SelectWidth(3, 1, 4, 6, nil)
		if rep.Chosen.Positions > 4 {
			t.Errorf("chosen positions = %d, want at most 4", rep.Chosen.Positions)
		}
	})
}

func TestSelectWidthNeverPicksSoloWhenBetterExists(t *testing.T) {
	type shape struct{ teams, fields, maxPositions, rounds int }
	shapes := []shape{
		{9, 1, 4, 6}, {9, 2, 4, 6}, {8, 1, 4, 6}, {12, 1, 4, 6},
		{7, 1, 4, 6}, {15, 1, 4, 8}, {20, 2, 4, 6}, {13, 1, 4, 6},
	}
	for _, s := range shapes {
		rep := SelectWidth(s.teams, s.fields, s.maxPositions, s.rounds, nil)
		if rep.Chosen.Positions > s.maxPositions {
			t.Errorf("%+v: chosen %d exceeds max %d", s, rep.Chosen.Positions, s.maxPositions)
		}
		if rep.Chosen.Tier == TierSoloWave {
			for _, o := range rep.Considered {
				if o.Tier < TierSoloWave && o.OpponentCeiling >= rep.Chosen.OpponentCeiling {
					t.Errorf("%+v: chose solo wave although %+v fits", s, o)
				}
			}
		}
	}
}

func TestBIBDTier(t *testing.T) {
	cases := []struct {
		teams, slots int
		want         WidthTier
	}{
		{9, 4, TierShortWave}, // 8 % 3 = 2
		{9, 3, TierExact},     // 8 % 2 = 0
		{7, 3, TierExact},     // 6 % 2 = 0
		{13, 4, TierExact},    // 12 % 3 = 0
		{9, 1, TierSoloWave},
	}
	for _, c := range cases {
		if got := BIBDTier(c.teams, c.slots); got != c.want {
			t.Errorf("BIBDTier(%d, %d) = %v, want %v", c.teams, c.slots, got, c.want)
		}
	}
}

func TestSelectWidthUsesGeneratorTier(t *testing.T) {
	// Under the BIBD rule, 9 teams at 4 slots is merely short-wave
	// (not solo), and 3 slots is exact, so the BID tier still
	// degrades 4 -> 3, but for a different stated reason.
	rep := SelectWidth(9, 1, 4, 6, BIBDTier)
	if rep.Chosen.Positions != 3 {
		t.Errorf("chosen positions = %d, want 3 (chose %+v)", rep.Chosen.Positions, rep.Chosen)
	}
	for _, o := range rep.Considered {
		if o.Positions == 4 && o.Tier != TierShortWave {
			t.Errorf("BIBDTier 9/4 = %v, want short-wave", o.Tier)
		}
	}
}

func physicalPositionsUsed(s *Schedule) map[int]bool {
	used := make(map[int]bool)
	for _, r := range s.Rounds {
		for _, m := range r.Matches {
			for loc := range m.Placements {
				used[loc.Position] = true
			}
		}
	}
	return used
}

func TestGenerateScheduleDegradesAnnealedWidth(t *testing.T) {
	// 9 teams on one field with 4 physical positions over enough
	// rounds that both widths keep the same opponent ceiling: the
	// resolver must narrow the match width to 3, and the resulting
	// schedule must play 3-seat matches that still rotate across all
	// four physical positions.
	s, err := GenerateSchedule("Annealed", Config{Teams: 9, Fields: 1, Positions: 4, Rounds: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !s.WidthDegraded {
		t.Errorf("expected a degraded schedule, active %d reason %q", s.ActivePositions, s.WidthReason)
	}
	if s.ActivePositions != 3 {
		t.Errorf("ActivePositions = %d, want 3", s.ActivePositions)
	}
	if s.WidthReason == "" {
		t.Error("expected a degradation reason")
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, r := range s.Rounds {
		for _, m := range r.Matches {
			if len(m.Placements) > 3 {
				t.Errorf("match carries %d placements, want at most 3", len(m.Placements))
			}
		}
	}
	if used := physicalPositionsUsed(s); len(used) != 4 {
		t.Errorf("degraded schedule uses positions %v, want all four", used)
	}
}

func TestGenerateScheduleCeilingGuardBlocksDegradation(t *testing.T) {
	// Three rounds at 3 positions can only offer 6 distinct
	// opponents against 4 positions' 8, so the width stays physical:
	// degradation never trades away matchups to fix a wave shape.
	s, err := GenerateSchedule("Annealed", Config{Teams: 9, Fields: 1, Positions: 4, Rounds: 3})
	if err != nil {
		t.Fatal(err)
	}
	if s.WidthDegraded || s.ActivePositions != 4 {
		t.Errorf("expected full width under the ceiling guard, active %d degraded %v",
			s.ActivePositions, s.WidthDegraded)
	}
}

func TestGenerateScheduleKeepsWidthWhenFit(t *testing.T) {
	s, err := GenerateSchedule("Annealed", Config{Teams: 12, Fields: 1, Positions: 4, Rounds: 3})
	if err != nil {
		t.Fatal(err)
	}
	if s.WidthDegraded || s.ActivePositions != 4 {
		t.Errorf("expected full width, active %d degraded %v", s.ActivePositions, s.WidthDegraded)
	}
}

func TestGenerateSchedulePinnedWidthWins(t *testing.T) {
	// 9 teams would degrade to 3, but a pinned width is the operator
	// deciding for themselves and must survive untouched.
	s, err := GenerateSchedule("Annealed", Config{Teams: 9, Fields: 1, Positions: 4, Rounds: 3, ActivePositions: 4})
	if err != nil {
		t.Fatal(err)
	}
	if s.WidthDegraded {
		t.Errorf("pinned width must not report degradation: %+v", s.WidthReason)
	}
	if s.ActivePositions != 4 {
		t.Errorf("ActivePositions = %d, want pinned 4", s.ActivePositions)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateScheduleNonDegradingGenerators(t *testing.T) {
	c := Config{Teams: 9, Fields: 1, Positions: 4, Rounds: 1}
	for _, name := range []string{"RandomSeeding", "OneShot"} {
		s, err := GenerateSchedule(name, c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s.WidthDegraded {
			t.Errorf("%s: must not degrade width", name)
		}
		if s.ActivePositions != 4 {
			t.Errorf("%s: ActivePositions = %d, want 4", name, s.ActivePositions)
		}
	}
}

func TestGenerateScheduleDegradesBID(t *testing.T) {
	// BIBD at 9 teams and 4 slots is not a clean lambda=1 design
	// (8 % 3 = 2), while 3 slots is, so the BID width degrades.
	s, err := GenerateSchedule("BID", Config{Teams: 9, Fields: 1, Positions: 4, Rounds: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !s.WidthDegraded || s.ActivePositions != 3 {
		t.Errorf("expected BID to degrade to 3, active %d degraded %v",
			s.ActivePositions, s.WidthDegraded)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	seen := make(map[int]bool)
	for _, r := range s.Rounds {
		for _, m := range r.Matches {
			for _, team := range m.Placements {
				seen[team] = true
			}
		}
	}
	for i := range 9 {
		if !seen[i] {
			t.Errorf("team %d missing from degraded BID schedule", i)
		}
	}
}

func TestPhysicalPositionRotation(t *testing.T) {
	// Width 3 on 4 physical positions must cycle seats across waves
	// and never repeat within a wave.
	for wave := range 6 {
		seen := make(map[int]bool)
		for p := range 3 {
			phys := physicalPosition(p, wave, 3, 4)
			if seen[phys] {
				t.Fatalf("wave %d: position %d collides at %d", wave, p, phys)
			}
			seen[phys] = true
		}
	}
	// Full width maps identically.
	for wave := range 4 {
		for p := range 4 {
			if got := physicalPosition(p, wave, 4, 4); got != p {
				t.Errorf("full width: physicalPosition(%d, %d) = %d, want %d", p, wave, got, p)
			}
		}
	}
}

func TestDivisionScheduleMixedWidths(t *testing.T) {
	// Division 0 has 9 teams (degrades to width 3), division 1 has 8
	// (stays at 4). Interleaving must keep every placement and never
	// double-book a (field, position) seat.
	divisions := []DivisionConfig{
		{ID: 1, Fields: []int{0}, TeamIndices: []int{0, 1, 2, 3, 4, 5, 6, 7, 8}},
		{ID: 2, Fields: []int{1}, TeamIndices: []int{9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}},
	}
	sched, err := GenerateDivisionSchedule("Annealed", Config{
		Fields:    2,
		Positions: 4,
		Teams:     21,
		Rounds:    4,
	}, divisions)
	if err != nil {
		t.Fatal(err)
	}
	if !sched.WidthDegraded {
		t.Error("expected the merged schedule to report degradation")
	}
	for ri, round := range sched.Rounds {
		placed := 0
		for _, m := range round.Matches {
			seen := make(map[int]bool)
			for _, team := range m.Placements {
				if seen[team] {
					t.Errorf("round %d: team %d twice in one match", ri, team)
				}
				seen[team] = true
				placed++
			}
		}
		if placed != 21 {
			t.Errorf("round %d places %d teams, want all 21", ri, placed)
		}
	}
}

func TestBakedWidthMismatchSkipped(t *testing.T) {
	// A design baked at full width cannot answer a degraded request
	// and vice versa; LookupBaked must skip mismatched widths rather
	// than silently undo the width decision.
	full := &BakedSchedule{
		Teams: 9, Fields: 1, Positions: 4, Rounds: 3,
		Grid: [][]int{
			{0, 1, 2, 3, 4, 5, 6, 7, 8, -1, -1, -1},
			{4, 5, 6, 7, 8, 0, 1, 2, 3, -1, -1, -1},
			{8, 7, 6, 5, 4, 3, 2, 1, 0, -1, -1, -1},
		},
	}
	if err := full.Validate(); err != nil {
		t.Fatal(err)
	}
	degradedReq := Config{Teams: 9, Fields: 1, Positions: 4, Rounds: 3, ActivePositions: 3}
	if full.activeWidth() == degradedReq.ActiveWidth() {
		t.Fatal("test setup: widths unexpectedly equal")
	}
	if _, err := full.Materialize(degradedReq, 1); err == nil {
		t.Error("expected materialization at a mismatched active width to fail")
	}

	// The same design serves a full-width request.
	fullReq := Config{Teams: 9, Fields: 1, Positions: 4, Rounds: 3}
	if _, err := full.Materialize(fullReq, 1); err != nil {
		t.Errorf("full-width request rejected: %v", err)
	}
}

func TestBakeMaterializeRoundTripDegraded(t *testing.T) {
	c := Config{Teams: 9, Fields: 1, Positions: 4, Rounds: 3, ActivePositions: 3}
	baked, err := BakeSchedule(c, AnnealOptions{Iterations: 20_000, Seed: 17})
	if err != nil {
		t.Fatal(err)
	}
	if baked.ActivePositions != 3 {
		t.Errorf("artifact ActivePositions = %d, want 3", baked.ActivePositions)
	}
	data, err := baked.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := ParseBaked(data)
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, data)
	}
	if reparsed.ActivePositions != 3 {
		t.Errorf("reparsed ActivePositions = %d, want 3", reparsed.ActivePositions)
	}
	s, err := reparsed.Materialize(c, 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if used := physicalPositionsUsed(s); len(used) != 4 {
		t.Errorf("materialized schedule uses positions %v, want all four", used)
	}
}

func TestLegacyBakedArtifactsDefaultToFullWidth(t *testing.T) {
	// Artifacts without active_positions (all checked-in ones) play
	// the full physical width.
	b := &BakedSchedule{
		Teams: 4, Fields: 1, Positions: 2, Rounds: 1,
		Grid: [][]int{{0, 1, 2, 3}},
	}
	if b.activeWidth() != 2 {
		t.Errorf("activeWidth() = %d, want 2", b.activeWidth())
	}
	b.ActivePositions = 3
	if err := b.Validate(); err == nil {
		t.Error("active_positions above the physical count must be rejected")
	}
}
