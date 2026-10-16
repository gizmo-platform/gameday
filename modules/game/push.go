package game

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"sync"
	"time"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/gizmo-platform/gameday/pkg/cloud"
	"github.com/gizmo-platform/gameday/pkg/db"
)

// placementKey is the natural key of a cloud placement: the contest
// plus the schedule slot. Match numbers repeat across phases, so the
// phase is part of the key.
type placementKey struct {
	ContestID  uint
	PhaseID    uint
	Round      int
	Match      int
	FieldID    uint
	PositionID uint
}

// cloudContestScoresData is the public endpoint that stores a site's
// schedule and scorecard data for one contest. The contest ID in the
// URL is authoritative; the body is a bare array of placement rows.
// Storing is an upsert on the placement's natural key in a
// transaction, so a re-push updates matching placements in place
// (keeping their IDs stable) and removes placements that are no
// longer in the schedule.
func (m *Module) cloudContestScoresData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	contestID := m.ws.StrToUint(chi.URLParam(r, "id"))
	if contestID == 0 {
		slog.Error("Error storing contest scores", "error", errors.New("invalid contest id"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid contest id"})
		return
	}

	var placements []cloud.ContestPlacement
	if err := json.NewDecoder(r.Body).Decode(&placements); err != nil {
		slog.Error("Error decoding contest scores payload", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Dedupe the payload on the natural key; the last row wins.
	incoming := make(map[placementKey]cloud.ContestPlacement, len(placements))
	for _, p := range placements {
		incoming[placementKey{
			ContestID:  contestID,
			PhaseID:    p.PhaseID,
			Round:      p.Round,
			Match:      p.Match,
			FieldID:    p.FieldID,
			PositionID: p.PositionID,
		}] = p
	}

	err := m.db.Transaction(func(tx *gorm.DB) error {
		// Prune placements for this contest that are no longer in
		// the schedule, along with their scorecard values.
		old, err := gorm.G[CloudMatchPlacement](tx).
			Where(&CloudMatchPlacement{ContestID: contestID}).
			Find(ctx)
		if err != nil {
			return err
		}
		for _, p := range old {
			if _, ok := incoming[placementKey{
				ContestID:  contestID,
				PhaseID:    p.PhaseID,
				Round:      p.Round,
				Match:      p.Match,
				FieldID:    p.FieldID,
				PositionID: p.PositionID,
			}]; ok {
				continue
			}
			if _, err := gorm.G[CloudScorecardValue](tx).
				Where("cloud_match_placement_id = ?", p.ID).
				Delete(ctx); err != nil {
				return err
			}
			if _, err := gorm.G[CloudMatchPlacement](tx).
				Where("id = ?", p.ID).
				Delete(ctx); err != nil {
				return err
			}
		}

		for key, p := range incoming {
			row := CloudMatchPlacement{
				ContestID:  key.ContestID,
				Round:      key.Round,
				Match:      key.Match,
				PhaseID:    key.PhaseID,
				TeamID:     p.TeamID,
				TeamName:   p.TeamName,
				FieldID:    key.FieldID,
				PositionID: key.PositionID,
				State:      p.State,
				Score:      p.Score,
			}
			if err := db.InsertOrUpdate(ctx, tx, &row, clause.OnConflict{
				Columns: []clause.Column{
					{Name: "contest_id"},
					{Name: "phase_id"},
					{Name: "round"},
					{Name: "match"},
					{Name: "field_id"},
					{Name: "position_id"},
				},
				DoUpdates: clause.AssignmentColumns([]string{"team_id", "team_name", "state", "score"}),
			}); err != nil {
				return err
			}
			if row.ID == 0 {
				found, err := gorm.G[CloudMatchPlacement](tx).
					Where(&CloudMatchPlacement{
						ContestID:  key.ContestID,
						PhaseID:    key.PhaseID,
						Round:      key.Round,
						Match:      key.Match,
						FieldID:    key.FieldID,
						PositionID: key.PositionID,
					}).
					First(ctx)
				if err != nil {
					return err
				}
				row.ID = found.ID
			}

			if _, err := gorm.G[CloudScorecardValue](tx).
				Where("cloud_match_placement_id = ?", row.ID).
				Delete(ctx); err != nil {
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
		slog.Error("Error storing contest scores", "error", err, "contest_id", contestID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]int{"stored": len(incoming)}); err != nil {
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

// autoPushTimeout bounds a single best-effort push (export plus
// upload), so a hung cloud cannot keep the worker alive indefinitely.
const autoPushTimeout = 30 * time.Second

// autoPushState tracks the background push worker.  At most one push
// runs at a time; a request that arrives while one is running marks
// the state pending, and the worker runs exactly one more push when it
// finishes.  Each push replaces the contest's full data on the cloud,
// so serializing prevents an older export from landing after a newer
// one, and the follow-up push carries every change made meanwhile.
type autoPushState struct {
	mu      sync.Mutex
	running bool
	pending bool

	// wg tracks the worker goroutine so tests can wait for it.
	wg sync.WaitGroup
}

// scheduleCloudPush requests a best-effort push of the selected
// contest's data to the cloud.  It never blocks and never fails the
// caller: the push runs in the background, a server with no contest
// selected is skipped, and failures are only logged (the manual Cloud
// Push page remains the way to retry).
func (m *Module) scheduleCloudPush() {
	s := &m.autoPush
	s.mu.Lock()
	if s.running {
		s.pending = true
		s.mu.Unlock()
		return
	}
	s.running = true
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		for {
			m.autoPushOnce()

			s.mu.Lock()
			if !s.pending {
				s.running = false
				s.mu.Unlock()
				return
			}
			s.pending = false
			s.mu.Unlock()
		}
	}()
}

// autoPushOnce performs a single best-effort push.
func (m *Module) autoPushOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), autoPushTimeout)
	defer cancel()

	contestID := m.selectedContestID(ctx)
	if contestID == 0 {
		slog.Debug("Skipping automatic cloud push, no contest selected")
		return
	}
	if err := m.PushContestData(ctx); err != nil {
		slog.Warn("Automatic cloud push failed", "error", err, "contest_id", contestID)
		return
	}
	slog.Debug("Automatic cloud push complete", "contest_id", contestID)
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
	contestID := m.selectedContestID(r.Context())
	if err := m.PushContestData(r.Context()); err != nil {
		slog.Error("Error pushing contest data to cloud", "error", err, "contest_id", contestID)
		m.ws.DoTemplate(w, r, "views/game/push_cloud.p2", pongo2.Context{
			"cloudBase":  m.cloudClient.BaseURL(),
			"contestID":  contestID,
			"error":      err,
			"moduleBase": m.basePath,
		})
		return
	}
	http.Redirect(w, r, path.Join(m.basePath, "/push-cloud")+"?pushed=1", http.StatusSeeOther)
}
