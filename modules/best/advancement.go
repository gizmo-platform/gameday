package best

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"sort"

	"github.com/expr-lang/expr"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules/game"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
)

const AdvancementFilterBESTNotebook = "BESTNotebook"

// tieBreakKeys lists the score type keys used to break notebook score
// ties, in priority order.  The team number is the last-resort tie
// breaker.
var tieBreakKeys = []string{"poster", "marketing", "video"}

// notebookRow is a single team's notebook score, ranked against the
// other scoped teams.
type notebookRow struct {
	Team   team.Team
	TeamID uint
	Value  float32
	Tie    []float32
	Rank   int
}

// NotebookAdvancement is an advancement filter owned by the BEST
// module that ranks teams by their notebook score.  Tied notebook
// scores are broken by the poster, marketing, and video scores in
// that order, with the team number as a last resort.  Teams without a
// recorded notebook score are rejected.
type NotebookAdvancement struct {
	db *db.DB
}

func (n *NotebookAdvancement) Name() string { return AdvancementFilterBESTNotebook }

// notebookRankings queries the notebook score values for the given
// team set and returns them sorted by score descending, with ties
// broken by the poster, marketing, and video scores (in that order,
// higher first), then by team number.  Teams tied on all tie breaker
// values share a rank.
func (n *NotebookAdvancement) notebookRankings(ctx context.Context, scope map[uint]team.Team) ([]notebookRow, error) {
	keys := append([]string{"notebook"}, tieBreakKeys...)
	types, err := n.scoreTypes(ctx, keys)
	if err != nil {
		return nil, err
	}

	nbIdx := 0
	for i, key := range keys {
		if key == "notebook" {
			nbIdx = i
			break
		}
	}

	typeIDs := make([]uint, 0, len(types))
	for _, st := range types {
		typeIDs = append(typeIDs, st.ID)
	}
	values, err := gorm.G[TeamScoreValue](n.db.DB).
		Where("score_type_id IN ?", typeIDs).
		Find(ctx)
	if err != nil {
		slog.Error("Error selecting notebook score values", "error", err)
		return nil, err
	}

	typeIdx := make(map[uint]int, len(types))
	for i, st := range types {
		typeIdx[st.ID] = i
	}

	// Buffer every value keyed by team and score type index so the
	// notebook row does not need to arrive before the tie breaker
	// values.
	buffer := make(map[uint]map[int]float32)
	for _, value := range values {
		if _, ok := scope[value.TeamID]; !ok {
			continue
		}
		idx, ok := typeIdx[value.ScoreTypeID]
		if !ok {
			continue
		}
		if buffer[value.TeamID] == nil {
			buffer[value.TeamID] = map[int]float32{}
		}
		buffer[value.TeamID][idx] = value.Value
	}

	out := []notebookRow{}
	for teamID, byType := range buffer {
		// A team is ranked on its notebook score only.  Teams with
		// tie breaker values but no notebook score are left out.
		nb, ok := byType[nbIdx]
		if !ok {
			continue
		}
		row := notebookRow{
			Team:   scope[teamID],
			TeamID: teamID,
			Value:  nb,
			Tie:    make([]float32, len(tieBreakKeys)),
		}
		for i := range tieBreakKeys {
			row.Tie[i] = byType[nbIdx+1+i]
		}
		out = append(out, row)
	}

	sort.Slice(out, func(i, j int) bool {
		return notebookRowLess(out[i], out[j])
	})

	rank := 1
	for i := range out {
		if i > 0 && !notebookRowEqual(out[i-1], out[i]) {
			rank++
		}
		out[i].Rank = rank
	}

	return out, nil
}

// scoreTypes looks up the score types for the given keys, preserving
// order.  Missing types are treated as an error because the ranking
// cannot evaluate without them.
func (n *NotebookAdvancement) scoreTypes(ctx context.Context, keys []string) ([]ScoreType, error) {
	types := make([]ScoreType, 0, len(keys))
	for _, key := range keys {
		st, err := gorm.G[ScoreType](n.db.DB).Where(&ScoreType{Key: key}).First(ctx)
		if err != nil {
			slog.Error("Error looking up score type", "key", key, "error", err)
			return nil, err
		}
		types = append(types, st)
	}
	return types, nil
}

// notebookRowLess reports whether a ranks strictly above b: by
// notebook score, then by each tie breaker score (higher first), then
// by team number.
func notebookRowLess(a, b notebookRow) bool {
	if a.Value != b.Value {
		return a.Value > b.Value
	}
	for i := range tieBreakKeys {
		if a.Tie[i] != b.Tie[i] {
			return a.Tie[i] > b.Tie[i]
		}
	}
	return a.Team.Number < b.Team.Number
}

// notebookRowEqual reports whether a and b are tied for the same rank.
// The team number is a last-resort ordering device only, so two rows
// still share a rank when their scores and every tie breaker value
// match.
func notebookRowEqual(a, b notebookRow) bool {
	if a.Value != b.Value {
		return false
	}
	for i := range tieBreakKeys {
		if a.Tie[i] != b.Tie[i] {
			return false
		}
	}
	return true
}

func (n *NotebookAdvancement) Apply(sctx *game.AdvancementFilterContext, rule string, mode game.GamePhaseAdvancementFilterMode, sExpr string) error {
	e, err := expr.Compile(sExpr)
	if err != nil {
		slog.Error("Could not compile slicing expression", "expr", sExpr, "error", err)
		return err
	}

	out, err := expr.Run(e, sctx)
	if err != nil {
		slog.Error("Error executing slicing expression", "expr", sExpr, "error", err)
		return err
	}
	cutoff, ok := out.(int)
	if !ok {
		slog.Error("Slicing expression did not produce an integer cutoff", "expr", sExpr, "result", out)
		return fmt.Errorf("slicing expression %q did not produce an integer cutoff", sExpr)
	}
	slog.Debug("Obtained slicing constraint", "constraint", cutoff)

	// Scope the ranking to the teams on the division scoreboard.
	// When the scoreboard is empty the roster is used instead so the
	// filter still evaluates to a definitive answer.
	scope := make(map[uint]team.Team, len(sctx.Scoreboard))
	for _, row := range sctx.Scoreboard {
		scope[row.Team.ID] = row.Team
	}
	if len(scope) == 0 {
		scope = make(map[uint]team.Team, len(sctx.Roster))
		maps.Copy(scope, sctx.Roster)
	}

	rows, err := n.notebookRankings(context.Background(), scope)
	if err != nil {
		return err
	}

	ranked := make(map[uint]notebookRow, len(rows))
	for _, row := range rows {
		ranked[row.TeamID] = row
	}

	for teamID, t := range scope {
		row, ok := ranked[teamID]
		switch mode {
		case game.GamePhaseAdvancementFilterModeInclude:
			if !ok {
				sctx.Determinations = append(sctx.Determinations, game.AdvancementDeterminationResult{
					Filter: n.Name(),
					Rule:   rule,
					Team:   t,
					Result: game.AdvancementDeterminationReject,
					Reason: "No notebook score recorded",
				})
				continue
			}
			if row.Rank > cutoff {
				sctx.Determinations = append(sctx.Determinations, game.AdvancementDeterminationResult{
					Filter: n.Name(),
					Rule:   rule,
					Team:   t,
					Result: game.AdvancementDeterminationReject,
					Reason: fmt.Sprintf("Notebook rank outside of cutoff (%d > %d)", row.Rank, cutoff),
				})
				continue
			}
			sctx.Candidates[teamID] = t
			sctx.Determinations = append(sctx.Determinations, game.AdvancementDeterminationResult{
				Filter: n.Name(),
				Rule:   rule,
				Team:   t,
				Result: game.AdvancementDeterminationAccept,
				Reason: fmt.Sprintf("Notebook rank within cutoff (%d <= %d)", row.Rank, cutoff),
			})
		case game.GamePhaseAdvancementFilterModeExclude:
			if !ok {
				sctx.Determinations = append(sctx.Determinations, game.AdvancementDeterminationResult{
					Filter: n.Name(),
					Rule:   rule,
					Team:   t,
					Result: game.AdvancementDeterminationReject,
					Reason: "No notebook score recorded",
				})
				continue
			}
			if row.Rank > cutoff {
				continue
			}
			delete(sctx.Candidates, teamID)
			sctx.Determinations = append(sctx.Determinations, game.AdvancementDeterminationResult{
				Filter: n.Name(),
				Rule:   rule,
				Team:   t,
				Result: game.AdvancementDeterminationReject,
				Reason: fmt.Sprintf("Notebook rank within cutoff (%d <= %d)", row.Rank, cutoff),
			})
		}
	}

	return nil
}
