package game

import (
	"context"
	"sort"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
)

const whenRoundTripYAML = `
Field:
  Positions:
    - ID: 1
      Name: alliance
      Color1: "e74c3c"
      Color2: "fadbd8"
Game:
  Phases:
    - Name: qualifying
      ID: 1
      ScheduleType: RandomSeeding
      ScoreSummation: AverageWithMulligan
    - Name: finals
      ID: 2
      ScheduleType: OneShot
      ScoreSummation: Total
      Suppress: 'len(Roster) < 4'
      SuppressMsg: 'too few teams — finals suppressed'
`

// TestWhenRoundTrip verifies that Suppress/SuppressMsg survive the
// full authored-YAML to database round trip: unmarshaled from YAML
// into a Config, persisted with InsertOrUpdate, and reloaded from
// SQLite.  This also confirms the column names survive AutoMigrate
// and GORM's sqlite identifier quoting.
func TestWhenRoundTrip(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	var c Config
	if err := yaml.Unmarshal([]byte(whenRoundTripYAML), &c); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}

	for _, phase := range c.Game.Phases {
		if err := db.InsertOrUpdate[GamePhase](ctx, d.DB, &phase); err != nil {
			t.Fatalf("InsertOrUpdate: %v", err)
		}
	}

	phases, err := findPhases(m, ctx)
	if err != nil {
		t.Fatalf("find phases: %v", err)
	}
	if len(phases) != 2 {
		t.Fatalf("expected 2 phases, got %d", len(phases))
	}

	for _, phase := range phases {
		switch phase.ID {
		case 1:
			if phase.Suppress != "" || phase.SuppressMsg != "" {
				t.Errorf("phase 1: expected empty Suppress/SuppressMsg, got %q / %q", phase.Suppress, phase.SuppressMsg)
			}
		case 2:
			if phase.Suppress != "len(Roster) < 4" {
				t.Errorf("phase 2: Suppress = %q, want %q", phase.Suppress, "len(Roster) < 4")
			}
			want := "too few teams — finals suppressed"
			if phase.SuppressMsg != want {
				t.Errorf("phase 2: SuppressMsg = %q, want %q", phase.SuppressMsg, want)
			}
		}
	}

	// A re-upload without When must reset the columns (UpdateAll).
	var bare Config
	bareYAML := `
Field:
  Positions:
    - ID: 1
      Name: alliance
      Color1: "e74c3c"
      Color2: "fadbd8"
Game:
  Phases:
    - Name: finals
      ID: 2
      ScheduleType: OneShot
      ScoreSummation: Total
`
	if err := yaml.Unmarshal([]byte(bareYAML), &bare); err != nil {
		t.Fatalf("yaml.Unmarshal bare: %v", err)
	}
	for _, phase := range bare.Game.Phases {
		if err := db.InsertOrUpdate[GamePhase](ctx, d.DB, &phase); err != nil {
			t.Fatalf("InsertOrUpdate bare: %v", err)
		}
	}

	phases, err = findPhases(m, ctx)
	if err != nil {
		t.Fatalf("find phases after bare re-upload: %v", err)
	}
	for _, phase := range phases {
		if phase.ID != 2 {
			continue
		}
		if phase.Suppress != "" || phase.SuppressMsg != "" {
			t.Errorf("phase 2 after bare re-upload: expected Suppress/SuppressMsg cleared, got %q / %q", phase.Suppress, phase.SuppressMsg)
		}
	}
}

// whenTestPhases loads the phases in the database ordered by Name so
// tests can reference Phases[N] positionally without depending on
// SQLite ID assignment order.
func whenTestPhases(t *testing.T, m *Module, ctx context.Context) []GamePhase {
	t.Helper()
	phases, err := findPhases(m, ctx)
	if err != nil {
		t.Fatalf("find phases: %v", err)
	}
	sort.Slice(phases, func(i, j int) bool { return phases[i].Name < phases[j].Name })
	return phases
}

// TestWhenSuppressed verifies the boolean suppression expression
// evaluated by whenSuppressed: literals, empty conditions, phase
// completion state, roster references, advancement-filter-sourced
// scoreboards, and compile/runtime/type errors.  It asserts the raw
// boolean value of the expression; an empty expression never
// suppresses.
func TestWhenSuppressed(t *testing.T) {
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
	qualifying, finals := phases[0], phases[1]

	// Seed qualifying as complete and finals as unscheduled.
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

	filt := GamePhaseAdvancementFilter{
		GamePhaseID: finals.ID,
		Filter:      "ScoreboardRanking",
		Rule:        "Pick",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  qualifying.ID,
		SliceExpr:   "2",
	}
	if err := d.Create(&filt).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}

	phaseComplete, err := m.phaseCompletionStates(ctx, phases)
	if err != nil {
		t.Fatalf("phaseCompletionStates: %v", err)
	}
	if !phaseComplete[qualifying.ID] {
		t.Error("qualifying phase: expected complete, got false")
	}
	if phaseComplete[finals.ID] {
		t.Error("finals phase: expected not complete, got true")
	}

	byID := map[uint]GamePhase{qualifying.ID: qualifying, finals.ID: finals}
	cases := []struct {
		name string
		phase GamePhase
		expr string
		division string
		want bool
		wantErr bool
	}{
		{"empty expression never suppresses", finals, "", "", false, false},
		{"literal true", finals, "true", "", true, false},
		{"literal false", finals, "false", "", false, false},
		{"complete source phase referenced positionally", finals, "Phases[0].Complete", "", true, false},
		{"incomplete target phase referenced positionally", finals, "Phases[1].Complete", "", false, false},
		{"roster size", finals, "len(Roster) == 3", "", true, false},
		{"roster size mismatch", finals, "len(Roster) == 4", "", false, false},
		{"scoreboard rank from source phase", finals, "Scoreboard[0].Rank == 1", "", true, false},
		{"scoreboard top team identity", finals, "Scoreboard[0].Team.Name == \"Team 3\"", "", true, false},
		{"scoreboard non-top team identity", finals, "Scoreboard[0].Team.Name == \"Team 1\"", "", false, false},
		{"division string context", finals, "Division == \"\"", "", true, false},
		{"division string mismatch", finals, "Division == \"Open\"", "", false, false},
		{"syntax error does not compile", finals, "this is not valid(((", "", false, true},
		{"non-boolean result is an error", finals, "1", "", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ph := byID[tc.phase.ID]
			ph.Suppress = tc.expr
			got, err := m.whenSuppressed(ctx, ph, phases, phaseComplete, teams, tc.division)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("whenSuppressed = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("whenSuppressed: %v", err)
			}
			if got != tc.want {
				t.Errorf("whenSuppressed = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestWhenStates verifies the per-phase schedulability and message
// maps produced by whenStates: phases without a When condition are
// always schedulable with no message, suppressed phases are inactive
// and surface their WhenMsg, and non-suppressed phases are active with
// no message.
func TestWhenStates(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	if err := d.Create(&GamePhase{Name: "qualifying"}).Error; err != nil {
		t.Fatalf("create qualifying: %v", err)
	}
	if err := d.Create(&GamePhase{Name: "finals", Suppress: "false", SuppressMsg: "hidden"}).Error; err != nil {
		t.Fatalf("create finals: %v", err)
	}
	if err := d.Create(&GamePhase{Name: "playoffs", Suppress: "true", SuppressMsg: "schedule the playoffs"}).Error; err != nil {
		t.Fatalf("create playoffs: %v", err)
	}

	teams := seedTeams(t, d, 3)
	phases := whenTestPhases(t, m, ctx)
	phaseComplete, err := m.phaseCompletionStates(ctx, phases)
	if err != nil {
		t.Fatalf("phaseCompletionStates: %v", err)
	}

	active, msgs, err := m.whenStates(ctx, phases, phaseComplete, teams)
	if err != nil {
		t.Fatalf("whenStates: %v", err)
	}

	byName := map[string]GamePhase{}
	for _, p := range phases {
		byName[p.Name] = p
	}

	if !active[byName["qualifying"].ID] || msgs[byName["qualifying"].ID] != "" {
		t.Errorf("qualifying: active = %v, msg = %q, want true / empty", active[byName["qualifying"].ID], msgs[byName["qualifying"].ID])
	}
	if !active[byName["finals"].ID] {
		t.Error("finals: active = false, want true (suppression condition is false)")
	}
	if msgs[byName["finals"].ID] != "" {
		t.Errorf("finals: msg = %q, want empty while not suppressed", msgs[byName["finals"].ID])
	}
	if active[byName["playoffs"].ID] || msgs[byName["playoffs"].ID] != "schedule the playoffs" {
		t.Errorf("playoffs: active = %v, msg = %q, want false / %q", active[byName["playoffs"].ID], msgs[byName["playoffs"].ID], "schedule the playoffs")
	}
}

// TestWhenStatesDivisionAware verifies that a division-aware phase is
// suppressed when its When expression holds for any configured
// division.  The team module migration always seeds the "Open"
// division alongside A and B, so a condition that names a single
// division holds for exactly one of them.
func TestWhenStatesDivisionAware(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	if err := d.Create(&team.Division{Name: "A", Static: true, ID: 2}).Error; err != nil {
		t.Fatalf("create division A: %v", err)
	}
	if err := d.Create(&team.Division{Name: "B", Static: true, ID: 3}).Error; err != nil {
		t.Fatalf("create division B: %v", err)
	}

	if err := d.Create(&GamePhase{Name: "finals", DivisionAware: true, Suppress: "Division == \"A\"", SuppressMsg: "division A only"}).Error; err != nil {
		t.Fatalf("create finals: %v", err)
	}

	teams := seedTeams(t, d, 3)
	phases := whenTestPhases(t, m, ctx)
	phaseComplete, err := m.phaseCompletionStates(ctx, phases)
	if err != nil {
		t.Fatalf("phaseCompletionStates: %v", err)
	}

	active, _, err := m.whenStates(ctx, phases, phaseComplete, teams)
	if err != nil {
		t.Fatalf("whenStates: %v", err)
	}
	if active[phases[0].ID] {
		t.Error("division-aware phase: active = true, want false (suppression condition holds for division A)")
	}
}
