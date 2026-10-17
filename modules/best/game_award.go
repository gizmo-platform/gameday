package best

import (
	"context"
	"sort"

	"github.com/gizmo-platform/gameday/modules/game"
	"github.com/gizmo-platform/gameday/modules/team"
)

// gameAwardPlaces is how many places the Game Award awards: the final
// phase decides 1st through 4th place, so all four are shown inline.
const gameAwardPlaces = 4

// finalPhaseRanker ranks the Game Award from the results of the phase
// carrying the game module's Final role.  Places are the phase's own
// ranks, decided by its configured score summation and tie-breaker,
// so the finals are never re-ranked with the BEST tie-break chain.
// Results are only official once the phase is frozen: an unfrozen
// final yields no places.  A division aware final yields one group per
// division that has ranked teams, following the phase's own division
// split rather than the contest-wide awareness the score awards use.
type finalPhaseRanker struct {
	gm GameModule
}

// Rank implements Ranker.  Should more than one phase carry the role,
// the first ranking of a team (phases are returned in ID order) wins,
// so a team never takes two places in the award.
func (r finalPhaseRanker) Rank(ctx context.Context, teams []team.Team) ([]RankedGroup, error) {
	if r.gm == nil {
		return nil, nil
	}

	tables, err := r.gm.PhaseRankings(ctx, game.RoleFinal, true)
	if err != nil {
		return nil, err
	}

	inScope := make(map[uint]struct{}, len(teams))
	for _, t := range teams {
		inScope[t.ID] = struct{}{}
	}

	byLabel := map[string][]rankedTeam{}
	seen := map[uint]struct{}{}
	for _, table := range tables {
		label := table.Division
		if label == "" {
			label = overallGroupLabel
		}
		for _, row := range table.Rows {
			if _, dup := seen[row.TeamID]; dup {
				continue
			}
			if _, ok := inScope[row.TeamID]; !ok {
				continue
			}
			seen[row.TeamID] = struct{}{}
			byLabel[label] = append(byLabel[label], rankedTeam{
				Team:  row.Team,
				Score: float64(row.Score),
				Rank:  row.Rank,
			})
		}
	}

	labels := make([]string, 0, len(byLabel))
	for label := range byLabel {
		labels = append(labels, label)
	}
	// The single ranking of a phase that is not division aware reads
	// as the headline result, so it leads whatever divisions follow.
	sort.Slice(labels, func(i, j int) bool {
		if (labels[i] == overallGroupLabel) != (labels[j] == overallGroupLabel) {
			return labels[i] == overallGroupLabel
		}
		return labels[i] < labels[j]
	})

	out := make([]RankedGroup, 0, len(labels))
	for _, label := range labels {
		rows := byLabel[label]
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Rank != rows[j].Rank {
				return rows[i].Rank < rows[j].Rank
			}
			return rows[i].Team.Number < rows[j].Team.Number
		})
		out = append(out, RankedGroup{Label: label, Teams: rows})
	}
	return out, nil
}
