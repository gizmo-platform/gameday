package game

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/expr-lang/expr"
	"github.com/flosch/pongo2/v6"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules/team"
)

// WhenPhaseState is the per-phase state made visible to a When
// expression.  The Phases slice in WhenContext is positional (in the
// same order as the phases list the game config was uploaded with),
// so Phases[N] is the Nth phase, zero-indexed.
type WhenPhaseState struct {
	ID       uint
	Name     string
	Active   bool
	Frozen   bool
	Complete bool
}

// WhenContext is the value handed to expr.Run when evaluating a phase
// When expression.  Phases is the positional phase list, Roster is the
// full team roster keyed by team ID, Scoreboard is the scoreboard of
// the phase referenced by this phase's first advancement filter (or of
// the active phase when there are none), and Division is the division
// being evaluated (empty string for the whole field).
type WhenContext struct {
	Phases     []WhenPhaseState
	Roster     map[uint]team.Team
	Scoreboard []scoreboardRow
	Division   string
}

// phaseCompletionStates reports, for each phase, whether every one of
// its placements is in a terminal state and at least one placement
// exists.
func (m *Module) phaseCompletionStates(ctx context.Context, phases []GamePhase) (map[uint]bool, error) {
	phaseComplete := make(map[uint]bool)
	for _, phase := range phases {
		completed, err1 := gorm.G[MatchPlacement](m.db.DB).
			Where(&MatchPlacement{PhaseID: phase.ID}).
			Where("state in (?)", []MatchState{
				MatchStateComplete,
				MatchStateNoShow,
				MatchStateDisqualified,
			}).
			Count(ctx, "*")
		if err1 != nil {
			return nil, err1
		}
		count, err2 := gorm.G[MatchPlacement](m.db.DB).
			Where(&MatchPlacement{PhaseID: phase.ID}).
			Where("state not in (?)", []MatchState{
				MatchStateComplete,
				MatchStateNoShow,
				MatchStateDisqualified,
			}).
			Count(ctx, "*")
		if err2 != nil {
			return nil, err2
		}
		phaseComplete[phase.ID] = (count == 0) && (completed > 0)
		slog.Debug("Phase completion state", "phase_id", phase.ID, "playable", count, "completed", completed)
	}
	return phaseComplete, nil
}

// whenDivisions returns the set of division names a phase's When
// condition must be evaluated against.  A division-aware phase is
// evaluated once per configured division; any other phase is evaluated
// once against the whole field (the empty string).
func (m *Module) whenDivisions(ctx context.Context, phase GamePhase) ([]string, error) {
	if !phase.DivisionAware {
		return []string{""}, nil
	}
	divisions, err := gorm.G[team.Division](m.db.DB).Find(ctx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, division := range divisions {
		out = append(out, division.Name)
	}
	return out, nil
}

// whenSatisfied evaluates a single phase's When expression for one
// division.  An empty When expression is always satisfied.  The
// scoreboard made visible to the expression is sourced from the phase's
// first advancement filter, falling back to the active phase.
func (m *Module) whenSatisfied(ctx context.Context, phase GamePhase, phases []GamePhase, phaseComplete map[uint]bool, teams []team.Team, division string) (bool, error) {
	if phase.When == "" {
		return true, nil
	}

	selectFrom := uint(0)
	filters, err := gorm.G[GamePhaseAdvancementFilter](m.db.DB).
		Where(&GamePhaseAdvancementFilter{GamePhaseID: phase.ID}).
		Find(ctx)
	if err != nil {
		return false, err
	}
	if len(filters) > 0 {
		selectFrom = filters[0].SelectFrom
	}

	scoreboard := []scoreboardRow{}
	if selectFrom != 0 {
		scoreboard, err = m.scoreboardRankings(ctx, selectFrom, division)
		if err != nil {
			return false, err
		}
	} else {
		activeCount, err := gorm.G[GamePhase](m.db.DB).Where("active = true").Count(ctx, "*")
		if err != nil {
			return false, err
		}
		if activeCount > 0 {
			scoreboard, err = m.scoreboardRankings(ctx, 0, division)
			if err != nil {
				return false, err
			}
		}
	}

	wctx := WhenContext{
		Phases:     phaseStates(phases, phaseComplete),
		Roster:     makeRoster(teams),
		Scoreboard: scoreboard,
		Division:   division,
	}

	return evalWhen(phase.When, wctx, fmt.Sprintf("When expression on phase %q", phase.Name))
}

// phaseStates projects a phase list and completion map into the
// WhenPhaseState slice exposed to When expressions as Phases.
func phaseStates(phases []GamePhase, phaseComplete map[uint]bool) []WhenPhaseState {
	phaseStates := make([]WhenPhaseState, 0, len(phases))
	for _, p := range phases {
		phaseStates = append(phaseStates, WhenPhaseState{
			ID:       p.ID,
			Name:     p.Name,
			Active:   p.Active,
			Frozen:   p.Frozen,
			Complete: phaseComplete[p.ID],
		})
	}
	return phaseStates
}

// makeRoster builds the Roster map exposed to When expressions from a
// team slice, keyed by team ID.
func makeRoster(teams []team.Team) map[uint]team.Team {
	roster := make(map[uint]team.Team, len(teams))
	for _, t := range teams {
		roster[t.ID] = t
	}
	return roster
}

// evalWhen compiles and runs a When expression against the supplied
// context and reports the boolean result.  An empty expression is
// always satisfied.  The label argument identifies the subject (phase
// or filter) in log and error messages.
func evalWhen(whenExpr string, wctx WhenContext, label string) (bool, error) {
	if whenExpr == "" {
		return true, nil
	}
	e, err := expr.Compile(whenExpr)
	if err != nil {
		slog.Error("Could not compile When expression", "label", label, "expr", whenExpr, "error", err)
		return false, err
	}
	out, err := expr.Run(e, wctx)
	if err != nil {
		slog.Error("Error executing When expression", "label", label, "expr", whenExpr, "error", err)
		return false, err
	}
	satisfied, ok := out.(bool)
	if !ok {
		return false, fmt.Errorf("%s did not evaluate to a boolean", label)
	}
	return satisfied, nil
}

// whenStates evaluates the When condition of every phase and returns,
// per phase ID, whether the condition is currently satisfied and the
// message to display while it is.  A division-aware phase is satisfied
// only when its condition holds for every configured division.
func (m *Module) whenStates(ctx context.Context, phases []GamePhase, phaseComplete map[uint]bool, teams []team.Team) (map[uint]bool, map[uint]string, error) {
	active := make(map[uint]bool)
	msgs := make(map[uint]string)
	for _, phase := range phases {
		if phase.When == "" {
			active[phase.ID] = true
			msgs[phase.ID] = phase.WhenMsg
			continue
		}
		divisions, err := m.whenDivisions(ctx, phase)
		if err != nil {
			return nil, nil, err
		}
		satisfied := true
		for _, division := range divisions {
			ok, err := m.whenSatisfied(ctx, phase, phases, phaseComplete, teams, division)
			if err != nil {
				return nil, nil, err
			}
			if !ok {
				satisfied = false
				break
			}
		}
		active[phase.ID] = satisfied
		if satisfied {
			msgs[phase.ID] = phase.WhenMsg
		}
	}
	return active, msgs, nil
}

// whenGate enforces a phase's When condition on the scheduling
// actions themselves, closing the vector of hitting the preview or
// accept endpoints directly while the condition is not met.  It
// renders an error page and reports true when the request should be
// aborted; false when the action may proceed.
func (m *Module) whenGate(w http.ResponseWriter, r *http.Request, phase GamePhase, teams []team.Team) bool {
	if phase.When == "" {
		return false
	}
	phases, err := gorm.G[GamePhase](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error loading phases for When gate", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return true
	}
	phaseComplete, err := m.phaseCompletionStates(r.Context(), phases)
	if err != nil {
		slog.Error("Error determining phase completion for When gate", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return true
	}
	divisions, err := m.whenDivisions(r.Context(), phase)
	if err != nil {
		slog.Error("Error loading divisions for When gate", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return true
	}
	for _, division := range divisions {
		satisfied, err := m.whenSatisfied(r.Context(), phase, phases, phaseComplete, teams, division)
		if err != nil {
			slog.Error("Error evaluating When condition", "phase", phase.ID, "division", division, "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return true
		}
		if !satisfied {
			slog.Info("When condition not satisfied, blocking schedule action", "phase", phase.ID, "division", division)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": fmt.Errorf("phase %q is not currently eligible for scheduling", phase.Name)})
			return true
		}
	}
	return false
}
