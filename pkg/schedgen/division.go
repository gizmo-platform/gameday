package schedgen

import "fmt"

// divResult holds a per-division sub-config and its generated schedule.
type divResult struct {
	config   DivisionConfig
	schedule *Schedule
}

// GenerateDivisionSchedule generates independent schedules per division
// and interleaves them round-by-round. Divisions are pinned to specific
// fields so teams stay on the same fields across matches.
func GenerateDivisionSchedule(generator string, cfg Config, divisions []DivisionConfig) (*Schedule, error) {
	if len(divisions) == 0 {
		return nil, fmt.Errorf("no divisions configured")
	}

	// Auto-assign fields to divisions that don't have explicit pinning.
	fieldMap := assignFieldsToDivisions(divisions, cfg.Fields, cfg.Positions)

	// Generate a schedule for each division independently.
	results := make([]divResult, 0, len(divisions))

	for _, div := range divisions {
		fields := fieldMap[div.ID]
		rounds := div.Rounds
		if rounds == 0 {
			rounds = cfg.Rounds
		}

		subCfg := Config{
			Fields:    len(fields),
			Positions: cfg.Positions,
			Teams:     len(div.TeamIndices),
			Rounds:    rounds,
		}

		sched, err := GenerateSchedule(generator, subCfg)
		if err != nil {
			return nil, fmt.Errorf("division %d: %w", div.ID, err)
		}
		if err := sched.Validate(); err != nil {
			return nil, fmt.Errorf("division %d validation: %w", div.ID, err)
		}

		results = append(results, divResult{config: div, schedule: sched})
	}

	// Interleave all division schedules round-by-round.
	return interleave(results, fieldMap, cfg), nil
}

// assignFieldsToDivisions assigns fields to divisions that don't have
// explicit pinning. Divisions with explicit Fields keep those; the rest
// get a round-robin assignment of the remaining fields.
func assignFieldsToDivisions(divisions []DivisionConfig, totalFields int, positions int) map[int][]int {
	used := make(map[int]struct{})
	result := make(map[int][]int)

	// First pass: honor explicit pinning.
	for _, div := range divisions {
		if len(div.Fields) > 0 {
			result[div.ID] = make([]int, len(div.Fields))
			copy(result[div.ID], div.Fields)
			for _, f := range div.Fields {
				used[f] = struct{}{}
			}
		}
	}

	// Collect unused fields.
	unused := make([]int, 0, totalFields)
	for f := 0; f < totalFields; f++ {
		if _, ok := used[f]; !ok {
			unused = append(unused, f)
		}
	}

	// Pre-calculate how many fields each auto-assigned division needs, so
	// we can distribute evenly across all divisions.
	type autoDiv struct {
		idx      int
		need     int
	}
	autoDivs := make([]autoDiv, 0)
	for i, div := range divisions {
		if len(div.Fields) > 0 {
			continue
		}
		teamCount := len(div.TeamIndices)
		need := (teamCount + positions - 1) / positions
		if need < 1 {
			need = 1
		}
		autoDivs = append(autoDivs, autoDiv{idx: i, need: need})
	}

	// Assign any remaining unused fields round-robin across divisions
	// that still need them.
	for len(unused) > 0 {
		distributed := false
		for _, ad := range autoDivs {
			if len(result[divisions[ad.idx].ID]) >= ad.need {
				continue // Already has enough
			}
			if len(unused) == 0 {
				break
			}
			result[divisions[ad.idx].ID] = append(result[divisions[ad.idx].ID], unused[0])
			unused = unused[1:]
			distributed = true
		}
		if !distributed {
			break
		}
	}

	// When fields are scarce (eg fewer fields than divisions), auto-assigned
	// divisions that received nothing get all fields. Divisions share fields
	// but their matches stay separate — the interleave step handles the
	// time-sharing.
	for _, ad := range autoDivs {
		divID := divisions[ad.idx].ID
		if len(result[divID]) > 0 {
			continue
		}
		allFields := make([]int, totalFields)
		for f := 0; f < totalFields; f++ {
			allFields[f] = f
		}
		result[divID] = allFields
	}

	return result
}



// interleave merges division schedules round-by-round. Local field indices
// are remapped to global pinned field indices, and each match is tagged
// with its DivisionID. Matches are greedily compacted when they use
// non-overlapping fields, unless NoCompact is set.
func interleave(results []divResult, fieldMap map[int][]int, globalCfg Config) *Schedule {
	// Find max rounds across all divisions.
	maxRounds := 0
	for _, r := range results {
		if len(r.schedule.Rounds) > maxRounds {
			maxRounds = len(r.schedule.Rounds)
		}
	}

	// Total teams across all divisions.
	totalTeams := 0
	for _, r := range results {
		totalTeams += len(r.config.TeamIndices)
	}

	interleaved := &Schedule{
		Config: Config{
			Fields:    globalCfg.Fields,
			Positions: globalCfg.Positions,
			Teams:     totalTeams,
			Rounds:    maxRounds,
		},
		Rounds:        make([]Round, maxRounds),
		RoundsDynamic: false,
		Interleaved:   true,
	}

	// Build a lookup for NoCompact flags.
	noCompact := make(map[int]bool)
	for _, r := range results {
		if r.config.NoCompact {
			noCompact[r.config.ID] = true
		}
	}

	for round := 0; round < maxRounds; round++ {
		teamAppearances := make(map[int]int)

		// Collect remapped matches per division for this round.
		type divMatches struct {
			matches []Match
			teamIdx []int
		}
		roundDivs := make([]divMatches, 0, len(results))

		for _, r := range results {
			if round >= len(r.schedule.Rounds) {
				continue // This division has fewer rounds; fields stay idle.
			}

			divRound := r.schedule.Rounds[round]
			fields := fieldMap[r.config.ID]
			teamIdx := r.config.TeamIndices

			divMs := divMatches{teamIdx: teamIdx}
			for _, m := range divRound.Matches {
				remapped := Match{
					Placements: make(map[Location]int),
					DivisionID: r.config.ID,
				}

				for loc, localTeam := range m.Placements {
					// Remap local field index to global pinned field index.
					if loc.Field < len(fields) {
						remapped.Placements[Location{
							Field:    fields[loc.Field],
							Position: loc.Position,
						}] = teamIdx[localTeam]
					}
				}

				divMs.matches = append(divMs.matches, remapped)
			}
			roundDivs = append(roundDivs, divMs)
		}

		// Flatten all matches into a single list for compaction.
		var allMatches []Match
		for _, div := range roundDivs {
			allMatches = append(allMatches, div.matches...)
		}

		// Greedy bin-packing: try to compact matches that don't conflict
		// on fields, respecting NoCompact constraints.
		var compacted []Match
		// slotFields tracks which fields are used in each compacted match.
		slotFields := make([]map[int]bool, 0)
		// slotDiv tracks the division of the first match placed in each slot
		// (used for NoCompact checks).
		slotDiv := make([]int, 0)

		for _, m := range allMatches {
			// Collect fields used by this match.
			fields := make(map[int]bool)
			for loc := range m.Placements {
				fields[loc.Field] = true
			}

			placed := false
			mNoCompact := noCompact[m.DivisionID]

			for si := range compacted {
				sNoCompact := noCompact[slotDiv[si]]

				// If either side is NoCompact, they must be from the
				// same division to merge.
				if (mNoCompact || sNoCompact) && m.DivisionID != slotDiv[si] {
					continue
				}

				// Check for field conflicts.
				conflict := false
				for f := range fields {
					if slotFields[si][f] {
						conflict = true
						break
					}
				}
				if conflict {
					continue
				}

				// Merge: add placements to existing match.
				for loc, team := range m.Placements {
					compacted[si].Placements[loc] = team
					slotFields[si][loc.Field] = true
				}
				placed = true
				break
			}

			if !placed {
				slotFields = append(slotFields, fields)
				slotDiv = append(slotDiv, m.DivisionID)
				compacted = append(compacted, m)
			}
		}

		// Track team appearances in the interleaved round.
		for mi, m := range compacted {
			for _, team := range m.Placements {
				teamAppearances[team] = mi
			}
		}

		interleaved.Rounds[round] = Round{
			Matches:         compacted,
			TeamAppearances: teamAppearances,
		}
	}

	return interleaved
}
