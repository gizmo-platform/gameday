package game

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/flosch/pongo2/v6"
)

func (m *Module) uiViewScoreboard(w http.ResponseWriter, r *http.Request) {
	m.ws.DoTemplate(w, r, "views/game/scoreboard.p2", nil)
}

func (m *Module) uiViewScoreboardData(w http.ResponseWriter, r *http.Request) {
	// Get the active phase if none was specified
	phaseID := m.ws.StrToUint(r.URL.Query().Get("phase"))

	rowData, err := m.scoreboardRankings(r.Context(), phaseID, r.URL.Query().Get("phase"))
	if err != nil {
		slog.Error("Error retrieving scoreboard data", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	if err := json.NewEncoder(w).Encode(rowData); err != nil {
		slog.Error("Error sending scoreboard data", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
}
