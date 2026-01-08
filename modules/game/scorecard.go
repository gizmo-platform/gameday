package game

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
)

func (m *Module) uiViewScorecard(w http.ResponseWriter, r *http.Request) {
	placementFilter := MatchPlacement{
		PhaseID:    m.ws.StrToUint(chi.URLParam(r, "phase")),
		Match:      int(m.ws.StrToUint(chi.URLParam(r, "match"))),
		FieldID:    m.ws.StrToUint(chi.URLParam(r, "field")),
		PositionID: m.ws.StrToUint(chi.URLParam(r, "position")),
	}
	placement, err := gorm.G[MatchPlacement](m.db.DB).
		Where(placementFilter).
		First(r.Context())
	if err != nil {
		slog.Error("Error retreiving scorecard", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	elements, err := gorm.G[GameElement](m.db.DB).
		Preload("States", nil).
		Preload("States.Values", nil).
		Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving scorecard", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	sce, err := gorm.G[ScorecardElement](m.db.DB).
		Where(&ScorecardElement{MatchPlacementID: placement.ID}).Find(r.Context())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		slog.Error("Error retreiving scorecard elements", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	scd := make(map[string]int)
	for _, e := range sce {
		scd[e.ElementID] = e.Value
	}

	slog.Debug("scorecard data", "mi", placement.Match, "data", scd)

	ctx := pongo2.Context{
		"MatchInfo":     placement,
		"Elements":      elements,
		"ScorecardData": scd,
	}

	m.ws.DoTemplate(w, r, "views/game/scorecard.p2", ctx)
}

func (m *Module) uiViewScorecardSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	placementFilter := MatchPlacement{
		PhaseID:    m.ws.StrToUint(chi.URLParam(r, "phase")),
		Match:      int(m.ws.StrToUint(chi.URLParam(r, "match"))),
		FieldID:    m.ws.StrToUint(chi.URLParam(r, "field")),
		PositionID: m.ws.StrToUint(chi.URLParam(r, "position")),
	}
	placement, err := gorm.G[MatchPlacement](m.db.DB).
		Where(placementFilter).
		First(r.Context())
	if err != nil {
		slog.Error("Error retreiving scorecard", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	m.db.Transaction(func(tx *gorm.DB) error {
		_, err := gorm.G[ScorecardElement](tx).
			Where(&ScorecardElement{MatchPlacementID: placement.ID}).
			Delete(r.Context())
		if err != nil {
			return err
		}

		for key := range r.Form {
			slog.Debug("form value", "key", key, "value", r.FormValue(key))

			v, err := strconv.Atoi(r.FormValue(key))
			if err != nil {
				slog.Error("Error retreiving scorecard", "error", err)
				m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
				return err
			}
			sce := ScorecardElement{
				MatchPlacementID: placement.ID,
				ElementID:        key,
				Value:            v,
			}

			if err := gorm.G[ScorecardElement](tx).Create(r.Context(), &sce); err != nil {
				slog.Error("Error saving scorecard element", "error", err)
				return err
			}
		}
		return nil
	})
}
