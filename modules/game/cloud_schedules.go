package game

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/web"
)

// cloudScheduleGroup is one pushed schedule: the placements of one
// phase within one contest, summarized for the schedules list.
type cloudScheduleGroup struct {
	ContestID uint
	PhaseID   uint
	Matches   int
	Teams     int
}

// cloudScheduleRow is one row on the cloud schedules page.
type cloudScheduleRow struct {
	Group       cloudScheduleGroup
	ContestName string
	PhaseName   string
}

// uiViewCloudSchedules lists the pushed schedules, one row per contest
// and phase, optionally filtered to one contest.  It is public, like
// the scorecards page.  The groups are few, so they are paged in Go
// after grouping.
func (m *Module) uiViewCloudSchedules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	contestID := m.ws.StrToUint(r.URL.Query().Get("contest_id"))

	q := m.db.Raw().Model(&CloudMatchPlacement{}).
		Select("contest_id, phase_id, COUNT(DISTINCT match) AS matches, COUNT(DISTINCT team_id) AS teams")
	if contestID != 0 {
		q = q.Where("contest_id = ?", contestID)
	}
	var groups []cloudScheduleGroup
	if err := q.Group("contest_id, phase_id").Order("contest_id, phase_id").Scan(&groups).Error; err != nil {
		slog.Error("Error listing cloud schedules", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	total := len(groups)
	page, pageSize := web.PageParams(r)
	pages := (total + pageSize - 1) / pageSize
	start := min((page-1)*pageSize, total)
	end := min(start+pageSize, total)

	contestNames := m.cloudContestNames(ctx)
	phaseNames := m.cloudPhaseNames(ctx)
	rows := make([]cloudScheduleRow, 0, end-start)
	for _, g := range groups[start:end] {
		rows = append(rows, cloudScheduleRow{
			Group:       g,
			ContestName: contestName(contestNames, g.ContestID),
			PhaseName:   phaseName(phaseNames, g.PhaseID),
		})
	}

	// The filter form drops "page" (resets to page 1); pager links
	// re-append it.  Preserve the other query params via filtersQS.
	fq := r.URL.Query()
	fq.Del("page")

	m.ws.DoTemplate(w, r, "views/game/cloud_schedules.p2", pongo2.Context{
		"rows":       rows,
		"contests":   m.cloudContestOptions(ctx),
		"contestID":  contestID,
		"filtersQS":  fq.Encode(),
		"total":      total,
		"page":       page,
		"pages":      pages,
		"prevPage":   page - 1,
		"nextPage":   page + 1,
		"pageWindow": pageWindow(page, pages),
	})
}

// uiViewCloudSchedule renders one pushed schedule (a contest's phase)
// with the onsite schedule template.  Field names come from the push;
// phase and position names come from the cloud's own game design,
// whose IDs match the sites' because both come from the shared game
// file.  A position the cloud does not have gets a placeholder column
// so its teams are not dropped from the table.
func (m *Module) uiViewCloudSchedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	contestID := m.ws.StrToUint(chi.URLParam(r, "contest"))
	phaseID := m.ws.StrToUint(chi.URLParam(r, "phase"))

	placements, err := gorm.G[CloudMatchPlacement](m.db.DB).
		Where("contest_id = ? AND phase_id = ?", contestID, phaseID).
		Find(ctx)
	if err != nil {
		slog.Error("Error retrieving cloud schedule", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	if len(placements) == 0 {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{
			"error": fmt.Errorf("no schedule has been pushed for contest %d phase %d", contestID, phaseID),
		})
		return
	}

	known, err := gorm.G[FieldPosition](m.db.DB).Find(ctx)
	if err != nil {
		slog.Error("Error retrieving field positions", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	positions := make(map[uint]FieldPosition, len(known))
	for _, p := range known {
		positions[p.ID] = p
	}

	fields := make(map[uint]Field)
	slots := make([]scheduleSlot, 0, len(placements))
	for _, p := range placements {
		if _, ok := fields[p.FieldID]; !ok {
			fields[p.FieldID] = Field{ID: p.FieldID, Name: cloudFieldName(p)}
		}
		if _, ok := positions[p.PositionID]; !ok {
			positions[p.PositionID] = FieldPosition{
				ID:     p.PositionID,
				Name:   fmt.Sprintf("Position %d", p.PositionID),
				Color1: "cccccc",
				Color2: "cccccc",
			}
		}
		slots = append(slots, scheduleSlot{
			Round:      p.Round,
			Match:      p.Match,
			FieldID:    p.FieldID,
			PositionID: p.PositionID,
			Team:       team.Team{ID: p.TeamID, Name: p.TeamName},
			State:      MatchState(p.State),
		})
	}

	fieldList := make([]Field, 0, len(fields))
	for _, f := range fields {
		fieldList = append(fieldList, f)
	}
	sort.Slice(fieldList, func(i, j int) bool { return fieldList[i].ID < fieldList[j].ID })
	positionList := make([]FieldPosition, 0, len(positions))
	for _, p := range positions {
		positionList = append(positionList, p)
	}
	sort.Slice(positionList, func(i, j int) bool { return positionList[i].ID < positionList[j].ID })

	m.ws.DoTemplate(w, r, "views/game/schedule.p2", pongo2.Context{
		"phase":       GamePhase{ID: phaseID, Name: phaseName(m.cloudPhaseNames(ctx), phaseID)},
		"contestName": contestName(m.cloudContestNames(ctx), contestID),
		"fields":      fieldList,
		"positions":   positionList,
		"schedule":    buildScheduleRows(slots),
	})
}
