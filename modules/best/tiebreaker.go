package best

import (
	"context"
	"slices"
	"sort"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/team"
	"gorm.io/gorm"
)

const TieBreakerBESTUnified = "BESTUnifiedTieBreaker"

// unifiedTieBreaker is the live tie-breaker function for the current
// module instance.  It is updated by New() on every construction so
// the registered function always queries the current database.  The
// default is a no-op that preserves input order.
var unifiedTieBreaker modules.TieBreaker = func(teamNumbers []int, _ int) []int {
	out := make([]int, len(teamNumbers))
	copy(out, teamNumbers)
	return out
}

// TieBreaker orders the given tied team numbers by notebook score
// first, then by marketing, poster, and video scores (in that order,
// each higher first), then by team number.  The match count is
// accepted for interface compatibility and ignored.
func (n *NotebookAdvancement) TieBreaker(teamNumbers []int, _ int) []int {
	ctx := context.Background()

	teams, err := gorm.G[team.Team](n.db.DB).
		Where("number IN ?", teamNumbers).
		Find(ctx)
	if err != nil {
		return slices.Clone(teamNumbers)
	}

	scope := make(map[uint]team.Team, len(teams))
	for _, t := range teams {
		scope[t.ID] = t
	}

	buffer, err := scoreBuffer(ctx, n.db, scope)
	if err != nil {
		return slices.Clone(teamNumbers)
	}

	rows := make([]notebookRow, 0, len(teamNumbers))
	for _, t := range teams {
		row := notebookRow{
			Team:   t,
			TeamID: t.ID,
			Tie:    make([]float32, len(tieBreakKeys)),
		}
		if byType, ok := buffer[t.ID]; ok {
			row.Value = byType[0]
			for i := range tieBreakKeys {
				row.Tie[i] = byType[1+i]
			}
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		return notebookRowLess(rows[i], rows[j])
	})

	out := make([]int, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Team.Number)
	}
	return out
}
