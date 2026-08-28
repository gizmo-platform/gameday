package game

import (
	"context"
	"log/slog"
	"sort"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
)

type scoreboardRow struct {
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

func (m *Module) scoreboardRankings(ctx context.Context, phaseID uint, division string) ([]scoreboardRow, error) {
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
	rowData, err := gorm.G[scoreboardRow](m.db.DB,
		clause.Select{
			Expression: clause.CommaExpression{Exprs: []clause.Expression{
				clause.NamedExpr{"?", []interface{}{clause.Column{Name: "team_id"}}},
				clause.NamedExpr{"CAST(AVG(?) AS INT) AS avg", []interface{}{clause.Column{Name: "score"}}},
				clause.NamedExpr{
					"COALESCE((SUM(?) - MIN(?)) / (COUNT(?) - 1), CAST(AVG(?) AS INT)) AS mulligan",
					[]interface{}{
						clause.Column{Name: "score"},
						clause.Column{Name: "score"},
						clause.Column{Name: "score"},
						clause.Column{Name: "score"},
					},
				},
				clause.NamedExpr{"SUM(?) AS total", []interface{}{clause.Column{Name: "score"}}},
				clause.NamedExpr{"MIN(?) AS min", []interface{}{clause.Column{Name: "score"}}},
				clause.NamedExpr{"MAX(?) AS max", []interface{}{clause.Column{Name: "score"}}},
				clause.NamedExpr{"COUNT(?) AS count", []interface{}{clause.Column{Name: "score"}}},
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

	out := []scoreboardRow{}
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

func resolveTies(ctx context.Context, dbd *db.DB, rows *[]scoreboardRow, phase *GamePhase) {
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
