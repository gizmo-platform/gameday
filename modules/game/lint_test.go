package game_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/gizmo-platform/gameday/modules/best"
	"github.com/gizmo-platform/gameday/modules/game"
)

const lintValidYAML = `
Field:
  Positions:
    - Name: red
      ID: 1
      Color1: ec5e5e
      Color2: f4a2a2

Game:
  Phases:
    - Name: seeding
      ID: 1
      ScoreSummation: AverageWithMulligan
      ScheduleType: RandomSeeding
    - Name: semifinal
      ID: 2
      ScoreSummation: Total
      ScheduleType: BID
      AdvancementFilters:
        - Rule: PickTop8
          Filter: ScoreboardRanking
          Mode: include
          SelectFrom: 1
          SliceExpr: 8
          When: Phases[0].Active
    - Name: finals
      ID: 3
      ScoreSummation: Total
      ScheduleType: OneShot
      TieBreaker: BESTUnifiedTieBreaker
      Suppress: Division == "Open"
      AdvancementFilters:
        - Rule: PickTop4
          Filter: BESTNotebook
          Mode: include
          SelectFrom: 2
          SliceExpr: 4
`

func init() {
	// Register the BEST module's advancement filter and tie-breaker,
	// mirroring the lint command.  The database and server are not
	// used by the lint path.
	best.New(nil, nil, nil)
}

func lintParse(t *testing.T, s string) game.Config {
	t.Helper()
	cfg := game.Config{}
	if err := yaml.Unmarshal([]byte(s), &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}
	return cfg
}

// TestLintConfigValid confirms that a well-formed config produces no
// problems.
func TestLintConfigValid(t *testing.T) {
	cfg := lintParse(t, lintValidYAML)

	problems := game.LintConfig(cfg)
	if len(problems) != 0 {
		for _, p := range problems {
			t.Errorf("unexpected problem: %v", p)
		}
	}
}

// TestLintConfigBroken confirms that every kind of broken reference is
// caught.  Each case mutates a copy of the valid config and expects
// the specific problem to be reported.
func TestLintConfigBroken(t *testing.T) {
	mutate := func(t *testing.T, f func(*game.Config)) []error {
		t.Helper()
		cfg := lintParse(t, lintValidYAML)
		f(&cfg)
		return game.LintConfig(cfg)
	}

	expect := func(t *testing.T, problems []error, want string) {
		t.Helper()
		for _, p := range problems {
			if strings.Contains(p.Error(), want) {
				return
			}
		}
		t.Errorf("expected problem containing %q, got: %v", want, problems)
	}

	t.Run("UnregisteredFilter", func(t *testing.T) {
		problems := mutate(t, func(c *game.Config) {
			c.Game.Phases[0].AdvancementFilters = append(c.Game.Phases[0].AdvancementFilters,
				game.GamePhaseAdvancementFilter{Rule: "X", Filter: "NotAFilter", Mode: game.GamePhaseAdvancementFilterModeInclude})
		})
		expect(t, problems, `advancement filter "NotAFilter" is not registered`)
	})

	t.Run("MissingSelectFromPhase", func(t *testing.T) {
		problems := mutate(t, func(c *game.Config) {
			c.Game.Phases[1].AdvancementFilters[0].SelectFrom = 42
		})
		expect(t, problems, "SelectFrom phase ID 42 does not exist")
	})

	t.Run("BrokenSuppressExpr", func(t *testing.T) {
		problems := mutate(t, func(c *game.Config) {
			c.Game.Phases[2].Suppress = "Division == "
		})
		expect(t, problems, `Suppress: expression does not compile`)
	})

	t.Run("BrokenWhenExpr", func(t *testing.T) {
		problems := mutate(t, func(c *game.Config) {
			c.Game.Phases[1].AdvancementFilters[0].When = "Phases["
		})
		expect(t, problems, `When: expression does not compile`)
	})

	t.Run("BrokenSliceExpr", func(t *testing.T) {
		problems := mutate(t, func(c *game.Config) {
			c.Game.Phases[1].AdvancementFilters[0].SliceExpr = "8 +"
		})
		expect(t, problems, `SliceExpr: expression does not compile`)
	})

	t.Run("UnknownScheduleType", func(t *testing.T) {
		problems := mutate(t, func(c *game.Config) {
			c.Game.Phases[0].ScheduleType = "NotAGenerator"
		})
		expect(t, problems, `unknown ScheduleType "NotAGenerator"`)
	})

	t.Run("UnknownTieBreaker", func(t *testing.T) {
		problems := mutate(t, func(c *game.Config) {
			c.Game.Phases[2].TieBreaker = "NotATieBreaker"
		})
		expect(t, problems, `unknown TieBreaker "NotATieBreaker"`)
	})

	t.Run("UnknownScoreSummation", func(t *testing.T) {
		problems := mutate(t, func(c *game.Config) {
			c.Game.Phases[0].ScoreSummation = "BestOf"
		})
		expect(t, problems, `unknown ScoreSummation "BestOf"`)
	})

	t.Run("DuplicatePhaseID", func(t *testing.T) {
		problems := mutate(t, func(c *game.Config) {
			c.Game.Phases = append(c.Game.Phases, c.Game.Phases[0])
		})
		expect(t, problems, "phase ID 1 is used more than once")
	})

	// Multiple problems at once must all be reported, not just the
	// first.
	problems := mutate(t, func(c *game.Config) {
		c.Game.Phases[0].ScheduleType = "NotAGenerator"
		c.Game.Phases[1].AdvancementFilters[0].Filter = "NotAFilter"
		c.Game.Phases[2].TieBreaker = "NotATieBreaker"
	})
	if len(problems) != 3 {
		t.Fatalf("expected 3 problems, got %d: %v", len(problems), problems)
	}
	if !slices.ContainsFunc(problems, func(e error) bool { return strings.Contains(e.Error(), "NotAGenerator") }) {
		t.Errorf("missing ScheduleType problem: %v", problems)
	}
	if !slices.ContainsFunc(problems, func(e error) bool { return strings.Contains(e.Error(), "NotAFilter") }) {
		t.Errorf("missing filter problem: %v", problems)
	}
	if !slices.ContainsFunc(problems, func(e error) bool { return strings.Contains(e.Error(), "NotATieBreaker") }) {
		t.Errorf("missing tie-breaker problem: %v", problems)
	}
}
