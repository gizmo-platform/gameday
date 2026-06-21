package game

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
)

func (m *Module) uiViewScorecardList(w http.ResponseWriter, r *http.Request) {
	// This function is quite complicated.  It needs to do 3 basic
	// things based on what the user selected.  It needs to fill
	// in data that will be used to fill in the filter form, it
	// needs to parse that form and optionally cookie the user,
	// and it needs to actually consume the filters to figure out
	// what scorecards to fetch.

	type filterParams struct {
		Phases    []uint
		Fields    []uint
		Positions []uint
		States    []MatchState
	}

	// Build up the elements for the filter form.
	phases, err := gorm.G[GamePhase](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving scorecard", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	fields, err := gorm.G[Field](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving scorecard", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	positions, err := gorm.G[FieldPosition](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving scorecard", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	// Work out what from the form was selected
	r.ParseForm()
	filter := filterParams{States: MatchStates}
	for _, phase := range phases {
		filter.Phases = append(filter.Phases, phase.ID)
	}
	for _, field := range fields {
		filter.Fields = append(filter.Fields, field.ID)
	}
	for _, position := range positions {
		filter.Positions = append(filter.Positions, position.ID)
	}

ParseFilter:
	switch r.FormValue("filter_action") {
	case "clear":
		http.SetCookie(w, &http.Cookie{
			Name:    "scorecard_filters",
			Value:   "",
			Expires: time.Now(),
			MaxAge:  0,
		})
	case "save":
		filter.Phases = m.ws.ParseUintSlice(r.Form["phase_id"])
		filter.Fields = m.ws.ParseUintSlice(r.Form["field_id"])
		filter.Positions = m.ws.ParseUintSlice(r.Form["position_id"])

		filter.States = []MatchState{}
		for _, state := range m.ws.ParseUintSlice(r.Form["state"]) {
			filter.States = append(filter.States, MatchState(state))
		}

		j, _ := json.Marshal(filter)
		http.SetCookie(w, &http.Cookie{
			Name:    "scorecard_filters",
			Value:   url.QueryEscape(string(j)),
			Expires: time.Now().Add(time.Hour * 24),
		})
	default:
		// Try to recover the filters from the cookie, if it exists.
		c, err := r.Cookie("scorecard_filters")
		if err == http.ErrNoCookie {
			break ParseFilter
		}
		v, _ := url.QueryUnescape(c.Value)
		if err := json.Unmarshal([]byte(v), &filter); err != nil {
			slog.Warn("Invalid but present filter cookie", "error", err)
		}
	}

	slog.Debug("Filter configuration", "filter", filter)

	// Finally select all MatchPlacements that match the given
	// filters, which in turn results in a list of scorecards
	// matching those match placements.
	placements, err := gorm.G[MatchPlacement](m.db.DB).
		Preload("Phase", nil).
		Preload("Team", nil).
		Preload("Field", nil).
		Preload("Position", nil).
		Where("phase_id in ?", filter.Phases).
		Where("field_id in ?", filter.Fields).
		Where("position_id in ?", filter.Positions).
		Where("state in ?", filter.States).
		Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving scorecard", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	ctx := pongo2.Context{
		"available_phases":    phases,
		"available_fields":    fields,
		"available_positions": positions,
		"available_states":    MatchStates,
		"filter":              filter,
		"placements":          placements,
	}

	m.ws.DoTemplate(w, r, "views/game/scorecard_list.p2", ctx)
}

func (m *Module) uiViewScorecard(w http.ResponseWriter, r *http.Request) {
	placementFilter := MatchPlacement{
		PhaseID:    m.ws.StrToUint(chi.URLParam(r, "phase")),
		Match:      int(m.ws.StrToUint(chi.URLParam(r, "match"))),
		FieldID:    m.ws.StrToUint(chi.URLParam(r, "field")),
		PositionID: m.ws.StrToUint(chi.URLParam(r, "position")),
	}
	placement, err := gorm.G[MatchPlacement](m.db.DB).
		Preload("Phase", nil).
		Preload("Team", nil).
		Preload("Field", nil).
		Preload("Position", nil).
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

	sce, err := gorm.G[ScorecardValue](m.db.DB).
		Where(&ScorecardValue{MatchPlacementID: placement.ID}).Find(r.Context())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		slog.Error("Error retreiving scorecard elements", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	scd := make(map[string]int)
	for _, e := range sce {
		scd[e.Element] = e.Value
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
		_, err := gorm.G[ScorecardValue](tx).
			Where(&ScorecardValue{MatchPlacementID: placement.ID}).
			Delete(r.Context())
		if err != nil {
			return err
		}

		_, err = gorm.G[MatchScore](tx).
			Where(&MatchScore{ID: placement.ID}).
			Delete(r.Context())
		if err != nil {
			return err
		}

		vMap := make(map[string]int)
		for key := range r.Form {
			slog.Debug("form value", "key", key, "value", r.FormValue(key))

			v, err := strconv.Atoi(r.FormValue(key))
			if err != nil {
				slog.Error("Error retreiving scorecard", "error", err)
				m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
				return err
			}
			scv := ScorecardValue{
				MatchPlacementID: placement.ID,
				Element:          key,
				Value:            v,
			}
			vMap[key] = v

			if err := gorm.G[ScorecardValue](tx).Create(r.Context(), &scv); err != nil {
				slog.Error("Error saving scorecard value", "error", err)
				return err
			}
		}

		elements, err := gorm.G[ScorecardElement](tx).Find(r.Context())
		if err != nil {
			slog.Error("Error fetching scorecard elements", "error", err)
			return err
		}
		exprFragments := make([]string, len(elements))
		for i := range elements {
			exprFragments[i] = elements[i].Expr
		}

		score, err := expr.Eval(strings.Join(exprFragments, " + "), vMap)
		if err != nil {
			slog.Error("Error evaluating scorecard", "error", err)
			return err
		}
		err = gorm.G[MatchScore](tx).Create(r.Context(), &MatchScore{
			ID:               placement.ID,
			MatchPlacementID: placement.ID,
			GamePhaseID:      placement.PhaseID,
			TeamID:           placement.TeamID,
			Score:            score.(int),
		})
		if err != nil {
			slog.Error("Error saving match score", "error", err)
			return err
		}

		_, err = gorm.G[MatchPlacement](tx).
			Where(&MatchPlacement{ID: placement.ID}).
			Update(r.Context(), "state", MatchStateComplete)
		if err != nil {
			slog.Error("Error finalizing match", "error", err)
			return err
		}

		slog.Info("Scorecard Evaluated", "score", score)
		return nil
	})

	http.Redirect(w, r, "../../../../scorecard", http.StatusSeeOther)
}
