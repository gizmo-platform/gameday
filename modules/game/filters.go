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
// filter adds a team to the list.  Roster is always the full enabled
// roster, so len(Roster) in a slicing expression means the same thing
// as it does in a When or Suppress expression.  Remaining is the
// roster with any teams blocked by an earlier exclude filter removed,
// and the Scoreboard is filtered the same way, so a filter's cutoff
// applies to the teams that remain.  Scoreboard is empty for
// roster-sourced filters (SelectFrom of 0), which must scope their
// selection from Pool.
type AdvancementFilterContext struct {
	Roster     map[uint]team.Team
	Remaining  map[uint]team.Team
	Candidates map[uint]team.Team
	Scoreboard []RankingRow

	Determinations []AdvancementDeterminationResult
}

// Pool returns the teams a filter may select from: Remaining when the
// driver supplied it, otherwise the full Roster.
func (sctx *AdvancementFilterContext) Pool() map[uint]team.Team {
	if sctx.Remaining != nil {
		return sctx.Remaining
	}
	return sctx.Roster
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
