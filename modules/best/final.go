package best

import (
	"fmt"

	"github.com/gizmo-platform/gameday/pkg/schedgen"
)

func init() {
	schedgen.RegisterGenerator("BESTFinals", NewBESTFinal)
	schedgen.RegisterGeneratorConfig("BESTFinals", BESTFinalsConfig{})
}

// Table 1.4.6.4 — Field position assignments for the finals.
// Column order is Yellow, Blue, Red, Green. Values are the 1-based
// semifinal rank; the team index bound to rank N is N-1. Only the top
// four ranked semifinal teams advance to the finals.
var finals4 = [4][4]int{
	{1, 2, 3, 4}, // F1
	{4, 3, 2, 1}, // F2
	{3, 1, 4, 2}, // F3
	{2, 4, 1, 3}, // F4
}

// BESTFinalsConfig provides information on the configuration defaults
// for the BEST finals rotation.
type BESTFinalsConfig struct{}

func (c BESTFinalsConfig) MaxRounds() int { return 4 }

func (c BESTFinalsConfig) DefaultRounds() int { return 4 }

func (c BESTFinalsConfig) RoundsDynamic() bool { return false }

func NewBESTFinal(c schedgen.Config) schedgen.Generator {
	return &BESTFinalsSchedule{
		Schedule: schedgen.Schedule{
			Config:        c,
			ClosestReplay: 99,
		},
	}
}

// BESTFinalsSchedule produces the fixed BEST finals rotation. Team
// index i corresponds to semifinal rank i+1: the table is a pure
// lookup from semifinal rank to field position, and the binding of an
// actual team (a top-4 semifinal finisher) to an index is the
// responsibility of the placement layer. The rotation plays on a
// single field: each of the four matches spans the four positions and
// every team plays every match. The winner is decided by total points
// across the four matches, which is computed by the score layer rather
// than this generator.
type BESTFinalsSchedule struct {
	schedgen.Schedule
}

// Generate looks up the fixed finals rotation and returns it as a
// single round of four single-field matches. The finals rotation
// requires exactly four teams, a single field, and at least four
// positions (Yellow, Blue, Red, Green); anything else returns an error.
func (s *BESTFinalsSchedule) Generate() (*schedgen.Schedule, error) {
	if s.Config.Teams != 4 {
		return nil, fmt.Errorf("BESTFinals: unsupported team count %d (must be 4)", s.Config.Teams)
	}
	if s.Config.Fields != 1 {
		return nil, fmt.Errorf("BESTFinals: rotation is single-field (got %d fields); all four teams play every match", s.Config.Fields)
	}
	if s.Config.Positions < 4 {
		return nil, fmt.Errorf("BESTFinals: requires 4 positions (got %d)", s.Config.Positions)
	}

	r := schedgen.Round{TeamAppearances: make(map[int]int)}
	for m, row := range finals4 {
		placements := make(map[schedgen.Location]int, 4)
		for col, rank := range row {
			teamIndex := rank - 1
			placements[schedgen.Location{Field: 0, Position: col}] = teamIndex
			if _, done := r.TeamAppearances[teamIndex]; !done {
				r.TeamAppearances[teamIndex] = m
			}
		}
		r.Matches = append(r.Matches, schedgen.Match{Placements: placements})
	}

	s.Rounds = []schedgen.Round{r}
	return &s.Schedule, nil
}
