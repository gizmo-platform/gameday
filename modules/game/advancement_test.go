package game

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
	"gorm.io/gorm"
)

// recordingFilter is a fake AdvancementFilter that records what the
// driver put in its context and, in include mode, advances every team
// in the roster.
type recordingFilter struct {
	name       string
	calls      int
	sbSize     int
	rosterSize int
}

func (f *recordingFilter) Name() string { return f.name }

func (f *recordingFilter) Apply(sctx *AdvancementFilterContext, rule string, mode GamePhaseAdvancementFilterMode, sliceExpr string) error {
	f.calls++
	f.sbSize = len(sctx.Scoreboard)
	f.rosterSize = len(sctx.Roster)
	if mode == GamePhaseAdvancementFilterModeInclude {
		for _, t := range sctx.Roster {
			sctx.Candidates[t.ID] = t
			sctx.Determinations = append(sctx.Determinations, AdvancementDeterminationResult{
				Filter: f.Name(),
				Rule:   rule,
				Team:   t,
				Result: AdvancementDeterminationAccept,
				Reason: "recording filter accepts roster",
			})
		}
	}
	return nil
}

func newTestModule(t *testing.T) (*Module, *db.DB) {
	t.Helper()

	t.Setenv("GAMEDAY_DB", filepath.Join(t.TempDir(), "test.db"))

	d, err := db.New()
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}

	ws, err := web.NewServer(web.WithDB(d))
	if err != nil {
		t.Fatalf("web.NewServer: %v", err)
	}

	tm := team.New(d, ws, nil)
	if tm == nil {
		t.Fatal("team.New failed")
	}
	if err := tm.Migrate(); err != nil {
		t.Fatalf("team Migrate: %v", err)
	}

	deps := modules.ModuleDeps{"team": tm}
	m := New(d, ws, deps)
	if m == nil {
		t.Fatal("game.New failed")
	}
	if err := m.Migrate(); err != nil {
		t.Fatalf("game Migrate: %v", err)
	}

	return m, d
}

func seedTeams(t *testing.T, d *db.DB, n int) []team.Team {
	t.Helper()

	var teams []team.Team
	for i := 1; i <= n; i++ {
		tr := team.Team{Name: fmt.Sprintf("Team %d", i), Number: i * 10, Region: "NW"}
		if err := d.Create(&tr).Error; err != nil {
			t.Fatalf("create team: %v", err)
		}
		teams = append(teams, tr)
	}
	return teams
}

// TestRunAdvancementFiltersRosterSource verifies that a filter with
// SelectFrom of 0 is handed an empty scoreboard and a populated
// roster, and that candidates flow through from the roster.
func TestRunAdvancementFiltersRosterSource(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 5)

	rec := &recordingFilter{name: "testRosterRecorder"}
	RegisterAdvancementFilter(rec.Name(), rec)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	filt := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      rec.Name(),
		Rule:        "PickRoster",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  0,
		SliceExpr:   "4",
	}
	if err := d.Create(&filt).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	phase.AdvancementFilters = []GamePhaseAdvancementFilter{filt}

	advancing, determinations, err := m.runAdvancementFilters(ctx, phase, []GamePhase{}, map[uint]bool{}, "", teams)
	if err != nil {
		t.Fatalf("runAdvancementFilters: %v", err)
	}

	if rec.calls != 1 {
		t.Fatalf("filter called %d times, want 1", rec.calls)
	}
	if rec.sbSize != 0 {
		t.Errorf("scoreboard size = %d, want 0 for roster-sourced filter", rec.sbSize)
	}
	if rec.rosterSize != len(teams) {
		t.Errorf("roster size = %d, want %d", rec.rosterSize, len(teams))
	}
	if len(advancing) != len(teams) {
		t.Errorf("advancing = %d teams, want %d", len(advancing), len(teams))
	}
	if len(determinations) != len(teams) {
		t.Errorf("determinations = %d, want %d", len(determinations), len(teams))
	}
}

// TestRunAdvancementFiltersRosterSourceNoFallback verifies that
// ScoreboardRanking produces zero candidates on an empty scoreboard
// (SelectFrom of 0) without panicking and without falling back to the
// roster.
func TestRunAdvancementFiltersRosterSourceNoFallback(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 5)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	filt := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      "ScoreboardRanking",
		Rule:        "PickTop4",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  0,
		SliceExpr:   "4",
	}
	if err := d.Create(&filt).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	phase.AdvancementFilters = []GamePhaseAdvancementFilter{filt}

	advancing, _, err := m.runAdvancementFilters(ctx, phase, []GamePhase{}, map[uint]bool{}, "", teams)
	if err != nil {
		t.Fatalf("runAdvancementFilters: %v", err)
	}
	if len(advancing) != 0 {
		t.Errorf("advancing = %d teams, want 0 (no roster fallback)", len(advancing))
	}
}

// TestRosterAdvancementInclude verifies the Roster filter advances
// every team in the roster in include mode with one determination per
// team.
func TestRosterAdvancementInclude(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 5)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	filt := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      "Roster",
		Rule:        "PickAll",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  0,
	}
	if err := d.Create(&filt).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	phase.AdvancementFilters = []GamePhaseAdvancementFilter{filt}

	advancing, determinations, err := m.runAdvancementFilters(ctx, phase, []GamePhase{}, map[uint]bool{}, "", teams)
	if err != nil {
		t.Fatalf("runAdvancementFilters: %v", err)
	}
	if len(advancing) != len(teams) {
		t.Errorf("advancing = %d teams, want %d", len(advancing), len(teams))
	}
	if len(determinations) != len(teams) {
		t.Errorf("determinations = %d, want %d", len(determinations), len(teams))
	}
	for _, det := range determinations {
		if det.Result != AdvancementDeterminationAccept {
			t.Errorf("determination for team %d = %v, want accept", det.Team.ID, det.Result)
		}
	}
}

// TestRosterAdvancementExclude verifies the Roster filter removes
// every roster team from the candidates in exclude mode while leaving
// non-roster candidates untouched.
func TestRosterAdvancementExclude(t *testing.T) {
	sctx := AdvancementFilterContext{
		Roster:     map[uint]team.Team{1: {ID: 1}, 2: {ID: 2}},
		Candidates: map[uint]team.Team{1: {ID: 1}, 2: {ID: 2}, 3: {ID: 3}},
	}
	f := new(RosterAdvancement)
	if err := f.Apply(&sctx, "DropAll", GamePhaseAdvancementFilterModeExclude, ""); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(sctx.Candidates) != 1 {
		t.Errorf("candidates = %d, want 1 (only team 3 survives)", len(sctx.Candidates))
	}
	if _, ok := sctx.Candidates[3]; !ok {
		t.Error("team 3 not in candidates, want it to survive")
	}
}

// TestRunAdvancementFiltersRealSource verifies that a non-zero
// SelectFrom still loads the source phase scoreboard.
func TestRunAdvancementFiltersRealSource(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 5)

	src := GamePhase{Name: "qualifying", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&src).Error; err != nil {
		t.Fatalf("create source phase: %v", err)
	}
	for i, tr := range teams {
		ms := MatchScore{Score: 10 + i, GamePhaseID: src.ID, TeamID: tr.ID}
		if err := d.Create(&ms).Error; err != nil {
			t.Fatalf("create match score: %v", err)
		}
	}

	rec := &recordingFilter{name: "testRealSourceRecorder"}
	RegisterAdvancementFilter(rec.Name(), rec)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	filt := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      rec.Name(),
		Rule:        "PickRoster",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  src.ID,
		SliceExpr:   "4",
	}
	if err := d.Create(&filt).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	phase.AdvancementFilters = []GamePhaseAdvancementFilter{filt}

	_, _, err := m.runAdvancementFilters(ctx, phase, []GamePhase{}, map[uint]bool{}, "", teams)
	if err != nil {
		t.Fatalf("runAdvancementFilters: %v", err)
	}
	if rec.calls != 1 {
		t.Fatalf("filter called %d times, want 1", rec.calls)
	}
	if rec.sbSize != len(teams) {
		t.Errorf("scoreboard size = %d, want %d from source phase", rec.sbSize, len(teams))
	}
}

// TestRunAdvancementFiltersUnregisteredFilter verifies that an
// unregistered filter name produces an error from the driver.
func TestRunAdvancementFiltersUnregisteredFilter(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 3)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	filt := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      "DefinitelyNotRegistered",
		Rule:        "PickRoster",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  0,
		SliceExpr:   "4",
	}
	if err := d.Create(&filt).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	phase.AdvancementFilters = []GamePhaseAdvancementFilter{filt}

	if _, _, err := m.runAdvancementFilters(ctx, phase, []GamePhase{}, map[uint]bool{}, "", teams); err == nil {
		t.Fatal("expected error for unregistered filter, got nil")
	}
}

// TestPhaseSchedulable verifies the scheduling gate: a phase whose
// only filter is roster-sourced is schedulable immediately, a phase
// gated on a real source waits for that source to complete and be
// frozen, and a mixed phase is still gated by its real source.
func TestPhaseSchedulable(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	if err := d.Create(&GamePhase{Name: "qualifying", Frozen: true}).Error; err != nil {
		t.Fatalf("create phase 1: %v", err)
	}
	if err := d.Create(&GamePhase{Name: "seeding" /* roster-sourced only */}).Error; err != nil {
		t.Fatalf("create phase 2: %v", err)
	}
	if err := d.Create(&GamePhase{Name: "mix" /* roster + real source */}).Error; err != nil {
		t.Fatalf("create phase 3: %v", err)
	}
	if err := d.Create(&GamePhase{Name: "waiting" /* real source only */}).Error; err != nil {
		t.Fatalf("create phase 4: %v", err)
	}
	if err := d.Create(&GamePhase{Name: "manual" /* no filters */}).Error; err != nil {
		t.Fatalf("create phase 5: %v", err)
	}

	if err := d.Create(&GamePhaseAdvancementFilter{GamePhaseID: 2, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 0, SliceExpr: "4"}).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	if err := d.Create(&GamePhaseAdvancementFilter{GamePhaseID: 3, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 0, SliceExpr: "4"}).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	if err := d.Create(&GamePhaseAdvancementFilter{GamePhaseID: 3, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 1, SliceExpr: "4"}).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	if err := d.Create(&GamePhaseAdvancementFilter{GamePhaseID: 4, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 1, SliceExpr: "4"}).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}

	phases, err := findPhases(m, ctx)
	if err != nil {
		t.Fatalf("find phases: %v", err)
	}

	cases := []struct {
		name  string
		phase uint
		comp  map[uint]bool
		want  bool
	}{
		{"roster-only phase is immediately schedulable", 2, map[uint]bool{}, true},
		{"mixed phase waits on real source", 3, map[uint]bool{}, false},
		{"mixed phase satisfied when real source complete and frozen", 3, map[uint]bool{1: true}, true},
		{"real-source phase waits when source incomplete", 4, map[uint]bool{}, false},
		{"real-source phase satisfied when source complete and frozen", 4, map[uint]bool{1: true}, true},
		{"filterless phase is schedulable", 5, map[uint]bool{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var target GamePhase
			for _, p := range phases {
				if p.ID == tc.phase {
					target = p
					break
				}
			}
			got, err := m.phaseSchedulable(ctx, target, phases, tc.comp)
			if err != nil {
				t.Fatalf("phaseSchedulable: %v", err)
			}
			if got != tc.want {
				t.Errorf("phaseSchedulable = %v, want %v", got, tc.want)
			}
		})
	}
}

func findPhases(m *Module, ctx context.Context) ([]GamePhase, error) {
	return gorm.G[GamePhase](m.db.DB).Find(ctx)
}

// TestRunAdvancementFiltersWhen verifies the per-filter When
// condition: an empty or true condition runs the filter, false skips
// it (contributing no candidates or determinations), and the
// expression sees the same context as a phase When expression,
// including the filter's own SelectFrom scoreboard.  Compilation,
// runtime, and non-boolean errors propagate.
func TestRunAdvancementFiltersWhen(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 3)

	if err := d.Create(&GamePhase{Name: "qualifying", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}).Error; err != nil {
		t.Fatalf("create qualifying: %v", err)
	}
	if err := d.Create(&GamePhase{Name: "finals", ScoreSummation: "Total", ScheduleType: "OneShot"}).Error; err != nil {
		t.Fatalf("create finals: %v", err)
	}
	phases := whenTestPhases(t, m, ctx)
	if len(phases) != 2 {
		t.Fatalf("expected 2 phases, got %d", len(phases))
	}
	byName := map[string]GamePhase{}
	for _, p := range phases {
		byName[p.Name] = p
	}
	qualifying, finals := byName["qualifying"], byName["finals"]

	// Seed qualifying as complete and with a scoreboard.
	mp := MatchPlacement{PhaseID: qualifying.ID, TeamID: teams[0].ID, State: MatchStateComplete}
	if err := d.Create(&mp).Error; err != nil {
		t.Fatalf("create match placement: %v", err)
	}
	for i, tr := range teams {
		ms := MatchScore{Score: 10 + i, GamePhaseID: qualifying.ID, TeamID: tr.ID}
		if err := d.Create(&ms).Error; err != nil {
			t.Fatalf("create match score: %v", err)
		}
	}

	rec := &recordingFilter{name: "testFilterWhenRecorder"}
	RegisterAdvancementFilter(rec.Name(), rec)
	filt := GamePhaseAdvancementFilter{
		GamePhaseID: finals.ID,
		Filter:      rec.Name(),
		Rule:        "PickRoster",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  qualifying.ID,
		SliceExpr:   "3",
	}
	if err := d.Create(&filt).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	finals.AdvancementFilters = []GamePhaseAdvancementFilter{filt}

	phaseComplete, err := m.phaseCompletionStates(ctx, phases)
	if err != nil {
		t.Fatalf("phaseCompletionStates: %v", err)
	}

	cases := []struct {
		name        string
		when        string
		division    string
		wantErr     bool
		wantCalls   int
		wantAdv     int
		wantDets    int
	}{
		{"empty When runs the filter", "", "", false, 1, len(teams), len(teams)},
		{"literal true runs the filter", "true", "", false, 1, len(teams), len(teams)},
		{"literal false skips the filter", "false", "", false, 0, 0, 0},
		{"phase state reference satisfied (qualifying complete)", "Phases[1].Complete", "", false, 1, len(teams), len(teams)},
		{"phase state reference unsatisfied (finals not frozen)", "Phases[0].Frozen", "", false, 0, 0, 0},
		{"roster size satisfied", "len(Roster) == 3", "", false, 1, len(teams), len(teams)},
		{"scoreboard from own SelectFrom", "Scoreboard[0].Rank == 1", "", false, 1, len(teams), len(teams)},
		{"scoreboard rank mismatch skips", "Scoreboard[0].Rank == 2", "", false, 0, 0, 0},
		{"division context mismatch skips", `Division == "Open"`, "", false, 0, 0, 0},
		{"division context satisfied", `Division == ""`, "", false, 1, len(teams), len(teams)},
		{"syntax error propagates", "this is not valid(((", "", true, 0, 0, 0},
		{"non-boolean result propagates", "1", "", true, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filt.When = tc.when
			finals.AdvancementFilters = []GamePhaseAdvancementFilter{filt}
			rec.calls = 0

			advancing, determinations, err := m.runAdvancementFilters(ctx, finals, phases, phaseComplete, tc.division, teams)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("runAdvancementFilters = no error, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("runAdvancementFilters: %v", err)
			}
			if rec.calls != tc.wantCalls {
				t.Errorf("filter called %d times, want %d", rec.calls, tc.wantCalls)
			}
			if len(advancing) != tc.wantAdv {
				t.Errorf("advancing = %d teams, want %d", len(advancing), tc.wantAdv)
			}
			if len(determinations) != tc.wantDets {
				t.Errorf("determinations = %d, want %d", len(determinations), tc.wantDets)
			}
		})
	}
}

// TestRunAdvancementFiltersWhenSkippedStillRunsOthers verifies that a
// filter skipped by a false When condition does not stop the
// remaining filters on the phase from applying.
func TestRunAdvancementFiltersWhenSkippedStillRunsOthers(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 4)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	skipped := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      "Roster",
		Rule:        "PickAll",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  0,
		When:        "false",
	}
	if err := d.Create(&skipped).Error; err != nil {
		t.Fatalf("create skipped filter: %v", err)
	}
	kept := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      "Roster",
		Rule:        "PickAll",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  0,
		When:        "true",
	}
	if err := d.Create(&kept).Error; err != nil {
		t.Fatalf("create kept filter: %v", err)
	}
	phase.AdvancementFilters = []GamePhaseAdvancementFilter{skipped, kept}

	advancing, determinations, err := m.runAdvancementFilters(ctx, phase, []GamePhase{}, map[uint]bool{}, "", teams)
	if err != nil {
		t.Fatalf("runAdvancementFilters: %v", err)
	}
	if len(advancing) != len(teams) {
		t.Errorf("advancing = %d teams, want %d (kept filter ran)", len(advancing), len(teams))
	}
	if len(determinations) != len(teams) {
		t.Errorf("determinations = %d, want %d (only the kept filter emits)", len(determinations), len(teams))
	}
	for _, det := range determinations {
		if det.Rule != "PickAll" {
			t.Errorf("determination rule = %q, want PickAll", det.Rule)
		}
	}
}
