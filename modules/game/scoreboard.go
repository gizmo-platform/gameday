package game

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/flosch/pongo2/v6"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/gizmo-platform/gameday/modules/team"
)

type scoreboardRow struct {
	Team     team.Team
	TeamID   uint
	Average  int `gorm:"column:avg"`
	Mulligan int
	Total    int
	Score    int
	Max      int
	Min      int
	Count    int
}

func (m *Module) uiViewScoreboard(w http.ResponseWriter, r *http.Request) {
	m.ws.DoTemplate(w, r, "views/game/scoreboard.p2", nil)
}

func (m *Module) uiViewScoreboardData(w http.ResponseWriter, r *http.Request) {
	// Get the active phase if none was specified
	phaseID := m.ws.StrToUint(r.URL.Query().Get("phase"))
	var phase GamePhase
	var err error
	if phaseID == 0 {
		phase, err = gorm.G[GamePhase](m.db.DB).Where("active = true").First(r.Context())
	} else {
		phase, err = gorm.G[GamePhase](m.db.DB).Where(&GamePhase{ID: phaseID}).First(r.Context())
	}
	if err != nil {
		slog.Error("Error fetching scoreboard data", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	orderBy := "mulligan"
	switch(phase.ScoreSummation) {
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
				clause.NamedExpr{"AVG(?) AS avg", []interface{}{clause.Column{Name: "score"}}},
				clause.NamedExpr{
					"COALESCE((SUM(?) - MIN(?)) / (COUNT(?) - 1), AVG(?)) AS mulligan",
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
			Desc: true,
		}}},
	).Preload("Team", nil).Find(r.Context())
	if err != nil {
		slog.Error("Error selecting scoreboard data", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	for i, row := range rowData {
		switch(phase.ScoreSummation) {
		case "Total":
			rowData[i].Score = row.Total
		case "AverageWithMulligan":
			rowData[i].Score = row.Mulligan
		}
	}

	if err := json.NewEncoder(w).Encode(rowData); err != nil {
		slog.Error("Error sending scoreboard data", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
}
