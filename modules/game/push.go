package game

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/pkg/cloud"
)

// cloudContestScoresData is the public endpoint that stores a site's
// schedule and scorecard data for one contest. The contest ID in the
// URL is authoritative; the body is a bare array of placement rows.
// Storing is delete-then-create in a transaction, so a re-push
// replaces the stored data for that contest in place.
func (m *Module) cloudContestScoresData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	contestID := m.ws.StrToUint(chi.URLParam(r, "id"))
	if contestID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var placements []cloud.ContestPlacement
	if err := json.NewDecoder(r.Body).Decode(&placements); err != nil {
		slog.Error("Error decoding contest scores payload", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	err := m.db.Transaction(func(tx *gorm.DB) error {
		old, err := gorm.G[CloudMatchPlacement](tx).
			Where(&CloudMatchPlacement{ContestID: contestID}).
			Find(ctx)
		if err != nil {
			return err
		}
		ids := make([]uint, 0, len(old))
		for _, p := range old {
			ids = append(ids, p.ID)
		}
		if len(ids) > 0 {
			if _, err := gorm.G[CloudScorecardValue](tx).
				Where("cloud_match_placement_id IN ?", ids).
				Delete(ctx); err != nil {
				return err
			}
		}
		if _, err := gorm.G[CloudMatchPlacement](tx).
			Where(&CloudMatchPlacement{ContestID: contestID}).
			Delete(ctx); err != nil {
			return err
		}

		for _, p := range placements {
			row := CloudMatchPlacement{
				ContestID:  contestID,
				Round:      p.Round,
				Match:      p.Match,
				PhaseID:    p.PhaseID,
				TeamID:     p.TeamID,
				TeamName:   p.TeamName,
				FieldID:    p.FieldID,
				PositionID: p.PositionID,
				State:      p.State,
				Score:      p.Score,
			}
			if err := gorm.G[CloudMatchPlacement](tx).Create(ctx, &row); err != nil {
				return err
			}
			for _, v := range p.Values {
				sv := CloudScorecardValue{
					CloudMatchPlacementID: row.ID,
					Element:               v.Element,
					Value:                 v.Value,
				}
				if err := gorm.G[CloudScorecardValue](tx).Create(ctx, &sv); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("Error storing contest scores", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]int{"stored": len(placements)}); err != nil {
		slog.Error("Error writing contest scores response", "error", err)
	}
}

// PushContestData exports this site's schedule and scorecard data for
// the selected contest and pushes it to the cloud instance.
func (m *Module) PushContestData(ctx context.Context) error {
	contestID := m.selectedContestID(ctx)
	if contestID == 0 {
		return errors.New("no contest is selected on this server")
	}

	placements, err := m.ExportContestData(ctx, contestID)
	if err != nil {
		return err
	}

	wire := make([]cloud.ContestPlacement, 0, len(placements))
	for _, p := range placements {
		values := make([]cloud.ContestScorecardValue, 0, len(p.Values))
		for _, v := range p.Values {
			values = append(values, cloud.ContestScorecardValue{Element: v.Element, Value: v.Value})
		}
		wire = append(wire, cloud.ContestPlacement{
			Round:      p.Round,
			Match:      p.Match,
			PhaseID:    p.PhaseID,
			TeamID:     p.TeamID,
			TeamName:   p.TeamName,
			FieldID:    p.FieldID,
			PositionID: p.PositionID,
			State:      uint(p.State),
			Score:      p.Score,
			Values:     values,
		})
	}

	return m.cloudClient.PushContestData(ctx, contestID, wire)
}

// uiViewPushCloud renders the cloud push page.
func (m *Module) uiViewPushCloud(w http.ResponseWriter, r *http.Request) {
	m.ws.DoTemplate(w, r, "views/game/push_cloud.p2", pongo2.Context{
		"cloudBase":  m.cloudClient.BaseURL(),
		"contestID":  m.selectedContestID(r.Context()),
		"pushed":     m.ws.StrToUint(r.URL.Query().Get("pushed")),
		"moduleBase": m.basePath,
	})
}

// uiViewPushCloudSubmit pushes the selected contest's schedule and
// scorecard data to the cloud instance.
func (m *Module) uiViewPushCloudSubmit(w http.ResponseWriter, r *http.Request) {
	if err := m.PushContestData(r.Context()); err != nil {
		slog.Error("Error pushing contest data to cloud", "error", err)
		m.ws.DoTemplate(w, r, "views/game/push_cloud.p2", pongo2.Context{
			"cloudBase":  m.cloudClient.BaseURL(),
			"contestID":  m.selectedContestID(r.Context()),
			"error":      err.Error(),
			"moduleBase": m.basePath,
		})
		return
	}
	http.Redirect(w, r, path.Join(m.basePath, "/push-cloud")+"?pushed=1", http.StatusSeeOther)
}
