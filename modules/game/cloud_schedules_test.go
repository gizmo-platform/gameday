package game

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/team"
)

// newCloudScheduleModule builds a cloud-scoped module with the phase
// names a cloud's game design would hold.
func newCloudScheduleModule(t *testing.T) *Module {
	t.Helper()
	m, d := newTestModule(t)
	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})
	for _, p := range []GamePhase{{ID: 1, Name: "Seeding"}, {ID: 3, Name: "Semifinals"}} {
		if err := d.Create(&p).Error; err != nil {
			t.Fatalf("create phase: %v", err)
		}
	}
	return cm
}

// seedContestName records a contest name the facet and columns use.
func seedContestName(t *testing.T, m *Module, id uint, name string) {
	t.Helper()
	if err := m.db.AutoMigrate(&contestNameRow{}); err != nil {
		t.Fatalf("migrate contests: %v", err)
	}
	if err := m.db.Create(&contestNameRow{ID: id, Name: name}).Error; err != nil {
		t.Fatalf("create contest: %v", err)
	}
}

func seedPlacements(t *testing.T, m *Module, rows ...CloudMatchPlacement) {
	t.Helper()
	for _, p := range rows {
		seedCloudPlacement(t, m.db.DB, p)
	}
}

// TestCloudSchedulesList verifies the list shows one row per contest
// and phase with match and team counts, is reachable without logging
// in, and filters by contest.
func TestCloudSchedulesList(t *testing.T) {
	m := newCloudScheduleModule(t)
	seedContestName(t, m, 7, "Spring Fling")
	seedPlacements(t, m,
		// Contest 7, Seeding: 2 matches, 3 teams.
		CloudMatchPlacement{ContestID: 7, PhaseID: 1, Match: 1, FieldID: 1, PositionID: 1, TeamID: 101, TeamName: "Alpha"},
		CloudMatchPlacement{ContestID: 7, PhaseID: 1, Match: 1, FieldID: 1, PositionID: 2, TeamID: 102, TeamName: "Beta"},
		CloudMatchPlacement{ContestID: 7, PhaseID: 1, Match: 2, FieldID: 1, PositionID: 1, TeamID: 103, TeamName: "Gamma"},
		// Contest 7, Semifinals: 1 match, 1 team.
		CloudMatchPlacement{ContestID: 7, PhaseID: 3, Match: 1, FieldID: 1, PositionID: 1, TeamID: 101, TeamName: "Alpha"},
		// Contest 9 (no name recorded), Seeding.
		CloudMatchPlacement{ContestID: 9, PhaseID: 1, Match: 1, FieldID: 1, PositionID: 1, TeamID: 201, TeamName: "Delta"},
	)

	code, body, _ := routerReq(t, m, nil, http.MethodGet, "/ui/mod/game/schedules", "")
	if code != http.StatusOK {
		t.Fatalf("schedules (anonymous) = %d, want 200: %s", code, body)
	}
	assertContains(t, body,
		"3 schedules",
		`href="/ui/mod/game/schedules/7/1"`,
		`href="/ui/mod/game/schedules/7/3"`,
		`href="/ui/mod/game/schedules/9/1"`,
		"<td>Spring Fling</td>\n          <td>Seeding</td>\n          <td>2</td>\n          <td>3</td>",
		"<td>Spring Fling</td>\n          <td>Semifinals</td>",
		"<td>Contest 9</td>",
		// The facet lists both contests.
		`<option value="7">Spring Fling</option>`,
		`<option value="9">Contest 9</option>`,
	)

	_, body, _ = routerReq(t, m, nil, http.MethodGet, "/ui/mod/game/schedules?contest_id=7", "")
	assertContains(t, body, "2 schedules", `href="/ui/mod/game/schedules/7/1"`, `<option value="7" selected>Spring Fling</option>`)
	assertNotContains(t, body, `href="/ui/mod/game/schedules/9/1"`)
}

func TestCloudSchedulesEmpty(t *testing.T) {
	m := newCloudScheduleModule(t)
	_, body, _ := routerReq(t, m, nil, http.MethodGet, "/ui/mod/game/schedules", "")
	assertContains(t, body, "0 schedules", "No schedules have been pushed.")
}

// TestCloudSchedulesPagination verifies paging over the grouped
// schedules keeps the contest filter in the pager links.
func TestCloudSchedulesPagination(t *testing.T) {
	m := newCloudScheduleModule(t)
	for phase := uint(1); phase <= 12; phase++ {
		seedPlacements(t, m, CloudMatchPlacement{ContestID: 4, PhaseID: phase, Match: 1, FieldID: 1, PositionID: 1, TeamID: 1, TeamName: "Solo"})
	}
	seedPlacements(t, m, CloudMatchPlacement{ContestID: 5, PhaseID: 1, Match: 1, FieldID: 1, PositionID: 1, TeamID: 2, TeamName: "Other"})

	_, body, _ := routerReq(t, m, nil, http.MethodGet, "/ui/mod/game/schedules?contest_id=4&page_size=5&page=2", "")
	assertContains(t, body,
		"12 schedules",
		`href="/ui/mod/game/schedules/4/6"`,
		`href="/ui/mod/game/schedules/4/10"`,
		`aria-current="page">2</a>`,
		"contest_id=4&amp;page_size=5&page=3",
	)
	assertNotContains(t, body, `href="/ui/mod/game/schedules/4/5"`, `href="/ui/mod/game/schedules/4/11"`, `/schedules/5/1"`)
}

// TestCloudScheduleDetail verifies a pushed schedule renders through
// the onsite schedule template with pushed field names, the cloud's
// position names and colours, a placeholder column for an unknown
// position, one-based rounds, and matches in order.
func TestCloudScheduleDetail(t *testing.T) {
	m := newCloudScheduleModule(t)
	seedContestName(t, m, 7, "Spring Fling")
	for _, p := range []FieldPosition{{ID: 1, Name: "red", Color1: "ec5e5e", Color2: "f4a2a2"}, {ID: 2, Name: "blue", Color1: "5e8bec", Color2: "a2b9f4"}} {
		if err := m.db.Create(&p).Error; err != nil {
			t.Fatalf("create position: %v", err)
		}
	}
	// Match 2 is inserted first; it must still render second.
	seedPlacements(t, m,
		CloudMatchPlacement{ContestID: 7, PhaseID: 1, Round: 1, Match: 2, FieldID: 2, FieldName: "South", PositionID: 9, TeamID: 103, TeamName: "Gamma", State: uint(MatchStateScheduled)},
		CloudMatchPlacement{ContestID: 7, PhaseID: 1, Round: 0, Match: 1, FieldID: 1, FieldName: "North", PositionID: 1, TeamID: 101, TeamName: "Alpha", State: uint(MatchStateComplete)},
		CloudMatchPlacement{ContestID: 7, PhaseID: 1, Round: 0, Match: 1, FieldID: 1, FieldName: "North", PositionID: 2, TeamID: 102, TeamName: "Beta", State: uint(MatchStateComplete)},
	)

	code, body, _ := routerReq(t, m, nil, http.MethodGet, "/ui/mod/game/schedules/7/1", "")
	if code != http.StatusOK {
		t.Fatalf("schedule (anonymous) = %d, want 200: %s", code, body)
	}
	assertContains(t, body,
		"Phase Schedule - Seeding",
		`<p class="subtitle">Spring Fling</p>`,
		`<th colspan="3">North</th>`,
		`<th colspan="3">South</th>`,
		"<th>Red</th>", "<th>Blue</th>", "<th>Position 9</th>",
		// The template shades odd matches with Color2, even with Color1.
		"background-color: #f4a2a2; color: black;\">Alpha</td>",
		"background-color: #a2b9f4; color: black;\">Beta</td>",
		"background-color: #cccccc; color: black;\">Gamma</td>",
		"<td>1</td>\n              <td>1</td>",
		"<td>2</td>\n              <td>2</td>",
	)
	// Coloured cells always use black text so they stay readable in
	// dark mode, where Bulma lightens table text.
	if colored, black := strings.Count(body, "background-color: #"), strings.Count(body, "; color: black;"); colored == 0 || colored != black {
		t.Errorf("%d coloured cells but %d with black text", colored, black)
	}
	// Two fields: the schedule prints on landscape pages.
	assertContains(t, body, "gameday-schedule schedule-wide", `<table class="table schedule-table">`, `<col class="schedule-rm" />`)
	if strings.Index(body, ">Alpha<") > strings.Index(body, ">Gamma<") {
		t.Error("match 2 rendered before match 1")
	}
}

func TestCloudScheduleDetailMissing(t *testing.T) {
	m := newCloudScheduleModule(t)
	_, body, _ := routerReq(t, m, nil, http.MethodGet, "/ui/mod/game/schedules/7/1", "")
	assertContains(t, body, "Internal Error", "no schedule has been pushed for contest 7 phase 1")
}

// TestCloudSchedulesOnsiteUnregistered verifies the schedules pages
// exist only on the cloud.
func TestCloudSchedulesOnsiteUnregistered(t *testing.T) {
	m, _ := newTestModule(t)
	for _, path := range []string{"/ui/mod/game/schedules", "/ui/mod/game/schedules/7/1"} {
		if code, _, _ := routerReq(t, m, adminContext(), http.MethodGet, path, ""); code != http.StatusNotFound {
			t.Errorf("onsite %s = %d, want 404", path, code)
		}
	}
}

// TestBuildScheduleRows verifies rows come out in match order whatever
// the input order, with one-based rounds, "<field>-<position>" keys,
// and Completed only when every slot is terminal.
func TestBuildScheduleRows(t *testing.T) {
	rows := buildScheduleRows([]scheduleSlot{
		{Round: 1, Match: 3, FieldID: 1, PositionID: 1, Team: team.Team{Name: "C"}, State: MatchStateScheduled},
		{Round: 0, Match: 1, FieldID: 1, PositionID: 2, Team: team.Team{Name: "B"}, State: MatchStateNoShow},
		{Round: 0, Match: 1, FieldID: 1, PositionID: 1, Team: team.Team{Name: "A"}, State: MatchStateComplete},
		{Round: 0, Match: 2, FieldID: 2, PositionID: 1, Team: team.Team{Name: "D"}, State: MatchStateComplete},
		{Round: 0, Match: 2, FieldID: 2, PositionID: 2, Team: team.Team{Name: "E"}, State: MatchStateRunning},
	})

	got := []string{}
	for _, r := range rows {
		got = append(got, fmt.Sprintf("r%d m%d %v %d", r.Round, r.Match, r.Completed, len(r.Placements)))
	}
	want := "r1 m1 true 2,r1 m2 false 2,r2 m3 false 1"
	if strings.Join(got, ",") != want {
		t.Errorf("rows = %v, want %s", got, want)
	}
	if rows[0].Placements["1-1"].Name != "A" || rows[0].Placements["1-2"].Name != "B" || rows[1].Placements["2-2"].Name != "E" {
		t.Errorf("placements keyed wrong: %+v", rows)
	}
}

func assertContains(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, s := range want {
		if !strings.Contains(body, s) {
			t.Errorf("body missing %q", s)
		}
	}
}

func assertNotContains(t *testing.T, body string, unwanted ...string) {
	t.Helper()
	for _, s := range unwanted {
		if strings.Contains(body, s) {
			t.Errorf("body unexpectedly contains %q", s)
		}
	}
}

// TestPhaseScheduleOnsiteOrder verifies the onsite schedule page,
// now built by buildScheduleRows, renders matches in order with
// one-based rounds even when placements were stored out of order.
func TestPhaseScheduleOnsiteOrder(t *testing.T) {
	m, d := newTestModule(t)
	teams := seedTeams(t, d, 3)
	pos := seedFieldPosition(t, d, "red")

	phase := GamePhase{Name: "seeding", ScoreSummation: "Total"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	for _, mp := range []MatchPlacement{
		{Round: 1, Match: 3, TeamID: teams[2].ID},
		{Round: 0, Match: 1, TeamID: teams[0].ID},
		{Round: 0, Match: 2, TeamID: teams[1].ID},
	} {
		mp.PhaseID, mp.FieldID, mp.PositionID = phase.ID, 1, pos.ID
		if err := d.Create(&mp).Error; err != nil {
			t.Fatalf("create placement: %v", err)
		}
	}

	_, body, _ := routerReq(t, m, adminContext(), http.MethodGet, fmt.Sprintf("/ui/mod/game/schedule/phases/%d", phase.ID), "")
	a, b, c := strings.Index(body, ">"+teams[0].Name+"<"), strings.Index(body, ">"+teams[1].Name+"<"), strings.Index(body, ">"+teams[2].Name+"<")
	if a < 0 || b < 0 || c < 0 || !(a < b && b < c) {
		t.Errorf("matches out of order (positions %d, %d, %d)", a, b, c)
	}
	assertContains(t, body, "<td>1</td>\n              <td>1</td>", "<td>1</td>\n              <td>2</td>", "<td>2</td>\n              <td>3</td>")
	// One field: the schedule keeps portrait pages.
	assertContains(t, body, `gameday-schedule">`)
	assertNotContains(t, body, "schedule-wide")
}
