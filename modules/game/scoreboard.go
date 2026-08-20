package game

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/flosch/pongo2/v6"
	"github.com/the-maldridge/authware"
	"gorm.io/gorm"
)

type scoreboardDataResponse struct {
	Hidden  bool   `json:"hidden,omitempty"`
	Message string `json:"message,omitempty"`
}

func (m *Module) uiViewScoreboard(w http.ResponseWriter, r *http.Request) {
	m.ws.DoTemplate(w, r, "views/game/scoreboard.p2", nil)
}

func (m *Module) uiViewScoreboardData(w http.ResponseWriter, r *http.Request) {
	// Get the active phase if none was specified
	phaseID := m.ws.StrToUint(r.URL.Query().Get("phase"))

	// Check if scores are hidden for this phase
	var phase GamePhase
	var err error
	if phaseID == 0 {
		phase, err = gorm.G[GamePhase](m.db.DB).Where("active = true").First(r.Context())
	} else {
		phase, err = gorm.G[GamePhase](m.db.DB).Where(&GamePhase{ID: phaseID}).First(r.Context())
	}
	if err != nil {
		slog.Error("Error fetching phase", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	if phase.HideScores {
		_, loggedIn := r.Context().Value(authware.UserKey{}).(authware.User)
		if !loggedIn {
			resp := scoreboardDataResponse{
				Hidden:  true,
				Message: "Scores are currently hidden for this phase.",
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				slog.Error("Error sending scoreboard data", "error", err)
			}
			return
		}
	}

	rowData, err := m.scoreboardRankings(r.Context(), phaseID, r.URL.Query().Get("division"))
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
