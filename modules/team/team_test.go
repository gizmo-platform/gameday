package team

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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
