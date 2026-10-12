package game

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/pkg/db"
)

// seedContest creates the onsite_contests table (the game test harness
// does not run the contest module's migration) and seeds a single
// contest row with the given ID.
func seedContest(t *testing.T, d *db.DB, id uint) {
	t.Helper()

	if err := d.AutoMigrate(&onsiteContestRow{}); err != nil {
		t.Fatalf("migrate onsite_contests: %v", err)
	}
	if err := d.Create(&onsiteContestRow{ID: id}).Error; err != nil {
		t.Fatalf("create onsite contest: %v", err)
	}
}

// seedFieldPosition get-or-creates a named FieldPosition. The game
// Migrate seeds no positions, and the OneShot generator divides by the
// slot count, so a schedule test must seed at least one.
func seedFieldPosition(t *testing.T, d *db.DB, name string) FieldPosition {
	t.Helper()

	position, err := gorm.G[FieldPosition](d.DB).Where(&FieldPosition{Name: name}).First(context.Background())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("find %s position: %v", name, err)
	}
	if position.ID == 0 {
		position = FieldPosition{Name: name}
		if err := d.Create(&position).Error; err != nil {
			t.Fatalf("create %s position: %v", name, err)
		}
	}
	return position
}

// postSchedulePreview drives the schedule preview handler directly,
// bypassing the GAME:SCHEDULE guard.
func postSchedulePreview(t *testing.T, m *Module, phaseID uint, fieldIDs, teamIDs []string) *httptest.ResponseRecorder {
	t.Helper()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", phaseID))

	form := url.Values{}
	for _, f := range fieldIDs {
		form.Add("fields", f)
	}
	for _, tm := range teamIDs {
		form.Add("selected_teams", tm)
	}
	form.Set("rounds", "1")
	form.Set("schedule_type", "OneShot")

	req := httptest.NewRequest("POST",
		fmt.Sprintf("http://test.ui/ui/mod/game/schedule/phases/%d/select-teams", phaseID),
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	m.uiViewPhaseSchedulePreview(w, req)
	return w
}

// TestSchedulePlacementCarriesContestID verifies that candidate
// placements created by the schedule preview carry the ID of the
// contest selected on the onsite server.
func TestSchedulePlacementCarriesContestID(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 4)
	seedContest(t, d, 7)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	seedFieldPosition(t, d, "Test")

	field, err := gorm.G[Field](d.DB).Where(&Field{Name: "Field 1"}).First(ctx)
	if err != nil {
		t.Fatalf("find Field 1: %v", err)
	}

	fieldIDs := []string{fmt.Sprintf("%d", field.ID)}
	teamIDs := make([]string, len(teams))
	for i, tr := range teams {
		teamIDs[i] = fmt.Sprintf("%d", tr.ID)
	}
	postSchedulePreview(t, m, phase.ID, fieldIDs, teamIDs)

	placements, err := gorm.G[MatchPlacement](d.DB).Where(&MatchPlacement{PhaseID: CandidatePhase}).Find(ctx)
	if err != nil {
		t.Fatalf("find candidate placements: %v", err)
	}
	if len(placements) == 0 {
		t.Fatal("no candidate placements created")
	}
	for _, p := range placements {
		if p.ContestID != 7 {
			t.Errorf("placement %d: ContestID = %d, want 7", p.ID, p.ContestID)
		}
	}
}

// TestSchedulePlacementZeroWhenNoContest verifies that candidate
// placements carry ContestID 0 when no contest is selected on the
// onsite server.
func TestSchedulePlacementZeroWhenNoContest(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 4)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	seedFieldPosition(t, d, "Test")

	field, err := gorm.G[Field](d.DB).Where(&Field{Name: "Field 1"}).First(ctx)
	if err != nil {
		t.Fatalf("find Field 1: %v", err)
	}

	fieldIDs := []string{fmt.Sprintf("%d", field.ID)}
	teamIDs := make([]string, len(teams))
	for i, tr := range teams {
		teamIDs[i] = fmt.Sprintf("%d", tr.ID)
	}
	postSchedulePreview(t, m, phase.ID, fieldIDs, teamIDs)

	placements, err := gorm.G[MatchPlacement](d.DB).Where(&MatchPlacement{PhaseID: CandidatePhase}).Find(ctx)
	if err != nil {
		t.Fatalf("find candidate placements: %v", err)
	}
	if len(placements) == 0 {
		t.Fatal("no candidate placements created")
	}
	for _, p := range placements {
		if p.ContestID != 0 {
			t.Errorf("placement %d: ContestID = %d, want 0", p.ID, p.ContestID)
		}
	}
}

// TestScorecardCarriesContestID verifies that a MatchScore created by
// the scorecard submit handler mirrors the ContestID of its placement.
func TestScorecardCarriesContestID(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 1)
	seedContest(t, d, 7)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	field, err := gorm.G[Field](d.DB).Where(&Field{Name: "Field 1"}).First(ctx)
	if err != nil {
		t.Fatalf("find Field 1: %v", err)
	}
	position := seedFieldPosition(t, d, "Test")

	mp := MatchPlacement{
		PhaseID:    phase.ID,
		Match:      1,
		TeamID:     teams[0].ID,
		FieldID:    field.ID,
		PositionID: position.ID,
		State:      MatchStateScheduled,
		ContestID:  7,
	}
	if err := d.Create(&mp).Error; err != nil {
		t.Fatalf("create placement: %v", err)
	}

	sce := ScorecardElement{Element: "widgets_fabricated", Type: "count", Expr: "int(widgets_fabricated * 1)"}
	if err := d.Create(&sce).Error; err != nil {
		t.Fatalf("create scorecard element: %v", err)
	}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("phase", fmt.Sprintf("%d", phase.ID))
	rctx.URLParams.Add("match", "1")
	rctx.URLParams.Add("field", fmt.Sprintf("%d", field.ID))
	rctx.URLParams.Add("position", fmt.Sprintf("%d", position.ID))

	form := url.Values{}
	form.Set("widgets_fabricated", "5")

	req := httptest.NewRequest("POST",
		fmt.Sprintf("http://test.ui/ui/mod/game/scorecard/%d/1/%d/%d", phase.ID, field.ID, position.ID),
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	m.uiViewScorecardSubmit(w, req)

	score, err := gorm.G[MatchScore](d.DB).Where(&MatchScore{ID: mp.ID}).First(ctx)
	if err != nil {
		t.Fatalf("find match score: %v", err)
	}
	if score.ContestID != 7 {
		t.Errorf("MatchScore.ContestID = %d, want 7", score.ContestID)
	}
	if score.Score != 5 {
		t.Errorf("MatchScore.Score = %d, want 5", score.Score)
	}
}

// TestExportContestData verifies that ExportContestData returns the
// placements, scores, and scorecard values for a contest as portable
// rows, and that an unknown contest ID yields an empty slice.
func TestExportContestData(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 1)
	seedContest(t, d, 7)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	field, err := gorm.G[Field](d.DB).Where(&Field{Name: "Field 1"}).First(ctx)
	if err != nil {
		t.Fatalf("find Field 1: %v", err)
	}
	position := seedFieldPosition(t, d, "Test")

	mp := MatchPlacement{
		Round:      1,
		Match:      1,
		PhaseID:    phase.ID,
		TeamID:     teams[0].ID,
		FieldID:    field.ID,
		PositionID: position.ID,
		State:      MatchStateComplete,
		ContestID:  7,
	}
	if err := d.Create(&mp).Error; err != nil {
		t.Fatalf("create placement: %v", err)
	}
	ms := MatchScore{ID: mp.ID, MatchPlacementID: mp.ID, GamePhaseID: phase.ID, TeamID: teams[0].ID, Score: 42, ContestID: 7}
	if err := d.Create(&ms).Error; err != nil {
		t.Fatalf("create score: %v", err)
	}
	sv := ScorecardValue{MatchPlacementID: mp.ID, Element: "widgets_fabricated", Value: 5}
	if err := d.Create(&sv).Error; err != nil {
		t.Fatalf("create scorecard value: %v", err)
	}

	out, err := m.ExportContestData(ctx, 7)
	if err != nil {
		t.Fatalf("ExportContestData: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("ExportContestData returned %d rows, want 1", len(out))
	}
	got := out[0]
	if got.TeamName != teams[0].Name {
		t.Errorf("TeamName = %q, want %q", got.TeamName, teams[0].Name)
	}
	if got.Score != 42 {
		t.Errorf("Score = %d, want 42", got.Score)
	}
	if got.State != MatchStateComplete {
		t.Errorf("State = %v, want %v", got.State, MatchStateComplete)
	}
	if len(got.Values) != 1 || got.Values[0].Element != "widgets_fabricated" || got.Values[0].Value != 5 {
		t.Errorf("Values = %+v, want [{widgets_fabricated 5}]", got.Values)
	}

	empty, err := m.ExportContestData(ctx, 999)
	if err != nil {
		t.Fatalf("ExportContestData(999): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("ExportContestData(999) returned %d rows, want 0", len(empty))
	}
}
