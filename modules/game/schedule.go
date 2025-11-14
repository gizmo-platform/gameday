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
	"gorm.io/gorm/clause"

	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/schedgen"
)

func (m *Module) uiViewPhaseList(w http.ResponseWriter, r *http.Request) {
	phases := []GamePhase{}

	if res := m.db.Find(&phases); res.Error != nil {
		slog.Error("Error retreiving field positions", "error", res.Error)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
	}

	scheduleAvailable := make(map[uint]bool)
	for _, phase := range phases {
		tmp := []MatchPlacement{}
		res := m.db.Where(&MatchPlacement{PhaseID: phase.ID}).Find(&tmp)
		scheduleAvailable[phase.ID] = (len(tmp) > 0) && (res.Error == nil)
	}

	ctx := pongo2.Context{
		"phases":   phases,
		"schedule": scheduleAvailable,
	}

	m.ws.DoTemplate(w, r, "views/game/phases.p2", ctx)
}

func (m *Module) uiViewPhaseSchedule(w http.ResponseWriter, r *http.Request) {
	gPhase := m.ws.StrToUint(chi.URLParam(r, "id"))

	fields := []Field{}
	if res := m.db.Where("id in (select distinct(field_id) from match_placements where phase_id = ?)", gPhase).Find(&fields); res.Error != nil {
	}
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].ID < fields[j].ID
	})

	positions := []FieldPosition{}
	if res := m.db.Find(&positions); res.Error != nil {
		slog.Error("Error loading positions", "error", res.Error)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
	}

	placements := []MatchPlacement{}
	if res := m.db.Preload(clause.Associations).Where(&MatchPlacement{PhaseID: gPhase}).Find(&placements); res.Error != nil {
		slog.Error("Error retreiving match placements", "error", res.Error)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
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

	phase := GamePhase{}
	if res := m.db.Where(&GamePhase{ID: gPhase}).Find(&phase); res.Error != nil {
		slog.Error("Error retreiving phase", "error", res.Error)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
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

func (m *Module) uiViewPhaseScheduleSelectTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := m.tm.ListTeams(team.Team{})
	if err != nil {
		slog.Error("Error retreiving field positions", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	fields := []Field{}
	if res := m.db.Find(&fields); res.Error != nil {
		slog.Error("Error loading fields", "error", res.Error)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
	}

	ctx := pongo2.Context{
		"teams":        teams,
		"fields":       fields,
		"scheduletype": r.URL.Query().Get("st"),
	}

	m.ws.DoTemplate(w, r, "views/game/schedule_select_teams.p2", ctx)
}

func (m *Module) uiViewPhaseSchedulePreview(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	fields := []Field{}
	for _, f := range r.Form["fields"] {
		field := Field{}
		if res := m.db.Where(&Field{ID: m.ws.StrToUint(f)}).Find(&field); res.Error != nil {
			continue
		}
		fields = append(fields, field)
	}
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].ID < fields[j].ID
	})

	positions := []FieldPosition{}
	if res := m.db.Find(&positions); res.Error != nil {
		slog.Error("Error loading positions", "error", res.Error)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
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
	}

	teams := []team.Team{}
	for _, t := range r.Form["selected_teams"] {
		team, err := m.tm.ListTeams(team.Team{ID: m.ws.StrToUint(t)})
		if err != nil {
			slog.Error("Error loading team", "error", err)
			continue
		}
		teams = append(teams, team...)
	}
	sort.Slice(teams, func(i, j int) bool {
		return teams[i].ID < teams[j].ID
	})

	if res := m.db.Where(&MatchPlacement{PhaseID: CandidatePhase}).Delete(&MatchPlacement{}); res.Error != nil {
		slog.Error("Error clearing candidate match", "error", res.Error)
	}

	for rNum, round := range s.Rounds {
		for mNum, match := range round.Matches {
			for _, field := range fields {
				for _, position := range positions {
					tID := match.Team(int(field.ID), int(position.ID)) - 1
					if tID < 1 {
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
					}

					if res := m.db.Save(&mp); res.Error != nil {
						slog.Error("Error saving match placement", "error", res.Error)
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

	res := m.db.Model(&MatchPlacement{}).Where(&MatchPlacement{PhaseID: CandidatePhase}).Update("PhaseID", gPhase)
	if res.Error != nil {
		slog.Error("Error updating schedule phase", "error", res.Error)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
		return
	}

	http.Redirect(w, r, path.Join(m.basePath, "schedule"), http.StatusSeeOther)
}
