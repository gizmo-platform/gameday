package game

import (
	"log/slog"

	"github.com/gizmo-platform/gameday/modules/team"
)

type AdvancementDetermination uint8

const (
	// AdvancementDeterminationAccept is used when a team is
	// accepted by a filter for advancement.
	AdvancementDeterminationAccept AdvancementDetermination = iota + 1

	// AdvancementDeterminationReject is used to reject teams from
	// advancement.
	AdvancementDeterminationReject
)

func (ad AdvancementDetermination) String() string {
	switch ad {
	case AdvancementDeterminationAccept:
		return "Accept"
	case AdvancementDeterminationReject:
		return "Reject"
	}
	return "UNKNOWN"
}

// AdvancementFilter defines a filtering primitive that can look at a
// list of teams and produce an output list of teams that should be
// advanced.
type AdvancementFilter interface {
	Name() string
	Apply(*AdvancementFilterContext, string, GamePhaseAdvancementFilterMode, string) error
}

// AdvancementFilterContext is used to supply additional information
// to a filter that is changed during the course of running
// advancement.  This structure supplies the roster, the scoreboard,
// and any filter configuration.  The candidates map is blank until a
// filter adds a team to the list.
type AdvancementFilterContext struct {
	Roster     map[uint]team.Team
	Candidates map[uint]team.Team
	Scoreboard []scoreboardRow

	Determinations []AdvancementDeterminationResult
}

// AdvancementDeterminationResult is used to
type AdvancementDeterminationResult struct {
	Filter string
	Rule   string
	Team   team.Team
	Result AdvancementDetermination
	Reason string
}

var (
	filters = make(map[string]AdvancementFilter)
)

func RegisterAdvancementFilter(name string, f AdvancementFilter) {
	if _, exists := filters[name]; exists {
		slog.Error("Refusing to register duplicate advancement filter", "filter", name)
	}
	filters[name] = f
}
