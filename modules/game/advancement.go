package game

import (
	"log/slog"
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/gizmo-platform/gameday/modules/team"
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

func (m *Module) scoreboardRankings(ctx context.Context, phaseID uint) ([]scoreboardRow, error) {
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
	).Preload("Team", nil).Find(ctx)
	if err != nil {
		slog.Error("Error selecting scoreboard data", "error", err)
		return nil, err
	}

	rank := 1
	for i, row := range rowData {
		switch phase.ScoreSummation {
		case "Total":
			rowData[i].Score = row.Total
		case "AverageWithMulligan":
			rowData[i].Score = row.Mulligan
		}

		// Setup the rank, which is different than the index
		// because of ties.
		if i > 0 && rowData[i-1].Score != rowData[i].Score {
			rank++
		}
		rowData[i].Rank = rank
	}

	return rowData, nil
}
