package game

import (
	"log/slog"
)

func init() {
	f := new(RosterAdvancement)
	RegisterAdvancementFilter(f.Name(), f)
}

// RosterAdvancement advances every team in the roster.  It is the
// start-of-schedule filter: a phase that uses it with a SelectFrom of
// 0 automatically includes the full roster without relying on a
// scoreboard or any recorded score.  In exclude mode it removes every
// roster team from the candidates.
type RosterAdvancement struct{}

func (r *RosterAdvancement) Name() string { return "Roster" }

func (r *RosterAdvancement) Apply(sctx *AdvancementFilterContext, rule string, mode GamePhaseAdvancementFilterMode, sExpr string) error {
	for _, t := range sctx.Roster {
		switch mode {
		case GamePhaseAdvancementFilterModeInclude:
			sctx.Candidates[t.ID] = t
			sctx.Determinations = append(sctx.Determinations, AdvancementDeterminationResult{
				Filter: r.Name(),
				Rule:   rule,
				Team:   t,
				Result: AdvancementDeterminationAccept,
				Reason: "Team is part of the full roster",
			})
		case GamePhaseAdvancementFilterModeExclude:
			delete(sctx.Candidates, t.ID)
			sctx.Determinations = append(sctx.Determinations, AdvancementDeterminationResult{
				Filter: r.Name(),
				Rule:   rule,
				Team:   t,
				Result: AdvancementDeterminationReject,
				Reason: "Team is part of the full roster",
			})
		default:
			slog.Error("Unknown advancement filter mode", "mode", mode)
			return nil
		}
	}

	return nil
}
