package team

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
)

// importCSV POSTs a CSV file to the bulk import endpoint as a multipart
// form, the way the browser form does.
func importCSV(t *testing.T, m *Module, csvText string) (int, string) {
	t.Helper()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("teams_file", "teams.csv")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte(csvText)); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/ui/mod/team/import", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req = req.WithContext(adminContext())

	mux := chi.NewRouter()
	mux.Mount("/ui/mod/team", m.Router())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func teamCount(t *testing.T, d *db.DB) int64 {
	t.Helper()
	var count int64
	if err := d.Model(&Team{}).Count(&count).Error; err != nil {
		t.Fatalf("count teams: %v", err)
	}
	return count
}

func fetchTeam(t *testing.T, d *db.DB, id uint) Team {
	t.Helper()
	tm, err := gorm.G[Team](d.DB).Where(&Team{ID: id}).First(context.Background())
	if err != nil {
		t.Fatalf("fetch team %d: %v", id, err)
	}
	return tm
}

func fetchTeamByNumber(t *testing.T, d *db.DB, number int) Team {
	t.Helper()
	tm, err := gorm.G[Team](d.DB).Where(&Team{Number: number}).First(context.Background())
	if err != nil {
		t.Fatalf("fetch team by number %d: %v", number, err)
	}
	return tm
}

func TestImportTeamsIdempotent(t *testing.T) {
	m, d := newTestModule(t)
	csv := "Name,Number,Division,Region\nAlpha,1,Open,North\nBeta,2,Open,South\n"

	code, body := importCSV(t, m, csv)
	if code != http.StatusSeeOther {
		t.Fatalf("first import = %d, want 303: %s", code, body)
	}
	if count := teamCount(t, d); count != 2 {
		t.Fatalf("after first import = %d teams, want 2", count)
	}

	// Re-import with a renamed team: rows must update in place, keyed
	// on team number, not duplicate.
	csv2 := "Name,Number,Division,Region\nAlpha Renamed,1,Open,North\nBeta,2,Open,South\n"
	code, body = importCSV(t, m, csv2)
	if code != http.StatusSeeOther {
		t.Fatalf("second import = %d, want 303: %s", code, body)
	}
	if count := teamCount(t, d); count != 2 {
		t.Fatalf("after re-import = %d teams, want 2", count)
	}

	renamed, err := gorm.G[Team](d.DB).Where(&Team{Number: 1}).First(context.Background())
	if err != nil {
		t.Fatalf("lookup team 1: %v", err)
	}
	if renamed.Name != "Alpha Renamed" {
		t.Errorf("re-imported name = %q, want Alpha Renamed", renamed.Name)
	}
}

func TestImportDoesNotResetDisabled(t *testing.T) {
	m, d := newTestModule(t)
	csv := "Name,Number,Division,Region\nAlpha,1,Open,North\n"

	if code, body := importCSV(t, m, csv); code != http.StatusSeeOther {
		t.Fatalf("first import = %d, want 303: %s", code, body)
	}

	tm := fetchTeamByNumber(t, d, 1)
	if err := d.Model(&Team{}).Where("id = ?", tm.ID).Update("disabled", true).Error; err != nil {
		t.Fatalf("disable team: %v", err)
	}

	// Re-import must update fields without resurrecting a
	// manually-disabled team: the import upsert only assigns
	// name/division/region and never touches disabled.
	if code, body := importCSV(t, m, csv); code != http.StatusSeeOther {
		t.Fatalf("re-import = %d, want 303: %s", code, body)
	}

	got := fetchTeam(t, d, tm.ID)
	if !got.Disabled {
		t.Error("re-import cleared the disabled flag")
	}
}

func TestListTeamsExcludesDisabled(t *testing.T) {
	m, d := newTestModule(t)

	enabled := Team{Name: "Enabled", Number: 1}
	disabled := Team{Name: "Disabled Team", Number: 2}
	if err := d.Create(&enabled).Error; err != nil {
		t.Fatalf("create enabled: %v", err)
	}
	if err := d.Create(&disabled).Error; err != nil {
		t.Fatalf("create disabled: %v", err)
	}
	if err := d.Model(&Team{}).Where("id = ?", disabled.ID).Update("disabled", true).Error; err != nil {
		t.Fatalf("disable team: %v", err)
	}

	roster, err := m.ListTeams(context.Background(), Team{})
	if err != nil {
		t.Fatalf("ListTeams roster: %v", err)
	}
	if len(roster) != 1 || roster[0].ID != enabled.ID {
		t.Fatalf("roster query returned %d teams, want only the enabled one: %+v", len(roster), roster)
	}

	byID, err := m.ListTeams(context.Background(), Team{ID: disabled.ID})
	if err != nil {
		t.Fatalf("ListTeams by ID: %v", err)
	}
	if len(byID) != 1 || !byID[0].Disabled {
		t.Errorf("specific ID lookup = %+v, want the disabled team", byID)
	}

	// The admin list page must still show disabled teams so they can
	// be re-enabled, with a visible marker.
	code, body := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/team/", "")
	if code != http.StatusOK {
		t.Fatalf("GET team list = %d, want 200", code)
	}
	for _, want := range []string{"Disabled Team", "Enabled", `class="tag is-dark">Disabled`} {
		if !strings.Contains(body, want) {
			t.Errorf("team list missing %q", want)
		}
	}
}

func TestUpsertTogglesDisabled(t *testing.T) {
	m, d := newTestModule(t)

	tm := Team{Name: "Toggle", Number: 5}
	if err := d.Create(&tm).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}

	open, err := gorm.G[Division](d.DB).Where(&Division{Name: "Open"}).First(context.Background())
	if err != nil {
		t.Fatalf("lookup Open division: %v", err)
	}
	base := "team_number=5&team_name=Toggle&team_division=" + strconv.FormatUint(uint64(open.ID), 10)

	// Save without the checkbox: flag must be clear.
	code, body := routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/team/1/edit", base)
	if code != http.StatusSeeOther {
		t.Fatalf("save (unchecked) = %d, want 303: %s", code, body)
	}
	if got := fetchTeam(t, d, tm.ID); got.Disabled {
		t.Error("save without checkbox left team disabled")
	}

	// Save with the checkbox: flag must be set.
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/team/1/edit", base+"&team_disabled=1")
	if code != http.StatusSeeOther {
		t.Fatalf("save (checked) = %d, want 303: %s", code, body)
	}
	if got := fetchTeam(t, d, tm.ID); !got.Disabled {
		t.Error("save with checkbox did not disable the team")
	}

	// Save again without the checkbox: flag must clear.
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/team/1/edit", base)
	if code != http.StatusSeeOther {
		t.Fatalf("save (uncheck) = %d, want 303: %s", code, body)
	}
	if got := fetchTeam(t, d, tm.ID); got.Disabled {
		t.Error("save without checkbox did not clear the disabled flag")
	}
}

func TestUpsertAddDisabled(t *testing.T) {
	m, d := newTestModule(t)

	open, err := gorm.G[Division](d.DB).Where(&Division{Name: "Open"}).First(context.Background())
	if err != nil {
		t.Fatalf("lookup Open division: %v", err)
	}
	base := "team_number=6&team_name=Fresh&team_division=" + strconv.FormatUint(uint64(open.ID), 10)

	// The add path shares the upsert handler; the checkbox must be
	// honored on create, not just on edit.
	code, body := routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/team/add", base+"&team_disabled=1")
	if code != http.StatusSeeOther {
		t.Fatalf("add = %d, want 303: %s", code, body)
	}
	tm := fetchTeamByNumber(t, d, 6)
	if !tm.Disabled {
		t.Error("team added with the checkbox checked was created enabled")
	}

	// An add without the checkbox must create an enabled team.
	code, body = routerReq(t, m, adminContext(), http.MethodPost, "/ui/mod/team/add", "team_number=7&team_name=Fresher&team_division="+strconv.FormatUint(uint64(open.ID), 10))
	if code != http.StatusSeeOther {
		t.Fatalf("add = %d, want 303: %s", code, body)
	}
	if tm := fetchTeamByNumber(t, d, 7); tm.Disabled {
		t.Error("team added without the checkbox was created disabled")
	}
}

// seedListFixture builds a division/region/team dataset to exercise the
// admin list's search, facet, and pagination behavior. It returns the
// created division IDs and region IDs.
func seedListFixture(t *testing.T, d *db.DB) (open, rookie Division, north, south, west Region) {
	t.Helper()

	var err error
	if open, err = gorm.G[Division](d.DB).Where(&Division{Name: "Open"}).First(context.Background()); err != nil {
		t.Fatalf("lookup Open: %v", err)
	}
	rookie = Division{Name: "Rookie"}
	if err := d.Create(&rookie).Error; err != nil {
		t.Fatalf("create Rookie: %v", err)
	}
	for _, r := range []Region{{Name: "North"}, {Name: "South"}, {Name: "West"}} {
		if err := d.Create(&r).Error; err != nil {
			t.Fatalf("create region %s: %v", r.Name, err)
		}
	}
	if north, err = firstRegion(d, "North"); err != nil {
		t.Fatal(err)
	}
	if south, err = firstRegion(d, "South"); err != nil {
		t.Fatal(err)
	}
	if west, err = firstRegion(d, "West"); err != nil {
		t.Fatal(err)
	}

	// 15 teams in Open (10 North, 5 South), 10 teams in Rookie
	// (5 North, 5 West), 25 total, numbered 1..25 in that order.
	for i := 1; i <= 25; i++ {
		div, reg := open, north
		switch {
		case i <= 10: // Open + North
		case i <= 15: // Open + South
			reg = south
		case i <= 20: // Rookie + North
			div = rookie
		default: // Rookie + West
			div, reg = rookie, west
		}
		tm := Team{Name: fmt.Sprintf("Team %d", i), Number: i, DivisionID: div.ID, RegionID: reg.ID}
		if err := d.Create(&tm).Error; err != nil {
			t.Fatalf("create team %d: %v", i, err)
		}
	}
	return open, rookie, north, south, west
}

func firstRegion(d *db.DB, name string) (Region, error) {
	return gorm.G[Region](d.DB).Where(&Region{Name: name}).First(context.Background())
}

// listTeamNames extracts the visible team names from the list page in
// document order (the Name cell is the second <td> in each row).
func listTeamNames(t *testing.T, body string) []string {
	t.Helper()
	rows := tdRowRe.FindAllStringSubmatch(body, -1)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, strings.TrimSpace(r[2]))
	}
	return out
}

var tdRowRe = regexp.MustCompile(`<tr>\s*<td>(.*?)</td>\s*<td>(.*?)</td>`)

// listTotalCount parses the "N of M teams" count line from the page.
func listTotalCount(t *testing.T, body string) int {
	t.Helper()
	m := totalCountRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("total count not found in body:\n%s", body)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse total count: %v", err)
	}
	return n
}

var totalCountRe = regexp.MustCompile(`<p class="has-text-grey mb-2">\s*(\d+) teams`)

func TestListPagination(t *testing.T) {
	m, d := newTestModule(t)
	seedListFixture(t, d)

	// Default page 1: 10 teams (number ASC), numbers 1..10.
	code, body := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/team/", "")
	if code != http.StatusOK {
		t.Fatalf("list = %d, want 200: %s", code, body)
	}
	names := listTeamNames(t, body)
	if len(names) != 10 {
		t.Fatalf("page 1 has %d rows, want 10", len(names))
	}
	want := "Team 1"
	if !strings.Contains(names[0], want) {
		t.Errorf("first row = %q, want Team 1", names[0])
	}
	if listTotalCount(t, body) != 25 {
		t.Errorf("total = %d, want 25", listTotalCount(t, body))
	}
	// 25 teams at 10/page = 3 pages.
	if !strings.Contains(body, "page=2") || !strings.Contains(body, "page=3") {
		t.Error("page 1 pager missing links to pages 2 and 3")
	}

	// Page 2: numbers 11..20, disjoint from page 1.
	code, body2 := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/team/?page=2", "")
	if code != http.StatusOK {
		t.Fatalf("page 2 = %d, want 200: %s", code, body2)
	}
	names2 := listTeamNames(t, body2)
	if len(names2) != 10 {
		t.Fatalf("page 2 has %d rows, want 10", len(names2))
	}
	overlap := map[string]bool{}
	for _, n := range names {
		overlap[n] = true
	}
	for _, n := range names2 {
		if overlap[n] {
			t.Errorf("team %q appears on both page 1 and page 2", n)
		}
	}

	// Page 3: the final 5 teams, numbers 21..25.
	code, body3 := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/team/?page=3", "")
	if code != http.StatusOK {
		t.Fatalf("page 3 = %d, want 200: %s", code, body3)
	}
	if names3 := listTeamNames(t, body3); len(names3) != 5 {
		t.Errorf("page 3 has %d rows, want 5", len(names3))
	}
	// No page=4 link on the last page.
	if strings.Contains(body3, "page=4") {
		t.Error("last page shows a next-page link")
	}
}

func TestListDivisionFacet(t *testing.T) {
	m, d := newTestModule(t)
	_, rookie, _, _, _ := seedListFixture(t, d)

	code, body := routerReq(t, m, adminContext(), http.MethodGet,
		"/ui/mod/team/?division_id="+strconv.FormatUint(uint64(rookie.ID), 10), "")
	if code != http.StatusOK {
		t.Fatalf("division filter = %d, want 200: %s", code, body)
	}
	names := listTeamNames(t, body)
	if len(names) != 10 {
		t.Fatalf("division=Rookie returned %d teams, want 10", len(names))
	}
	for _, n := range names {
		if strings.Contains(n, "Team 1 ") || strings.Contains(n, "Team 10") {
			t.Errorf("Open team leaked into Rookie filter: %q", n)
		}
	}
	if listTotalCount(t, body) != 10 {
		t.Errorf("division total = %d, want 10", listTotalCount(t, body))
	}
}

func TestListRegionFacet(t *testing.T) {
	m, d := newTestModule(t)
	_, _, _, _, west := seedListFixture(t, d)

	code, body := routerReq(t, m, adminContext(), http.MethodGet,
		"/ui/mod/team/?region_id="+strconv.FormatUint(uint64(west.ID), 10), "")
	if code != http.StatusOK {
		t.Fatalf("region filter = %d, want 200: %s", code, body)
	}
	names := listTeamNames(t, body)
	if len(names) != 5 {
		t.Fatalf("region=West returned %d teams, want 5", len(names))
	}
	if listTotalCount(t, body) != 5 {
		t.Errorf("region total = %d, want 5", listTotalCount(t, body))
	}
}

func TestListCombinedFilters(t *testing.T) {
	m, d := newTestModule(t)
	_, rookie, north, _, _ := seedListFixture(t, d)

	// q + division + region ANDed together: Rookie AND North ->
	// numbers 16..20 (q=16 matches numbers 16..19 and 16 via CAST; use a
	// name substring that only hits 16..20 range within Rookie+North).
	qs := "/ui/mod/team/?" +
		"q=Team&division_id=" + strconv.FormatUint(uint64(rookie.ID), 10) +
		"&region_id=" + strconv.FormatUint(uint64(north.ID), 10)
	code, body := routerReq(t, m, adminContext(), http.MethodGet, qs, "")
	if code != http.StatusOK {
		t.Fatalf("combined filter = %d, want 200: %s", code, body)
	}
	names := listTeamNames(t, body)
	if len(names) != 5 {
		t.Fatalf("combined returned %d teams, want 5: %v", len(names), names)
	}
	if listTotalCount(t, body) != 5 {
		t.Errorf("combined total = %d, want 5", listTotalCount(t, body))
	}
	// All must be in Rookie (16..20), none from Open (1..15).
	for _, n := range names {
		if strings.Contains(n, "Team 1 ") || strings.Contains(n, "Team 2 ") || strings.Contains(n, "Team 10") {
			t.Errorf("Open team leaked into combined filter: %q", n)
		}
	}
}

func TestListFilteredPagination(t *testing.T) {
	m, d := newTestModule(t)
	open, _, _, _, _ := seedListFixture(t, d)

	// Filter to Open (15 teams) -> 2 pages at 10/page.
	qs := "/ui/mod/team/?division_id=" + strconv.FormatUint(uint64(open.ID), 10)
	code, body := routerReq(t, m, adminContext(), http.MethodGet, qs, "")
	if code != http.StatusOK {
		t.Fatalf("filtered page 1 = %d, want 200: %s", code, body)
	}
	if listTotalCount(t, body) != 15 {
		t.Errorf("filtered total = %d, want 15", listTotalCount(t, body))
	}
	if len(listTeamNames(t, body)) != 10 {
		t.Errorf("filtered page 1 = %d rows, want 10", len(listTeamNames(t, body)))
	}
	// 15 teams -> page 2 exists, page 3 does not.
	if !strings.Contains(body, "page=2") || strings.Contains(body, "page=3") {
		t.Errorf("filtered pager: want page=2 present, page=3 absent; body=%s", body)
	}

	// Page 2 of the filtered set: the remaining 5 (numbers 11..15).
	code, body = routerReq(t, m, adminContext(), http.MethodGet,
		qs+"&page=2", "")
	if code != http.StatusOK {
		t.Fatalf("filtered page 2 = %d, want 200: %s", code, body)
	}
	if names := listTeamNames(t, body); len(names) != 5 {
		t.Errorf("filtered page 2 = %d rows, want 5", len(names))
	}
}

func TestListStatePreserved(t *testing.T) {
	m, d := newTestModule(t)
	_, _, north, _, _ := seedListFixture(t, d)

	// q + region match 20 teams -> 2 pages, so the pager is rendered.
	qs := "/ui/mod/team/?" +
		"q=Team&region_id=" + strconv.FormatUint(uint64(north.ID), 10) +
		"&page=2"
	code, body := routerReq(t, m, adminContext(), http.MethodGet, qs, "")
	if code != http.StatusOK {
		t.Fatalf("state = %d, want 200: %s", code, body)
	}
	// The search box and facet select retain their submitted values.
	if !strings.Contains(body, `value="Team"`) {
		t.Error("search box lost its value")
	}
	if !strings.Contains(body, `<option value="`+strconv.FormatUint(uint64(north.ID), 10)+`" selected`) {
		t.Error("region facet lost its selection")
	}
	// Pager links re-emit the active filters (url.Values encodes keys in
	// alphabetical order, and pongo2 HTML-escapes & in the href) but not the
	// page param (page is re-added by the link itself).
	if !strings.Contains(body, "q=Team&amp;region_id="+strconv.FormatUint(uint64(north.ID), 10)) {
		t.Error("pager link did not preserve q and region_id")
	}
}

func TestListNoMatch(t *testing.T) {
	m, d := newTestModule(t)
	seedListFixture(t, d)

	code, body := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/team/?q=zzzzz", "")
	if code != http.StatusOK {
		t.Fatalf("no-match list = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "No teams match") {
		t.Error("no-match state missing empty message")
	}
}
