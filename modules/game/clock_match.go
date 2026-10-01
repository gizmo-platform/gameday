package game

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"

	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/pkg/db"
)

// matchTransitionStates lists the MatchState values the clock can
// drive a match from.  Scorable matches are post-run and are not
// pulled back into the cycle; terminal states (Complete, NoShow,
// Disqualified) are never touched by the clock.
var matchTransitionStates = []MatchState{
	MatchStateScheduled,
	MatchStateQueued,
	MatchStateRunning,
}

// activePhase loads the currently active game phase, or nil if there
// is none.
func (m *Module) activePhase(ctx context.Context) (*GamePhase, error) {
	phase, err := gorm.G[GamePhase](m.db.DB).
		Where("active = true").
		First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &phase, nil
}

// nonTerminalMatchNumbers returns the sorted list of match numbers in
// the given phase that still have at least one placement in a
// non-terminal state (Scheduled, Queued, Running, or Scorable).  Match
// numbers are assigned chronologically by the schedule generator, so
// the lowest number is the earliest remaining match.
func (m *Module) nonTerminalMatchNumbers(ctx context.Context, phaseID uint) ([]int, error) {
	placements, err := gorm.G[MatchPlacement](m.db.DB).
		Where(&MatchPlacement{PhaseID: phaseID}).
		Find(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[int]bool)
	out := []int{}
	for _, p := range placements {
		if isTransitionState(p.State) && !seen[p.Match] {
			seen[p.Match] = true
			out = append(out, p.Match)
		}
	}
	sort.Ints(out)
	return out, nil
}

// isTransitionState reports whether a state is one the clock is
// allowed to drive (anything not terminal).
func isTransitionState(s MatchState) bool {
	return slices.Contains(matchTransitionStates, s)
}

// computeClockTargets picks, from the sorted non-terminal match
// numbers, which match a clock run acts on (cur) and which match it
// queues up (next).  override is the operator's next-match pointer; a
// value of 0 selects the lowest non-terminal match.  next is 0 when
// there is no match after cur.
func computeClockTargets(override int, nums []int) (cur, next int) {
	if override > 0 {
		for _, n := range nums {
			if n == override {
				for _, m := range nums {
					if m > override {
						next = m
						break
					}
				}
				return override, next
			}
		}
	}
	if len(nums) == 0 {
		return 0, 0
	}
	cur = nums[0]
	if len(nums) > 1 {
		next = nums[1]
	}
	return cur, next
}

// setMatchState sets the state of every placement in the given match
// of the given phase.
func (m *Module) setMatchState(ctx context.Context, phaseID uint, match int, state MatchState) (int, error) {
	return gorm.G[MatchPlacement](m.db.DB).
		Where(&MatchPlacement{PhaseID: phaseID, Match: match}).
		Update(ctx, "state", state)
}

// setPhaseState sets the state of every placement in the given phase
// whose current state matches from.
func (m *Module) setPhaseState(ctx context.Context, phaseID uint, from, to MatchState) (int, error) {
	return gorm.G[MatchPlacement](m.db.DB).
		Where(&MatchPlacement{PhaseID: phaseID, State: from}).
		Update(ctx, "state", to)
}

// applyClockMatchStart performs the match transitions for a clock
// start: the current match goes Running, the following match goes
// Queued, and any other match left in a stale Running or Queued state
// (for example by a crashed run) is reset to Scheduled.  Scheduled
// and Scorable placements are untouched.
func (m *Module) applyClockMatchStart(ctx context.Context, phaseID uint, cur, next int) error {
	return m.db.Transaction(func(tx *gorm.DB) error {
		if _, err := gorm.G[MatchPlacement](tx).
			Where(&MatchPlacement{PhaseID: phaseID, Match: cur}).
			Update(ctx, "state", MatchStateRunning); err != nil {
			return err
		}
		if next > 0 && next != cur {
			if _, err := gorm.G[MatchPlacement](tx).
				Where(&MatchPlacement{PhaseID: phaseID, Match: next}).
				Update(ctx, "state", MatchStateQueued); err != nil {
				return err
			}
		}
		// Reset stale Running/Queued placements in every match
		// other than cur and next back to Scheduled.
		matchFilter := gorm.G[MatchPlacement](tx).
			Where(&MatchPlacement{PhaseID: phaseID}).
			Where("state in (?)", []MatchState{MatchStateRunning, MatchStateQueued}).
			Where("match not in (?)", []int{cur, next})
		if _, err := matchFilter.Update(ctx, "state", MatchStateScheduled); err != nil {
			return err
		}
		return nil
	})
}

// applyClockMatchEnd performs the match transition for a clock end:
// the given match goes Scorable.
func (m *Module) applyClockMatchEnd(ctx context.Context, phaseID uint, match int) error {
	if _, err := m.setMatchState(ctx, phaseID, match, MatchStateScorable); err != nil {
		return err
	}
	return nil
}

// applyClockMatchCancel performs the match transitions for a clock
// cancel: every Running and Queued placement in the phase goes back
// to Scheduled.  This also normalizes stale rows left behind by a
// crashed run.
func (m *Module) applyClockMatchCancel(ctx context.Context, phaseID uint) error {
	if _, err := m.setPhaseState(ctx, phaseID, MatchStateRunning, MatchStateScheduled); err != nil {
		return err
	}
	if _, err := m.setPhaseState(ctx, phaseID, MatchStateQueued, MatchStateScheduled); err != nil {
		return err
	}
	return nil
}

// clockNextMatchOverride loads the operator's next-match pointer if
// it applies to the given phase, or 0 for auto-selection.
func (m *Module) clockNextMatchOverride(ctx context.Context, cs *ClockSettings, phaseID uint) int {
	if cs != nil && cs.NextMatch > 0 && cs.NextMatchPhase == phaseID {
		return cs.NextMatch
	}
	return 0
}

// setClockNextMatch stores (or clears, when n is 0) the operator's
// next-match pointer, scoped to the given phase.
func (m *Module) setClockNextMatch(ctx context.Context, phaseID uint, n int) error {
	cs, err := m.clockSettings(ctx)
	if err != nil {
		if errors.Is(err, errClockNotConfigured) {
			return errClockNotConfigured
		}
		return err
	}
	if n > 0 {
		cs.NextMatch = n
		cs.NextMatchPhase = phaseID
	} else {
		cs.NextMatch = 0
		cs.NextMatchPhase = 0
	}
	return db.InsertOrUpdate(ctx, m.db.DB, cs)
}

// clockMatchTarget resolves the match a clock run should act on for
// the active phase.  It returns the phase, the current match and the
// following match (0 when none).  errClockNoMatch is returned when
// the active phase has no non-terminal match.
func (m *Module) clockMatchTarget(ctx context.Context, cs *ClockSettings) (*GamePhase, int, int, error) {
	phase, err := m.activePhase(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	if phase == nil {
		return nil, 0, 0, fmt.Errorf("no active phase")
	}
	nums, err := m.nonTerminalMatchNumbers(ctx, phase.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	override := m.clockNextMatchOverride(ctx, cs, phase.ID)
	cur, next := computeClockTargets(override, nums)
	if cur == 0 {
		return nil, 0, 0, fmt.Errorf("no non-terminal match in the active phase %q", phase.Name)
	}
	return phase, cur, next, nil
}

// clockEndMatchTransition performs the end-of-run transition for the
// match that is currently Running in the active phase (at most one by
// invariant).  A missing bus is irrelevant here; errors are logged and
// never fail the caller, matching the publish-and-never-fail posture
// of the auto-stop path.
func (m *Module) clockEndMatchTransition() {
	ctx := context.Background()
	cs, err := m.clockSettings(ctx)
	if err != nil || !cs.Transitions {
		return
	}
	phase, err := m.activePhase(ctx)
	if err != nil || phase == nil {
		slog.Error("Clock end transition: no active phase", "error", err)
		return
	}
	running, err := gorm.G[MatchPlacement](m.db.DB).
		Where(&MatchPlacement{PhaseID: phase.ID, State: MatchStateRunning}).
		Find(ctx)
	if err != nil {
		slog.Error("Clock end transition: loading running placements", "error", err)
		return
	}
	if len(running) == 0 {
		slog.Error("Clock end transition: no running match in the active phase")
		return
	}
	match := running[0].Match
	if err := m.applyClockMatchEnd(ctx, phase.ID, match); err != nil {
		slog.Error("Clock end transition: setting match scorable", "match", match, "error", err)
		return
	}
	slog.Debug("Clock end transition: match is scorable", "phase", phase.ID, "match", match)
}

// clockTransitionsEnabled reports whether clock-driven match state
// transitions are enabled in the settings.
func (m *Module) clockTransitionsEnabled(ctx context.Context) bool {
	cs, err := m.clockSettings(ctx)
	return err == nil && cs.Transitions
}

// clockCancelMatchTransitions reverts the match state transitions a
// clock run made in the active phase: Running and Queued placements
// go back to Scheduled.  A missing active phase is a no-op (nothing
// to revert).
func (m *Module) clockCancelMatchTransitions(ctx context.Context) error {
	phase, err := m.activePhase(ctx)
	if err != nil {
		return err
	}
	if phase == nil {
		return nil
	}
	return m.applyClockMatchCancel(ctx, phase.ID)
}
