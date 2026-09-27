package game

import (
	"fmt"

	"github.com/expr-lang/expr"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/pkg/schedgen"
)

// LintConfig validates a game setup config without touching the
// database.  It mirrors the checks that would otherwise surface as
// runtime errors during scheduling and advancement: every expr
// expression must compile, every referenced advancement filter must
// be registered, every referenced source phase must exist, and every
// referenced schedule generator and tie-breaker must be registered.
// A nil or empty slice means the config is valid.
func LintConfig(c Config) []error {
	var problems []error

	report := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf(format, args...))
	}

	compileExpr := func(label, e string) {
		if e == "" {
			return
		}
		if _, err := expr.Compile(e); err != nil {
			report("%s: expression does not compile: %s: %v", label, e, err)
		}
	}

	phaseByID := make(map[uint]GamePhase, len(c.Game.Phases))
	seenPhases := make(map[uint]bool, len(c.Game.Phases))
	for _, phase := range c.Game.Phases {
		if seenPhases[phase.ID] {
			report("phase ID %d is used more than once", phase.ID)
		}
		seenPhases[phase.ID] = true
		phaseByID[phase.ID] = phase
	}

	for _, phase := range c.Game.Phases {
		label := fmt.Sprintf("phase %q (ID %d)", phase.Name, phase.ID)

		if phase.ScheduleType != "" {
			if _, err := schedgen.GetConfig(phase.ScheduleType); err != nil {
				report("%s: unknown ScheduleType %q", label, phase.ScheduleType)
			}
		}

		if phase.TieBreaker != "" {
			if _, ok := modules.GetTieBreaker(phase.TieBreaker); !ok {
				report("%s: unknown TieBreaker %q", label, phase.TieBreaker)
			}
		}

		switch phase.ScoreSummation {
		case "", "Total", "AverageWithMulligan":
		default:
			report("%s: unknown ScoreSummation %q", label, phase.ScoreSummation)
		}

		compileExpr(label+" Suppress", phase.Suppress)

		for _, filter := range phase.AdvancementFilters {
			flabel := fmt.Sprintf("%s filter %q (rule %q)", label, filter.Filter, filter.Rule)

			if _, ok := filters[filter.Filter]; !ok {
				report("%s: advancement filter %q is not registered", label, filter.Filter)
			}

			if filter.SelectFrom != 0 {
				if _, ok := phaseByID[filter.SelectFrom]; !ok {
					report("%s: SelectFrom phase ID %d does not exist", flabel, filter.SelectFrom)
				}
			}

			compileExpr(flabel+" SliceExpr", filter.SliceExpr)
			compileExpr(flabel+" When", filter.When)
		}
	}

	return problems
}
