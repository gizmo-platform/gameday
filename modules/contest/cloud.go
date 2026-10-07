package contest

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/pkg/cloud"
)

// cloudContestsData is the public JSON endpoint listing every contest
// the cloud instance holds, ordered by name. It is public on purpose,
// like the other /cloud/* endpoints: the contest list is pulled by
// onsite servers to let them pick which contest they are handling.
// Each entry carries the cloud contest ID, which an onsite server
// adopts as its own local contest ID.
func (m *Module) cloudContestsData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	contests, err := gorm.G[Contest](m.db.DB).
		Preload("Region", nil).
		Order("name ASC").
		Find(ctx)
	if err != nil {
		slog.Error("Error listing contests for cloud export", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	out := make([]cloud.ContestInfo, 0, len(contests))
	for _, c := range contests {
		out = append(out, cloud.ContestInfo{
			ID:     c.ID,
			Name:   c.Name,
			Region: c.Region.Name,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		slog.Error("Error encoding cloud contests", "error", err)
	}
}

// cloudContestRosterData is the public JSON endpoint listing the teams
// on a single contest's roster, ordered by team number. It is public
// on purpose, like the other /cloud/* endpoints: onsite servers pull
// the roster of the contest they just selected.
func (m *Module) cloudContestRosterData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := m.ws.StrToUint(chi.URLParam(r, "id"))

	roster, err := gorm.G[ContestRoster](m.db.DB).
		Where(&ContestRoster{ContestID: id}).
		Preload("Team", nil).
		Preload("Team.Division", nil).
		Preload("Team.Region", nil).
		Find(ctx)
	if err != nil {
		slog.Error("Error loading contest roster for cloud export", "contest", id, "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	out := make([]cloud.TeamInfo, 0, len(roster))
	for _, row := range roster {
		out = append(out, cloud.TeamInfo{
			Number:   row.Team.Number,
			Name:     row.Team.Name,
			Division: row.Team.Division.Name,
			Region:   row.Team.Region.Name,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		slog.Error("Error encoding cloud contest roster", "contest", id, "error", err)
	}
}
