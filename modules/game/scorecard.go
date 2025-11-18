package game

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) uiViewScorecard(w http.ResponseWriter, r *http.Request) {
	placement := MatchPlacement{}
	placementFilter := MatchPlacement{
		PhaseID:    m.ws.StrToUint(chi.URLParam(r, "phase")),
		Match:      int(m.ws.StrToUint(chi.URLParam(r, "match"))),
		FieldID:    m.ws.StrToUint(chi.URLParam(r, "field")),
		PositionID: m.ws.StrToUint(chi.URLParam(r, "position")),
	}
	if res := m.db.Preload(clause.Associations).Where(placementFilter).First(&placement); res.Error != nil {
		slog.Error("Error retreiving scorecard", "error", res.Error)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
	}

	elements := []GameElement{}
	if res := m.db.Preload("States.Values").Preload(clause.Associations).Find(&elements); res.Error != nil {
		slog.Error("Error retreiving scorecard", "error", res.Error)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
	}

	sce := []ScorecardElement{}
	m.db.Where(&ScorecardElement{MatchPlacementID: placement.ID}).Find(&sce)
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

	placement := MatchPlacement{}
	placementFilter := MatchPlacement{
		PhaseID:    m.ws.StrToUint(chi.URLParam(r, "phase")),
		Match:      int(m.ws.StrToUint(chi.URLParam(r, "match"))),
		FieldID:    m.ws.StrToUint(chi.URLParam(r, "field")),
		PositionID: m.ws.StrToUint(chi.URLParam(r, "position")),
	}
	if res := m.db.Where(placementFilter).First(&placement); res.Error != nil {
		slog.Error("Error retreiving scorecard", "error", res.Error)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
	}

	m.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Where(&ScorecardElement{MatchPlacementID: placement.ID}).Delete(&ScorecardElement{})
		if res.Error != nil {
			return res.Error
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

			if res := tx.Save(&sce); res.Error != nil {
				slog.Error("Error saving scorecard element", "error", res.Error)
				return res.Error
			}
		}
		return nil
	})
}
