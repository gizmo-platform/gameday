package schedgen

import (
	"fmt"
	"log/slog"
)

var (
	generators = make(map[string]GeneratorFactory)
	configs    = make(map[string]GeneratorConfig)
)

// Generator is implemented by schedule generators which produce a
// schedule from inputs.
type Generator interface {
	Generate() *Schedule
	Score() int
	Validate() error
}

// GeneratorConfig is used to return the limits for a scheduler to be
// templated as default items into other parts of the system.
type GeneratorConfig interface {
	MaxRounds() int
	DefaultRounds() int
}

// GeneratorFactory produces a generator from a given config.
type GeneratorFactory func(Config) Generator

// RegisterGenerator is called by implementations to add their
// capabilities to the list of generators that are available.
func RegisterGenerator(name string, g GeneratorFactory) {
	if _, exists := generators[name]; exists {
		slog.Warn("Attempted to register duplicated generator", "generator", name)
		return
	}
	generators[name] = g
}

// RegisterGeneratorConfig is called by an implementation to register
// its configuration provider.
func RegisterGeneratorConfig(name string, g GeneratorConfig) {
	if _, exists := configs[name]; exists {
		slog.Warn("Attempted to register duplicated config", "generator", name)
		return
	}
	configs[name] = g
}

// GenerateSchedule is used to wrap all the logic of schedule
// implementations and allow for consumers to just pass a generator
// name and a config and get back a schedule.
func GenerateSchedule(g string, c Config) (*Schedule, error) {
	gf, exists := generators[g]
	if !exists {
		return nil, &UnknownGenerator{msg: "generator does not exist (" + g + ")"}
	}
	gen := gf(c)
	return gen.Generate(), nil
}

// GetConfig returns the configuration implementation for a given
// scheduler if it is registered.
func GetConfig(g string) (GeneratorConfig, error) {
	gf, exists := configs[g]
	if !exists {
		return nil, &UnknownGenerator{msg: "generator does not exist (" + g + ")"}
	}
	return gf, nil
}

// UnknownGenerator is returned when a generator is requested that has
// an unknown name.
type UnknownGenerator struct{ msg string }

func (u UnknownGenerator) Error() string { return u.msg }

// InvariantViolation is returned in the case that an internal
// invariant is not maintained.
type InvariantViolation struct{ msg string }

func (i InvariantViolation) Error() string { return i.msg }

// Config sets up the schedule that should be generated.
type Config struct {
	Fields    int
	Positions int
	Teams     int
	Rounds    int
}

// Schedule binds all the base types used by different schedules.
type Schedule struct {
	Config Config
	Rounds []Round

	TeamBestScore  int
	TeamWorstScore int
	TeamAvgScore   int

	ClosestReplay      int
	ClosestReplayMatch int
	ClosestReplayRound int

	WorstLocationDiversity   int
	WorstCompetitorDiversity int
}

// Location represents a Field and Position on that field as a unique
// location where a scheduled match happens.
type Location struct {
	Field    int
	Position int
}

func (l Location) String() string {
	return fmt.Sprintf("F%d-P%d", l.Field+1, l.Position+1)
}

// Round keeps track of the matches in a given round.
type Round struct {
	Matches         []Match
	TeamAppearances map[int]int
}

// GetRelativeMatchForTeam returns the match, counting from zero, that
// a team appears in during a round.
func (r Round) GetRelativeMatchForTeam(t int) int {
	return r.TeamAppearances[t]
}

// PositionsPlayedByTeam is used in working out whether or not a round
// improves position diversity.
func (r Round) PositionsPlayedByTeam() map[int]Location {
	out := make(map[int]Location)
	for _, m := range r.Matches {
		for loc, team := range m.Placements {
			out[team] = loc
		}
	}
	return out
}

// Match contains a single match.
type Match struct {
	Placements map[Location]int
}

func (m Match) Team(field, position int) int {
	t, ok := m.Placements[Location{field, position}]
	if !ok {
		return -1
	}
	return t
}

func (m Match) PeerTeams(field, position, maxPositions int) []int {
	ret := []int{}
	for pos := range maxPositions {
		if pos+1 == position {
			continue
		}
		ret = append(ret, m.Team(field, pos))
	}

	return ret
}
