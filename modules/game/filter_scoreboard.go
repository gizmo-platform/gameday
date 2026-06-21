package game

import (
	"log/slog"
	"fmt"

	"github.com/expr-lang/expr"
)

func init() {
	f := new(ScoreboardAdvancement)
	RegisterAdvancementFilter(f.Name(), f)
}

// ScoreboardAdvancement is the simplest filter, it is used to
// manipulate the top N positions of the scoreboard, either including
// them or excluding them.
type ScoreboardAdvancement struct{}

func (s *ScoreboardAdvancement) Name() string { return "ScoreboardRanking" }

func (s *ScoreboardAdvancement) Apply(sctx *AdvancementFilterContext, rule string, mode GamePhaseAdvancementFilterMode, sExpr string) error {
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
	slog.Debug("Obtained slicing constraint", "constraint", out)

	for i := range out.(int) {
		switch mode {
		case GamePhaseAdvancementFilterModeInclude:
			sctx.Candidates[sctx.Scoreboard[i].Team.ID] = sctx.Scoreboard[i].Team
			sctx.Determinations = append(sctx.Determinations, AdvancementDeterminationResult{
				Filter: s.Name(),
				Rule:   rule,
				Team:   sctx.Scoreboard[i].Team,
				Result: AdvancementDeterminationAccept,
				Reason: fmt.Sprintf("Team rank less than cutoff (%d < %d)", i, out.(int)),
			})
		case GamePhaseAdvancementFilterModeExclude:
			delete(sctx.Candidates, sctx.Scoreboard[i].Team.ID)
			sctx.Determinations = append(sctx.Determinations, AdvancementDeterminationResult{
				Filter: s.Name(),
				Rule:   rule,
				Team:   sctx.Scoreboard[i].Team,
				Result: AdvancementDeterminationReject,
				Reason: fmt.Sprintf("Team rank less than cutoff (%d < %d)", i, out.(int)),
			})
		}
	}

	return nil
}
