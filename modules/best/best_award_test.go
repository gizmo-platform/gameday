package best

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gizmo-platform/gameday/modules/team"
)

// fakePerformance is a performanceSource returning a fixed table, for
// scorer tests that do not need a game module.
type fakePerformance struct {
	table *performanceTable
	err   error
}

func (f fakePerformance) Points(context.Context) (*performanceTable, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.table == nil {
		return &performanceTable{ByTeam: map[uint]float64{}, State: performanceReady}, nil
	}
	return f.table, nil
}

func perfectPerformance(ids ...uint) *performanceTable {
	table := &performanceTable{ByTeam: map[uint]float64{}, State: performanceReady}
	for i, id := range ids {
		table.ByTeam[id] = performancePoints(i+1, len(ids))
	}
	return table
}

// TestBestAwardRequiresAllComponents is rules §5: a team is eligible
// only when all five components are present.
func TestBestAwardRequiresAllComponents(t *testing.T) {
	_, d := newTestModule(t)
	ctx := context.Background()

	open := divisionByName(t, d, "Open")
	complete := createTeam(t, d, 101, "Alpha", open.ID)
	noVideo := createTeam(t, d, 102, "Beta", open.ID)
	noSeeding := createTeam(t, d, 103, "Gamma", open.ID)

	for _, tm := range []team.Team{complete, noVideo} {
		setScore(t, d, tm.ID, "notebook", 300)
		setScore(t, d, tm.ID, "marketing", 250)
		setScore(t, d, tm.ID, "poster", 100)
	}
	setScore(t, d, complete.ID, "video", 100)

	scorer := &bestAwardScorer{db: d, perf: fakePerformance{table: perfectPerformance(complete.ID, noVideo.ID)}}
	scores, err := scorer.Score(ctx, []team.Team{complete, noVideo, noSeeding})
	if err != nil {
		t.Fatalf("Score: %v", err)
	}

	if len(scores) != 1 {
		t.Fatalf("eligible teams = %d, want 1 (%v)", len(scores), scores)
	}
	// 300 + 250 + 100 + 100 judged + 250 Robot Performance.
	if scores[complete.ID] != 1000 {
		t.Errorf("total = %v, want 1000", scores[complete.ID])
	}

	diag := scorer.Diagnose()
	if diag == nil {
		t.Fatal("expected a diagnosis")
	}
	if len(diag.Candidates) != 3 {
		t.Fatalf("diagnosis rows = %d, want 3", len(diag.Candidates))
	}

	missing := map[int]string{}
	for _, row := range diag.Candidates {
		missing[row.Team.Number] = strings.Join(row.Missing, ",")
		if row.Team.Number == 101 && !row.Ready {
			t.Errorf("complete team reported as not ready (missing %v)", row.Missing)
		}
	}
	if missing[101] != "" {
		t.Errorf("complete team missing %q", missing[101])
	}
	if missing[102] != "Video" {
		t.Errorf("team 102 missing = %q, want Video", missing[102])
	}
	if missing[103] != "Notebook,Marketing,Poster,Video,Robot Performance" {
		t.Errorf("team 103 missing = %q", missing[103])
	}
}

// TestBestAwardSkipsDisabledTeams checks a disabled team is neither
// ranked nor reported.
func TestBestAwardSkipsDisabledTeams(t *testing.T) {
	_, d := newTestModule(t)
	ctx := context.Background()

	open := divisionByName(t, d, "Open")
	tm := createTeam(t, d, 101, "Alpha", open.ID)
	tm.Disabled = true
	if err := d.Save(&tm).Error; err != nil {
		t.Fatalf("disable team: %v", err)
	}

	scorer := &bestAwardScorer{db: d, perf: fakePerformance{table: perfectPerformance(tm.ID)}}
	scores, err := scorer.Score(ctx, []team.Team{tm})
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if len(scores) != 0 {
		t.Errorf("disabled team scored: %v", scores)
	}
	if len(scorer.Diagnose().Candidates) != 0 {
		t.Errorf("disabled team reported in diagnosis")
	}
}

// TestBestAwardPerformanceNotes checks the diagnosis explains why Robot
// Performance is missing.
func TestBestAwardPerformanceNotes(t *testing.T) {
	_, d := newTestModule(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		table *performanceTable
		want  string
	}{
		{"no phase", &performanceTable{ByTeam: map[uint]float64{}, State: performanceNoPhase}, "Seeding role"},
		{"not frozen", &performanceTable{ByTeam: map[uint]float64{}, State: performanceNotFrozen}, "not frozen"},
		{"no game module", &performanceTable{ByTeam: map[uint]float64{}, State: performanceNoGameModule}, "game module"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scorer := &bestAwardScorer{db: d, perf: fakePerformance{table: tc.table}}
			if _, err := scorer.Score(ctx, nil); err != nil {
				t.Fatalf("Score: %v", err)
			}
			notes := strings.Join(scorer.Diagnose().Notes, " ")
			if !strings.Contains(notes, tc.want) {
				t.Errorf("notes = %q, want a mention of %q", notes, tc.want)
			}
		})
	}
}

// TestBestAwardSplitDivisionNote checks the operator is told when a flat
// seeding phase was ranked per division.
func TestBestAwardSplitDivisionNote(t *testing.T) {
	_, d := newTestModule(t)

	table := &performanceTable{ByTeam: map[uint]float64{}, State: performanceReady, SplitByDivision: true}
	scorer := &bestAwardScorer{db: d, perf: fakePerformance{table: table}}
	if _, err := scorer.Score(context.Background(), nil); err != nil {
		t.Fatalf("Score: %v", err)
	}
	if notes := strings.Join(scorer.Diagnose().Notes, " "); !strings.Contains(notes, "per division") {
		t.Errorf("notes = %q, want a mention of the per-division split", notes)
	}
}

// TestBestAwardIsFirstAward checks the composite award leads the page.
func TestBestAwardIsFirstAward(t *testing.T) {
	m, _ := newTestModule(t)

	results, err := m.computeAwards(context.Background())
	if err != nil {
		t.Fatalf("computeAwards: %v", err)
	}
	if len(results) != 6 {
		t.Fatalf("award count = %d, want 6", len(results))
	}
	if results[0].Award.Key != "best" {
		t.Errorf("first award = %q, want best", results[0].Award.Key)
	}
	want := []string{"best", "game", "notebook", "marketing", "poster", "video"}
	for i, key := range want {
		if results[i].Award.Key != key {
			t.Errorf("award %d = %q, want %q", i, results[i].Award.Key, key)
		}
	}
}

// TestBestAwardDiagnosedFlag checks only the BEST Award carries an
// eligibility breakdown, and the flag tracks it.
func TestBestAwardDiagnosedFlag(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	enableDivisionAwarePhase(t, d)
	open := divisionByName(t, d, "Open")
	tm := createTeam(t, d, 101, "Alpha", open.ID)
	setScore(t, d, tm.ID, "notebook", 200)

	results, err := m.computeAwards(ctx)
	if err != nil {
		t.Fatalf("computeAwards: %v", err)
	}

	best := awardByName(t, results, "best")
	if !best.Diagnosed {
		t.Errorf("BEST Award should be diagnosed")
	}
	if len(best.Diagnosis.Components) != 5 {
		t.Errorf("component columns = %d, want 5", len(best.Diagnosis.Components))
	}
	if got := best.Diagnosis.Components[0].Header; got != "Notebook /300" {
		t.Errorf("first column header = %q, want %q", got, "Notebook /300")
	}
	if got := best.Diagnosis.Components[4].Header; got != "Robot Performance /250" {
		t.Errorf("last column header = %q, want %q", got, "Robot Performance /250")
	}

	for _, key := range []string{"game", "notebook", "marketing", "poster", "video"} {
		res := awardByName(t, results, key)
		if res.Diagnosed {
			t.Errorf("award %q should not be diagnosed", key)
		}
	}
}

// TestBestAwardEndToEnd ranks a team on the derived Robot Performance
// points from a frozen seeding phase.
func TestBestAwardEndToEnd(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	open := divisionByName(t, d, "Open")
	teams := []team.Team{
		createTeam(t, d, 101, "Alpha", open.ID),
		createTeam(t, d, 102, "Beta", open.ID),
	}
	seedSeedingPhase(t, d, teams, false)

	for _, tm := range teams {
		setScore(t, d, tm.ID, "notebook", 300)
		setScore(t, d, tm.ID, "marketing", 250)
		setScore(t, d, tm.ID, "poster", 100)
		setScore(t, d, tm.ID, "video", 100)
	}

	results, err := m.computeAwards(ctx)
	if err != nil {
		t.Fatalf("computeAwards: %v", err)
	}

	best := awardByName(t, results, "best")
	group := groupLabel(t, best, overallGroupLabel)
	if len(group.Complete) != 2 {
		t.Fatalf("ranking = %+v, want two teams", group.Complete)
	}
	if group.Complete[0].Team.Number != 101 || group.Complete[0].Score != 1000 {
		t.Errorf("winner = %+v, want team 101 on 1000", group.Complete[0])
	}
	if group.Complete[1].Team.Number != 102 || group.Complete[1].Score != 775 {
		t.Errorf("runner-up = %+v, want team 102 on 775", group.Complete[1])
	}
}

func TestAwardsPageShowsEligibility(t *testing.T) {
	m, d := newTestModule(t)

	open := divisionByName(t, d, "Open")
	teams := []team.Team{
		createTeam(t, d, 101, "Alpha", open.ID),
		createTeam(t, d, 102, "Beta", open.ID),
	}
	seedSeedingPhase(t, d, teams, false)

	for _, tm := range teams {
		setScore(t, d, tm.ID, "notebook", 300)
		setScore(t, d, tm.ID, "marketing", 250)
		setScore(t, d, tm.ID, "poster", 100)
	}
	setScore(t, d, teams[0].ID, "video", 100)

	req := httptest.NewRequest("GET", "http://test.ui/ui/mod/best/awards", nil)
	w := httptest.NewRecorder()
	m.uiViewAwards(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{
		"BEST Award",
		"Eligibility",
		"Robot Performance /250",
		"Notebook /300",
		"Video",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("awards page missing %q", want)
		}
	}
}

// TestAwardsPageIncompleteEligibility checks the page says so when no
// team has every component.
func TestAwardsPageIncompleteEligibility(t *testing.T) {
	m, d := newTestModule(t)

	open := divisionByName(t, d, "Open")
	tm := createTeam(t, d, 101, "Alpha", open.ID)
	setScore(t, d, tm.ID, "notebook", 300)
	setScore(t, d, tm.ID, "marketing", 250)
	setScore(t, d, tm.ID, "poster", 100)
	setScore(t, d, tm.ID, "video", 100)

	req := httptest.NewRequest("GET", "http://test.ui/ui/mod/best/awards", nil)
	w := httptest.NewRecorder()
	m.uiViewAwards(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "No team has completed all five components yet.") {
		t.Errorf("awards page missing the incomplete-eligibility notice: %s", body)
	}
	if !strings.Contains(body, "no phase is marked with the Seeding role") {
		t.Errorf("awards page missing the Robot Performance note")
	}
}

// TestScoresPageFootnote checks the scores page explains that its Total
// leaves Robot Performance out on purpose.
func TestScoresPageFootnote(t *testing.T) {
	m, _ := newTestModule(t)

	req := httptest.NewRequest("GET", "http://test.ui/ui/mod/best/scores/", nil)
	w := httptest.NewRecorder()
	m.uiViewScores(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "judged categories only") {
		t.Errorf("scores page missing the Total footnote")
	}
	if !strings.Contains(body, "<th class=\"has-text-right\">Total</th>") {
		t.Errorf("scores page Total header changed")
	}
}
