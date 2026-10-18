package game

import (
	"context"
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

// AdvancementFilterReadiness is an optional interface an
// AdvancementFilter may implement to report whether the data it
// selects from is present and usable right now.  It answers "can I
// produce a correct answer", never "will I select anyone": a filter
// whose data is loaded but which matches no team is ready, and a phase
// that would advance zero teams is a Suppress concern, not a readiness
// one.
//
// CanRun receives the same context Apply will be handed, built by the
// same code path, so a filter can never be told it is ready against a
// view it will not be applied to.  The returned reason explains, in
// operator terms, why the filter cannot run, and is shown verbatim as
// the cue on the Generate Schedule button.  An error from CanRun is
// treated as not-ready by the driver with the error message as the
// reason, so a broken data source never reports itself as ready.
//
// A filter that does not implement this interface is always ready.
// The readiness context is built with an empty blocklist and carries
// no Division, exactly as Apply does for a roster-sourced filter, so
// readiness answers a data-availability question rather than one about
// which teams have been pruned.
type AdvancementFilterReadiness interface {
	CanRun(context.Context, *AdvancementFilterContext) (bool, string, error)
}

// filterCanRun reports whether a registered filter can run right now.
// A filter that does not implement AdvancementFilterReadiness is
// always ready.  A CanRun error is reported as not-ready with the
// error message as the reason, and the caller decides whether to
// degrade or fail.
func filterCanRun(ctx context.Context, f AdvancementFilter, sctx *AdvancementFilterContext) (bool, string, error) {
	r, ok := f.(AdvancementFilterReadiness)
	if !ok {
		return true, "", nil
	}
	return r.CanRun(ctx, sctx)
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
