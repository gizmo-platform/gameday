package game

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"sort"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
)

// RankingRow is a single team's aggregated scoreboard row for one
// phase (and, for a division aware phase, one division).  Score holds
// the value the phase's ScoreSummation selected, and Rank is the
// team's position, resolved through the phase's TieBreaker when one is
// configured.
type RankingRow struct {
	Team     team.Team
	TeamID   uint
	Rank     int
	Average  int `gorm:"column:avg"`
	Mulligan int
	Total    int
	Score    int
	Max      int
	Min      int
	Count    int
}

func (m *Module) scoreboardRankings(ctx context.Context, phaseID uint, division string) ([]RankingRow, error) {
	var phase GamePhase
	var err error
	if phaseID == 0 {
		phase, err = gorm.G[GamePhase](m.db.DB).Where("active = true").First(ctx)
	} else {
		phase, err = gorm.G[GamePhase](m.db.DB).Where(&GamePhase{ID: phaseID}).First(ctx)
	}
	if err != nil {
		slog.Error("Error fetching scoreboard data", "error", err)
		return nil, err
	}

	orderBy := "mulligan"
	switch phase.ScoreSummation {
	case "Total":
		orderBy = "total"
	}

	// This query is terrible, but it is what is required to
	// generate the SQL using the query builder.  Even then its
	// not entirely portable, as it depends on the existence of
	// SUM(), MAX(), MIN(), and COUNT() being direct callables in
	// the dialect of SQL this runs against.  Since this is only
	// really expected to run against SQLite and PostgreSQL, this
	// is fine (TM) but if someone decides to run this against
	// T-SQL, undefined behaviors may happen.
	rowData, err := gorm.G[RankingRow](m.db.DB,
		clause.Select{
			Expression: clause.CommaExpression{Exprs: []clause.Expression{
				clause.NamedExpr{SQL: "?", Vars: []interface{}{clause.Column{Name: "team_id"}}},
				clause.NamedExpr{SQL: "CAST(AVG(?) AS INT) AS avg", Vars: []interface{}{clause.Column{Name: "score"}}},
				clause.NamedExpr{
					SQL: "COALESCE((SUM(?) - MIN(?)) / (COUNT(?) - 1), CAST(AVG(?) AS INT)) AS mulligan",
					Vars: []interface{}{
						clause.Column{Name: "score"},
						clause.Column{Name: "score"},
						clause.Column{Name: "score"},
						clause.Column{Name: "score"},
					},
				},
				clause.NamedExpr{SQL: "SUM(?) AS total", Vars: []interface{}{clause.Column{Name: "score"}}},
				clause.NamedExpr{SQL: "MIN(?) AS min", Vars: []interface{}{clause.Column{Name: "score"}}},
				clause.NamedExpr{SQL: "MAX(?) AS max", Vars: []interface{}{clause.Column{Name: "score"}}},
				clause.NamedExpr{SQL: "COUNT(?) AS count", Vars: []interface{}{clause.Column{Name: "score"}}},
			}},
		},
		clause.From{Tables: []clause.Table{{Name: "match_scores"}}},
		clause.Where{Exprs: []clause.Expression{clause.Eq{Column: "game_phase_id", Value: phase.ID}}},
		clause.GroupBy{Columns: []clause.Column{{Name: "team_id"}}},
		clause.OrderBy{Columns: []clause.OrderByColumn{{
			Column: clause.Column{Name: orderBy},
			Desc:   true,
		}}},
	).Preload("Team.Division", nil).Find(ctx)
	if err != nil {
		slog.Error("Error selecting scoreboard data", "error", err)
		return nil, err
	}

	out := []RankingRow{}
	for _, row := range rowData {
		if row.Team.Division.Name != division && division != "" {
			continue
		}
		out = append(out, row)
	}

	rank := 1
	for i, row := range out {
		switch phase.ScoreSummation {
		case "Total":
			out[i].Score = row.Total
		case "AverageWithMulligan":
			out[i].Score = row.Mulligan
		}

		// Setup the rank, which is different than the index
		// because of ties.
		if i > 0 && out[i-1].Score != out[i].Score {
			rank++
		}
		out[i].Rank = rank
	}

	// Resolve ties using phase-configured tie-breaker.
	if phase.TieBreaker != "" {
		resolveTies(ctx, m.db, &out, &phase)
	}

	return out, nil
}

// PhaseStats holds the aggregate score statistics for a single game
// phase.
type PhaseStats struct {
	Min int
	Max int
	Avg int

	// MinPlacement and MaxPlacement are the placements (with their
	// team, field, and position) that produced the minimum and maximum
	// scores. When several placements share an extreme value, the one
	// with the lowest MatchScore ID is reported.
	MinPlacement MatchPlacement
	MaxPlacement MatchPlacement
}

// PhaseScoreStats returns the minimum, maximum, and average score for
// the given game phase. For the minimum and maximum, the MatchPlacement
// (with its team, field, and position) that produced the value is
// included. A phase with no scores returns a zero PhaseStats and a nil
// error.
func (m *Module) PhaseScoreStats(ctx context.Context, phaseID uint) (PhaseStats, error) {
	var out PhaseStats

	rows, err := gorm.G[MatchScore](m.db.DB).
		Where(&MatchScore{GamePhaseID: phaseID}).
		Order("id ASC").
		Preload("MatchPlacement.Team", nil).
		Preload("MatchPlacement.Field", nil).
		Preload("MatchPlacement.Position", nil).
		Find(ctx)
	if err != nil {
		slog.Error("Error fetching phase scores", "error", err)
		return out, err
	}

	if len(rows) == 0 {
		return out, nil
	}

	sum := 0
	for i, r := range rows {
		sum += r.Score
		if i == 0 || r.Score < out.Min {
			out.Min = r.Score
			out.MinPlacement = r.MatchPlacement
		}
		if i == 0 || r.Score > out.Max {
			out.Max = r.Score
			out.MaxPlacement = r.MatchPlacement
		}
	}
	out.Avg = sum / len(rows)

	return out, nil
}

// PhaseRankings returns the final rankings of every phase carrying the
// given role, as one PhaseRanking per phase-and-division pair (a phase
// that is not division aware yields a single entry with an empty
// Division; a division aware phase yields one entry per division that
// has ranked teams).  Ranks use each phase's configured
// ScoreSummation and TieBreaker and restart at 1 in each division.
// When frozenOnly is set, phases that are not frozen are silently
// skipped.  An unknown or unused role yields an empty slice, not an
// error.  Phases are returned in ID order, which matches tournament
// order only when phases are authored with monotonically increasing
// IDs.
func (m *Module) PhaseRankings(ctx context.Context, role PhaseRole, frozenOnly bool) ([]PhaseRanking, error) {
	// The string form is deliberate: Where(&GamePhase{Role: ""})
	// would drop the zero-value condition and match every phase.
	phases, err := gorm.G[GamePhase](m.db.DB).
		Where("phase_role = ?", role).
		Order("id ASC").
		Find(ctx)
	if err != nil {
		slog.Error("Error fetching phases by role", "error", err, "role", role)
		return nil, err
	}

	out := []PhaseRanking{}
	for _, phase := range phases {
		if frozenOnly && !phase.Frozen {
			continue
		}
		tables, err := m.phaseRankingForPhase(ctx, phase)
		if err != nil {
			return nil, err
		}
		out = append(out, tables...)
	}
	return out, nil
}

// runAdvancementFilters applies every advancement filter configured on
// a phase for a single division.  The filters run in order against a
// single shared candidate pool: an include filter adds the teams it
// selects, and an exclude filter removes the teams it selects.  Teams
// an exclude filter removes join a blocklist that is then stripped from
// the field every later filter sees, so an include filter's cutoff
// applies to the teams that remain (a "best of the losers" ranking) and
// no later include can re-advance them.  For a filter with SelectFrom
// of 0 the scoreboard is left empty so the filter selects from the full
// roster (the start-of-schedule case); otherwise the scoreboard for the
// referenced phase is loaded.  A filter with a When condition is
// evaluated against the same context as a phase When expression (with
// the filter's own raw scoreboard) and is skipped entirely when it
// evaluates to false.  The remaining candidates and the accumulated
// determinations are returned.
func (m *Module) runAdvancementFilters(ctx context.Context, phase GamePhase, phases []GamePhase, phaseComplete map[uint]bool, division string, teams []team.Team) (map[uint]struct{}, []AdvancementDeterminationResult, error) {
	determinations := []AdvancementDeterminationResult{}
	// candidates is the shared pool of teams advanced so far; include
	// filters add to it.  excluded is the blocklist of teams an exclude
	// filter removed, which is stripped from the field of every later
	// filter and must never be re-advanced.
	candidates := make(map[uint]team.Team)
	excluded := make(map[uint]struct{})
	wctx := WhenContext{
		Phases:   phaseStates(phases, phaseComplete),
		Roster:   makeRoster(teams),
		Division: division,
	}
	for _, filter := range phase.AdvancementFilters {
		var rowData []RankingRow
		if filter.SelectFrom != 0 {
			var err error
			rowData, err = m.scoreboardRankings(ctx, filter.SelectFrom, division)
			if err != nil {
				slog.Error("Error retrieving filter scoreboard", "filter", filter)
				return nil, nil, err
			}
		}

		wctx.Scoreboard = rowData
		satisfied, err := evalWhen(filter.When, wctx, fmt.Sprintf("When expression on filter %q", filter.Rule))
		if err != nil {
			slog.Error("Error evaluating filter When", "filter", filter, "error", err)
			return nil, nil, err
		}
		if !satisfied {
			slog.Debug("Skipping advancement filter (When not satisfied)", "filter", filter)
			continue
		}

		f, exists := filters[filter.Filter]
		if !exists {
			slog.Error("Tried to load unregistered filter", "filter", filter)
			return nil, nil, fmt.Errorf("advancement filter %q is not registered", filter.Filter)
		}

		// The field the filter sees is the roster minus the
		// blocklist, so a cutoff applies to the teams that remain
		// rather than to the whole field.
		remaining := make(map[uint]team.Team, len(teams))
		for id, t := range makeRoster(teams) {
			if _, blocked := excluded[id]; !blocked {
				remaining[id] = t
			}
		}
		remainingBoard := make([]RankingRow, 0, len(rowData))
		for _, row := range rowData {
			if _, blocked := excluded[row.Team.ID]; !blocked {
				remainingBoard = append(remainingBoard, row)
			}
		}
		sctx := AdvancementFilterContext{
			Roster:     makeRoster(teams),
			Remaining:  remaining,
			Scoreboard: remainingBoard,
		}

		if filter.Mode == GamePhaseAdvancementFilterModeExclude {
			// An exclude filter removes teams.  Run it against a
			// copy of the remaining field so every removal lands;
			// the field-minus-result diff is the filter's full
			// target set, which joins the blocklist.
			sctx.Candidates = make(map[uint]team.Team, len(remaining))
			maps.Copy(sctx.Candidates, remaining)
			if err := f.Apply(&sctx, filter.Rule, filter.Mode, filter.SliceExpr); err != nil {
				slog.Error("Error applying advancement filter", "filter", filter, "error", err)
				return nil, nil, err
			}
			for id := range remaining {
				if _, kept := sctx.Candidates[id]; !kept {
					excluded[id] = struct{}{}
				}
			}
			// Keep the pool free of newly blocked teams as well.
			for id := range excluded {
				delete(candidates, id)
			}
		} else {
			sctx.Candidates = candidates
			if err := f.Apply(&sctx, filter.Rule, filter.Mode, filter.SliceExpr); err != nil {
				slog.Error("Error applying advancement filter", "filter", filter, "error", err)
				return nil, nil, err
			}
		}

		determinations = append(determinations, sctx.Determinations...)
	}

	advancing := make(map[uint]struct{}, len(candidates))
	for id := range candidates {
		advancing[id] = struct{}{}
	}
	return advancing, determinations, nil
}

// filterWhenSatisfied evaluates a single filter's When expression for
// one division, using the filter's own scoreboard as the Scoreboard
// context (mirroring runAdvancementFilters).  An empty When expression
// is always satisfied, i.e. the filter always runs.
func (m *Module) filterWhenSatisfied(ctx context.Context, phases []GamePhase, phaseComplete map[uint]bool, teams []team.Team, filter GamePhaseAdvancementFilter, division string) (bool, error) {
	scoreboard := []RankingRow{}
	if filter.SelectFrom != 0 {
		var err error
		scoreboard, err = m.scoreboardRankings(ctx, filter.SelectFrom, division)
		if err != nil {
			return false, err
		}
	}
	wctx := WhenContext{
		Phases:     phaseStates(phases, phaseComplete),
		Roster:     makeRoster(teams),
		Scoreboard: scoreboard,
		Division:   division,
	}
	return evalWhen(filter.When, wctx, fmt.Sprintf("When expression on filter %q", filter.Rule))
}

// phaseSchedulable reports whether the filters configured on a phase
// can be satisfied right now.  A filter is satisfied when its source
// phase is complete and frozen; a roster-sourced filter (SelectFrom
// of 0) is always satisfied.  A filter whose When condition evaluates
// to false is skipped entirely, so it can never block the phase from
// being scheduled; for a division-aware phase the filter only blocks
// when its When condition is true for at least one configured
// division.  A phase with no filters is schedulable.
func (m *Module) phaseSchedulable(ctx context.Context, phase GamePhase, phases []GamePhase, phaseComplete map[uint]bool, teams []team.Team) (bool, error) {
	filters, err := gorm.G[GamePhaseAdvancementFilter](m.db.DB).
		Where(&GamePhaseAdvancementFilter{GamePhaseID: phase.ID}).
		Find(ctx)
	if err != nil {
		return false, err
	}
	for _, filter := range filters {
		if filter.SelectFrom == 0 {
			continue
		}

		// Determine whether this filter's source phase is required
		// at all right now.  A filter gated by a When condition that
		// is false is skipped by runAdvancementFilters, so it must
		// not keep the phase from being scheduled.
		required := true
		if filter.When != "" {
			divisions := []string{""}
			if phase.DivisionAware {
				divisions, err = m.whenDivisions(ctx, phase)
				if err != nil {
					return false, err
				}
			}
			required = false
			for _, division := range divisions {
				runs, err := m.filterWhenSatisfied(ctx, phases, phaseComplete, teams, filter, division)
				if err != nil {
					return false, err
				}
				if runs {
					required = true
					break
				}
			}
			if !required {
				slog.Debug("Filter not required (When not satisfied), not blocking schedulability",
					"phase", phase.Name,
					"rule", filter.Rule,
					"source_id", filter.SelectFrom,
				)
				continue
			}
		}

		slog.Debug("Evaluating filter satisfaction",
			"phase", phase.Name,
			"rule", filter.Rule,
			"source_id", filter.SelectFrom,
			"source_complete", phaseComplete[filter.SelectFrom],
			"source_frozen", phases[filter.SelectFrom-1].Frozen,
		)
		if !phaseComplete[filter.SelectFrom] || !phases[filter.SelectFrom-1].Frozen {
			return false, nil
		}
	}
	return true, nil
}

func resolveTies(ctx context.Context, dbd *db.DB, rows *[]RankingRow, phase *GamePhase) {
	if len(*rows) == 0 {
		return
	}
	tb, ok := modules.GetTieBreaker(phase.TieBreaker)
	if !ok {
		slog.Warn("Tie-breaker not found", "name", phase.TieBreaker)
		return
	}

	// Group rows by rank to find tied groups.
	// A tied group has 2+ rows sharing the same rank.
	type tiedGroup struct {
		divisionName string
		indices      []int
	}
	var groups []tiedGroup
	currentRank := (*rows)[0].Rank
	var currentIndices []int
	for i := range *rows {
		if (*rows)[i].Rank == currentRank {
			currentIndices = append(currentIndices, i)
		} else {
			if len(currentIndices) > 1 {
				if phase.DivisionAware {
					// Split by division.
					byDiv := make(map[string][]int)
					for _, idx := range currentIndices {
						divName := (*rows)[idx].Team.Division.Name
						byDiv[divName] = append(byDiv[divName], idx)
					}
					for div, indices := range byDiv {
						if len(indices) > 1 {
							groups = append(groups, tiedGroup{divisionName: div, indices: indices})
						}
					}
				} else {
					groups = append(groups, tiedGroup{divisionName: "", indices: currentIndices})
				}
			}
			currentRank = (*rows)[i].Rank
			currentIndices = []int{i}
		}
	}
	// Handle the last group.
	if len(currentIndices) > 1 {
		if phase.DivisionAware {
			byDiv := make(map[string][]int)
			for _, idx := range currentIndices {
				divName := (*rows)[idx].Team.Division.Name
				byDiv[divName] = append(byDiv[divName], idx)
			}
			for div, indices := range byDiv {
				if len(indices) > 1 {
					groups = append(groups, tiedGroup{divisionName: div, indices: indices})
				}
			}
		} else {
			groups = append(groups, tiedGroup{divisionName: "", indices: currentIndices})
		}
	}

	if len(groups) == 0 {
		return
	}

	// Count total matches for this phase (for tie-breaker context).
	var matchCount int64
	if err := dbd.Model(&MatchScore{}).Where("game_phase_id = ?", phase.ID).Count(&matchCount).Error; err != nil {
		slog.Warn("Error counting matches for tie-breaker context", "error", err)
	}

	// Build ordered rank assignments for each tied group.
	type rankAssignment struct {
		teamNumber int
		newRank    int
	}
	assignments := make(map[int]rankAssignment)

	for g, group := range groups {
		teamNumbers := make([]int, len(group.indices))
		for i, idx := range group.indices {
			teamNumbers[i] = (*rows)[idx].Team.Number
		}

		ordered := tb(teamNumbers, int(matchCount))

		baseRank := group.indices[0]
		for i, num := range ordered {
			for _, idx := range group.indices {
				if (*rows)[idx].Team.Number == num {
					assignments[idx] = rankAssignment{teamNumber: num, newRank: baseRank + 1 + i}
					break
				}
			}
		}
		_ = g
	}

	// Apply new ranks and sort by new rank.
	for idx, a := range assignments {
		(*rows)[idx].Rank = a.newRank
	}
	sort.SliceStable(*rows, func(i, j int) bool {
		return (*rows)[i].Rank < (*rows)[j].Rank
	})
}
