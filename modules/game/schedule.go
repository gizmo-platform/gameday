package game

import (
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"sort"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/schedgen"
)

func (m *Module) uiViewPhaseList(w http.ResponseWriter, r *http.Request) {
	phases, err := gorm.G[GamePhase](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving field positions", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	scheduleAvailable := make(map[uint]bool)
	phaseComplete := make(map[uint]bool)
	for _, phase := range phases {
		tmp, err := gorm.G[MatchPlacement](m.db.DB).
			Where("phase_id = ?", phase.ID).
			Find(r.Context())
		scheduleAvailable[phase.ID] = (len(tmp) > 0) && (err == nil)

		completed, err1 := gorm.G[MatchPlacement](m.db.DB).
			Where(&MatchPlacement{PhaseID: phase.ID}).
			Where("state in (?)", []MatchState{
				MatchStateComplete,
				MatchStateNoShow,
				MatchStateDisqualified,
			}).
			Count(r.Context(), "*")
		count, err2 := gorm.G[MatchPlacement](m.db.DB).
			Where(&MatchPlacement{PhaseID: phase.ID}).
			Where("state not in (?)", []MatchState{
				MatchStateComplete,
				MatchStateNoShow,
				MatchStateDisqualified,
			}).
			Count(r.Context(), "*")
		phaseComplete[phase.ID] = (count == 0) && (completed > 0) && (err1 == nil) && (err2 == nil)
		slog.Debug("Phase completion state", "phase_id", phase.ID, "playable", count, "completed", completed)
	}

	// This has to be a second pass to ensure that all the phasing
	// information is populated.  This could be pushed down into
	// the database by a more skilled programmer.
	canSchedule := make(map[uint]bool)
	for _, phase := range phases {
		can := true

		filters, err := gorm.G[GamePhaseAdvancementFilter](m.db.DB).
			Where(&GamePhaseAdvancementFilter{GamePhaseID: phase.ID}).
			Find(r.Context())
		if err != nil {
			slog.Debug("Could not load advancement filters for phase", "phase", phase.ID, "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return
		}
		for _, filter := range filters {
			slog.Debug("Evaluating filter satisfaction",
				"phase", phase.Name,
				"rule", filter.Rule,
				"source_id", filter.SelectFrom,
				"source_complete", phaseComplete[filter.SelectFrom],
				"source_frozen", phases[filter.SelectFrom-1].Frozen,
			)
			can = can && phaseComplete[filter.SelectFrom] && phases[filter.SelectFrom-1].Frozen
		}

		canSchedule[phase.ID] = can
	}

	ctx := pongo2.Context{
		"phases":      phases,
		"completed":   phaseComplete,
		"schedule":    scheduleAvailable,
		"canSchedule": canSchedule,
	}

	m.ws.DoTemplate(w, r, "views/game/phases.p2", ctx)
}

func (m *Module) uiViewPhaseSchedule(w http.ResponseWriter, r *http.Request) {
	gPhase := m.ws.StrToUint(chi.URLParam(r, "id"))

	fields, err := gorm.G[Field](m.db.DB).
		Where("id in (select distinct(field_id) from match_placements where phase_id = ?)", gPhase).
		Find(r.Context())
	if err != nil {
		slog.Error("Error loading fields", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].ID < fields[j].ID
	})

	positions, err := gorm.G[FieldPosition](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error loading positions", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	placements, err := gorm.G[MatchPlacement](m.db.DB).
		Preload("Team", nil).
		Preload("Field", nil).
		Preload("Position", nil).
		Where(&MatchPlacement{PhaseID: gPhase}).
		Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving match placements", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	type scheduleRow struct {
		Round      int
		Match      int
		Placements map[string]team.Team
	}

	schedule := []scheduleRow{}

	round := 1
	match := 1
	sr := scheduleRow{
		Round:      round,
		Match:      match,
		Placements: make(map[string]team.Team),
	}
	for _, p := range placements {
		if p.Match != match {
			schedule = append(schedule, sr)
			round = p.Round
			match = p.Match
			sr = scheduleRow{
				Round:      round + 1,
				Match:      match,
				Placements: make(map[string]team.Team),
			}
		}
		sr.Placements[fmt.Sprintf("%d-%d", p.FieldID, p.PositionID)] = p.Team
	}
	if len(sr.Placements) > 0 {
		schedule = append(schedule, sr)
	}

	phase, err := gorm.G[GamePhase](m.db.DB).
		Where(&GamePhase{ID: gPhase}).
		First(r.Context())
	if err != nil {
		slog.Error("Error retreiving phase", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	ctx := pongo2.Context{
		"phase":     phase,
		"fields":    fields,
		"positions": positions,
		"schedule":  schedule,
	}

	switch r.URL.Query().Get("format") {
	case "csv":
		cw := csv.NewWriter(w)

		for _, row := range schedule {
			cFields := []string{}
			for _, field := range fields {
				for _, position := range positions {
					cFields = append(cFields, row.Placements[fmt.Sprintf("%d-%d", field.ID, position.ID)].Name)
				}
			}
			slog.Debug("Schedule CSV", "row", cFields)
			if err := cw.Write(cFields); err != nil {
				slog.Error("Error writing CSV", "error", err)
			}
		}
		cw.Flush()

	default:
		m.ws.DoTemplate(w, r, "views/game/schedule.p2", ctx)
	}
}

func (m *Module) uiViewPhaseMakeActive(w http.ResponseWriter, r *http.Request) {
	gPhase := m.ws.StrToUint(chi.URLParam(r, "id"))

	m.db.Transaction(func(tx *gorm.DB) error {
		_, err := gorm.G[GamePhase](tx).
			Where(&GamePhase{Active: true}).
			Update(r.Context(), "Active", false)
		if err != nil {
			slog.Error("Error deactivating all schedule phases", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return err
		}

		_, err = gorm.G[GamePhase](tx).
			Where(&GamePhase{ID: gPhase}).
			Update(r.Context(), "Active", true)
		if err != nil {
			slog.Error("Error activating schedule phase", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return err
		}

		return nil
	})
	http.Redirect(w, r, path.Join(m.basePath, "schedule"), http.StatusSeeOther)
}

func (m *Module) uiViewPhaseMakeFrozen(w http.ResponseWriter, r *http.Request) {
	gPhase := m.ws.StrToUint(chi.URLParam(r, "id"))

	_, err := gorm.G[GamePhase](m.db.DB).
		Where(&GamePhase{ID: gPhase}).
		Update(r.Context(), "Frozen", true)
	if err != nil {
		slog.Error("Error freezing schedule phase", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	http.Redirect(w, r, path.Join(m.basePath, "schedule"), http.StatusSeeOther)
}

func (m *Module) uiViewPhaseScheduleSelectTeams(w http.ResponseWriter, r *http.Request) {
	gPhase := m.ws.StrToUint(chi.URLParam(r, "id"))
	phase, err := gorm.G[GamePhase](m.db.DB).
		Preload("AdvancementFilters", nil).
		Where(&GamePhase{ID: gPhase}).
		First(r.Context())
	if err != nil {
		slog.Error("Error loading phase for schedule parameter selection", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	slog.Debug("Selected game phase", "phase", phase)

	teams, err := m.tm.ListTeams(r.Context(), team.Team{})
	if err != nil {
		slog.Error("Error retreiving field positions", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	fields, err := gorm.G[Field](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error loading fields", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	ctx := pongo2.Context{
		"phase":         phase,
		"teams":         teams,
		"manualEnabled": true,
		"fields":        fields,
		"scheduletype":  r.URL.Query().Get("st"),
	}

	advancingTeams := make(map[uint]struct{})
	if len(phase.AdvancementFilters) > 0 {
		ctx["manualEnabled"] = false

		sctx := AdvancementFilterContext{
			Roster:     make(map[uint]team.Team),
			Candidates: make(map[uint]team.Team),
		}
		for _, team := range teams {
			sctx.Roster[team.ID] = team
		}

		for _, filter := range phase.AdvancementFilters {
			rowData, err := m.scoreboardRankings(r.Context(), filter.SelectFrom)
			if err != nil {
				slog.Error("Error retrieving filter scoreboard", "filter", filter)
				m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
				return
			}
			sctx.Scoreboard = rowData

			f, exists := filters[filter.Filter]
			if !exists {
				slog.Error("Tried to load unregistered filter", "filter", filter)
				m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
				return
			}
			f.Apply(&sctx, filter.Rule, filter.Mode, filter.SliceExpr)
		}

		// After all filters have been applied, the remaining
		// candidates advance.
		for _, t := range sctx.Candidates {
			advancingTeams[t.ID] = struct{}{}
		}
		ctx["advancingTeams"] = advancingTeams
		ctx["advancementReasons"] = sctx.Determinations
	}
	if r.URL.Query().Get("override") != "" {
		ctx["manualEnabled"] = false
	}

	m.ws.DoTemplate(w, r, "views/game/schedule_select_teams.p2", ctx)
}

func (m *Module) uiViewPhaseSchedulePreview(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	fields := []Field{}
	for _, f := range r.Form["fields"] {
		field, err := gorm.G[Field](m.db.DB).
			Where(&Field{ID: m.ws.StrToUint(f)}).
			First(r.Context())
		if err != nil {
			continue
		}
		fields = append(fields, field)
	}
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].ID < fields[j].ID
	})

	positions, err := gorm.G[FieldPosition](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error loading positions", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	var s *schedgen.Schedule

	switch r.FormValue("schedule_type") {
	case schedgen.TypeRandomSeeding:
		c := schedgen.Config{
			Fields:    len(r.Form["fields"]),
			Positions: len(positions),
			Teams:     len(r.Form["selected_teams"]),
			Rounds:    int(m.ws.StrToUint(r.FormValue("rounds"))),
		}

		s = schedgen.Generate(c)
		s.Score()
		if err := s.Validate(); err != nil {
			slog.Error("Error generating schedule", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return
		}
	}

	teams := []team.Team{}
	for _, t := range r.Form["selected_teams"] {
		team, err := m.tm.ListTeams(r.Context(), team.Team{ID: m.ws.StrToUint(t)})
		if err != nil {
			slog.Error("Error loading team", "error", err)
			continue
		}
		teams = append(teams, team...)
	}
	sort.Slice(teams, func(i, j int) bool {
		return teams[i].ID < teams[j].ID
	})

	if _, err := gorm.G[MatchPlacement](m.db.DB).Where(&MatchPlacement{PhaseID: CandidatePhase}).Delete(r.Context()); err != nil {
		slog.Error("Error clearing candidate match", "error", err)
	}

	for rNum, round := range s.Rounds {
		for mNum, match := range round.Matches {
			for fNum, field := range fields {
				for pNum, position := range positions {
					tID := match.Team(fNum, pNum)
					if tID < 0 {
						continue
					}
					t := teams[tID]
					mp := MatchPlacement{
						Round:      rNum,
						Match:      mNum + rNum*len(round.Matches) + 1,
						PhaseID:    CandidatePhase,
						TeamID:     t.ID,
						FieldID:    field.ID,
						PositionID: position.ID,
						State:      MatchStateScheduled,
					}

					if err := gorm.G[MatchPlacement](m.db.DB).Create(r.Context(), &mp); err != nil {
						slog.Error("Error saving match placement", "error", err)
						continue
					}
				}
			}
		}
	}

	ctx := pongo2.Context{
		"schedule":  s,
		"teams":     teams,
		"fields":    fields,
		"positions": positions,
	}

	m.ws.DoTemplate(w, r, "views/game/schedule_preview.p2", ctx)
}

func (m *Module) uiViewPhaseScheduleAccept(w http.ResponseWriter, r *http.Request) {
	gPhase := m.ws.StrToUint(chi.URLParam(r, "id"))

	m.db.Transaction(func(tx *gorm.DB) error {
		_, err := gorm.G[MatchPlacement](tx).
			Where(&MatchPlacement{PhaseID: CandidatePhase}).
			Update(r.Context(), "PhaseID", gPhase)
		if err != nil {
			slog.Error("Error updating schedule phase", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return err
		}

		_, err = gorm.G[GamePhase](tx).
			Where(&GamePhase{Active: true}).
			Update(r.Context(), "Active", false)
		if err != nil {
			slog.Error("Error deactivating all schedule phases", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return err
		}

		_, err = gorm.G[GamePhase](tx).
			Where(&GamePhase{ID: gPhase}).
			Update(r.Context(), "Active", true)
		if err != nil {
			slog.Error("Error activating schedule phase", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return err
		}

		return nil
	})

	http.Redirect(w, r, path.Join(m.basePath, "schedule"), http.StatusSeeOther)
}
