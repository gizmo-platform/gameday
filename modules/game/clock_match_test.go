package game

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/event"
	"github.com/gizmo-platform/gameday/pkg/web"
	"gorm.io/gorm"
)

// seedClockSettingsWithTransitions seeds the clock settings singleton
// with the given transition flag.
func seedClockSettingsWithTransitions(t *testing.T, m *Module, duration, hurry time.Duration, transitions bool) {
	t.Helper()
	if err := db.InsertOrUpdate(context.Background(), m.db.DB, &ClockSettings{
		ID:          ClockSettingsID,
		Duration:    duration,
		Hurry:       hurry,
		Transitions: transitions,
	}); err != nil {
		t.Fatalf("seed clock settings: %v", err)
	}
}

// seedPhasePlacements creates an active phase and one or two
// placements per given match number, all in the given initial state.
func seedPhasePlacements(t *testing.T, m *Module, matches []int, state MatchState) *GamePhase {
	t.Helper()
	phase := GamePhase{Name: "test phase", Active: true}
	if err := m.db.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	for _, match := range matches {
		for pos := 1; pos <= 2; pos++ {
			p := MatchPlacement{
				Round:    1,
				Match:    match,
				PhaseID:  phase.ID,
				Field:    Field{ID: 1, Name: "Field"},
				Position: FieldPosition{ID: uint(pos), Name: "P"},
				State:    state,
			}
			if err := m.db.Create(&p).Error; err != nil {
				t.Fatalf("create placement: %v", err)
			}
		}
	}
	return &phase
}

// statesForMatch returns the distinct states of the placements in a
// match; a single-state match returns a one-element slice.
func statesForMatch(t *testing.T, m *Module, phaseID uint, match int) []MatchState {
	t.Helper()
	ps, err := gorm.G[MatchPlacement](m.db.DB).
		Where(&MatchPlacement{PhaseID: phaseID, Match: match}).
		Find(context.Background())
	if err != nil {
		t.Fatalf("find placements: %v", err)
	}
	seen := map[MatchState]bool{}
	out := []MatchState{}
	for _, p := range ps {
		if !seen[p.State] {
			seen[p.State] = true
			out = append(out, p.State)
		}
	}
	return out
}

// requireStates asserts that every placement in the match is in
// exactly the given state.
func requireStates(t *testing.T, m *Module, phaseID uint, match int, want MatchState) {
	t.Helper()
	got := statesForMatch(t, m, phaseID, match)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("match %d states = %v, want exactly %v", match, got, want)
	}
}

func settings(t *testing.T, m *Module) *ClockSettings {
	t.Helper()
	cs, err := m.clockSettings(context.Background())
	if err != nil {
		t.Fatalf("clockSettings: %v", err)
	}
	return cs
}

// TestComputeClockTargets is a pure unit test of the target
// selection: override (operator pointer) wins when it is a
// non-terminal match, otherwise the lowest non-terminal match is
// current and the next-lowest is queued.
func TestComputeClockTargets(t *testing.T) {
	cases := []struct {
		name     string
		override int
		nums     []int
		cur      int
		next     int
	}{
		{"no override, three matches", 0, []int{1, 2, 3}, 1, 2},
		{"no override, single match", 0, []int{5}, 5, 0},
		{"no override, none", 0, []int{}, 0, 0},
		{"override picks middle", 2, []int{1, 2, 3}, 2, 3},
		{"override picks last, no follow", 3, []int{1, 2, 3}, 3, 0},
		{"override absent falls back to lowest", 4, []int{1, 2, 3}, 1, 2},
		{"override zero is auto", 0, []int{7, 9}, 7, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cur, next := computeClockTargets(tc.override, tc.nums)
			if cur != tc.cur || next != tc.next {
				t.Errorf("computeClockTargets(%d, %v) = (%d, %d), want (%d, %d)",
					tc.override, tc.nums, cur, next, tc.cur, tc.next)
			}
		})
	}
}

// TestClockMatchStartEndAuto walks the full lifecycle with
// transitions enabled: start moves the lowest match to Running and
// the next to Queued, cancel reverts both, the next start picks up
// the queued match, and auto-end makes it Scorable.
func TestClockMatchStartEndAuto(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	m, _ := newTestModule(t, web.EventBus(bus))
	seedClockSettingsWithTransitions(t, m, 200*time.Millisecond, 100*time.Millisecond, true)
	phase := seedPhasePlacements(t, m, []int{1, 2, 3}, MatchStateScheduled)

	ch, err := bus.Subscribe(nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// First start: 1 Running, 2 Queued, 3 untouched.
	if !startClock(t, m) {
		t.Fatal("start failed")
	}
	requireStates(t, m, phase.ID, 1, MatchStateRunning)
	requireStates(t, m, phase.ID, 2, MatchStateQueued)
	requireStates(t, m, phase.ID, 3, MatchStateScheduled)
	if cs := settings(t, m); cs.NextMatch != 2 {
		t.Errorf("NextMatch = %d after start, want 2", cs.NextMatch)
	}
	recvEvent(t, ch, eventTypeClockStart, 2*time.Second)

	// Cancel reverts to Scheduled; the pointer stays.
	if !cancelClock(t, m) {
		t.Fatal("cancel failed")
	}
	requireStates(t, m, phase.ID, 1, MatchStateScheduled)
	requireStates(t, m, phase.ID, 2, MatchStateScheduled)
	requireStates(t, m, phase.ID, 3, MatchStateScheduled)
	if cs := settings(t, m); cs.NextMatch != 2 {
		t.Errorf("NextMatch = %d after cancel, want 2", cs.NextMatch)
	}
	recvEvent(t, ch, eventTypeClockCancel, 2*time.Second)

	// Second start picks up the queued match 2: 2 Running, 3 Queued.
	if !startClock(t, m) {
		t.Fatal("second start failed")
	}
	requireStates(t, m, phase.ID, 2, MatchStateRunning)
	requireStates(t, m, phase.ID, 3, MatchStateQueued)
	requireStates(t, m, phase.ID, 1, MatchStateScheduled)
	if cs := settings(t, m); cs.NextMatch != 3 {
		t.Errorf("NextMatch = %d after second start, want 3", cs.NextMatch)
	}

	// Auto-end at EndTime moves the running match to Scorable.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if getClock(t, m) == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	requireNoActiveClock(t, m)
	recvEvent(t, ch, eventTypeClockEnd, 2*time.Second)
	requireStates(t, m, phase.ID, 2, MatchStateScorable)
	requireStates(t, m, phase.ID, 3, MatchStateQueued)
}

// TestClockMatchOverride verifies the operator's next-match pointer
// steers the start: setting the pointer to 1 (already used by
// auto-advance) is ignored once cleared, and an explicit set makes
// that match the current one.
func TestClockMatchOverride(t *testing.T) {
	m, _ := newTestModule(t)
	seedClockSettingsWithTransitions(t, m, time.Hour, 30*time.Second, true)
	phase := seedPhasePlacements(t, m, []int{1, 2, 3}, MatchStateScheduled)

	// Set the pointer to 2 via the handler.
	req := httptest.NewRequest(http.MethodPost, "/ui/mod/game/timekeeping/next-match", nil)
	req.ParseForm()
	req.Form.Set("match", "2")
	rec := httptest.NewRecorder()
	m.uiViewTimekeepingSetNextMatch(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("set-next-match = %d, want 303", rec.Code)
	}
	cs := settings(t, m)
	if cs.NextMatch != 2 || cs.NextMatchPhase != phase.ID {
		t.Fatalf("NextMatch = (%d, %d), want (2, %d)", cs.NextMatch, cs.NextMatchPhase, phase.ID)
	}

	// Start honours the override: 2 Running, 3 Queued, 1 untouched.
	if !startClock(t, m) {
		t.Fatal("start failed")
	}
	requireStates(t, m, phase.ID, 2, MatchStateRunning)
	requireStates(t, m, phase.ID, 3, MatchStateQueued)
	requireStates(t, m, phase.ID, 1, MatchStateScheduled)

	// Cancel reverts everything.
	if !cancelClock(t, m) {
		t.Fatal("cancel failed")
	}
	for _, match := range []int{1, 2, 3} {
		requireStates(t, m, phase.ID, match, MatchStateScheduled)
	}
}

// TestClockMatchDisabled verifies that with the flag off the clock
// never touches match states or the pointer.
func TestClockMatchDisabled(t *testing.T) {
	m, _ := newTestModule(t)
	seedClockSettingsWithTransitions(t, m, time.Hour, 30*time.Second, false)
	phase := seedPhasePlacements(t, m, []int{1, 2}, MatchStateScheduled)

	if !startClock(t, m) {
		t.Fatal("start failed")
	}
	for _, match := range []int{1, 2} {
		requireStates(t, m, phase.ID, match, MatchStateScheduled)
	}
	if cs := settings(t, m); cs.NextMatch != 0 {
		t.Errorf("NextMatch = %d with transitions off, want 0", cs.NextMatch)
	}

	if !cancelClock(t, m) {
		t.Fatal("cancel failed")
	}
	for _, match := range []int{1, 2} {
		requireStates(t, m, phase.ID, match, MatchStateScheduled)
	}
}

// TestClockMatchStartErrors covers the rejection paths: no active
// phase, no non-terminal match, and an invalid next-match value.
func TestClockMatchStartErrors(t *testing.T) {
	t.Run("no active phase", func(t *testing.T) {
		m, _ := newTestModule(t)
		seedClockSettingsWithTransitions(t, m, time.Hour, 30*time.Second, true)
		if startClock(t, m) {
			t.Fatal("start should have been rejected without an active phase")
		}
		requireNoActiveClock(t, m)
	})

	t.Run("all matches terminal", func(t *testing.T) {
		m, _ := newTestModule(t)
		seedClockSettingsWithTransitions(t, m, time.Hour, 30*time.Second, true)
		seedPhasePlacements(t, m, []int{1, 2}, MatchStateComplete)

		req := httptest.NewRequest(http.MethodPost, "/ui/mod/game/timekeeping/start", nil)
		rec := httptest.NewRecorder()
		m.uiViewTimekeepingStart(rec, req)
		if rec.Code == http.StatusSeeOther {
			t.Fatal("start should have been rejected when every match is terminal")
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("start with no matches remaining = %d, want 400", rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, "no matches remain in this phase") {
			t.Errorf("error page missing the no-matches-remaining message:\n%s", body)
		}
		requireNoActiveClock(t, m)
	})

	t.Run("negative next match", func(t *testing.T) {
		m, _ := newTestModule(t)
		seedClockSettingsWithTransitions(t, m, time.Hour, 30*time.Second, true)
		seedPhasePlacements(t, m, []int{1}, MatchStateScheduled)

		req := httptest.NewRequest(http.MethodPost, "/ui/mod/game/timekeeping/next-match", nil)
		req.ParseForm()
		req.Form.Set("match", "-1")
		rec := httptest.NewRecorder()
		m.uiViewTimekeepingSetNextMatch(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("set-next-match(-1) = %d, want 400", rec.Code)
		}
		if cs := settings(t, m); cs.NextMatch != 0 {
			t.Errorf("NextMatch = %d after rejected set, want 0", cs.NextMatch)
		}
	})

	t.Run("non-numeric next match", func(t *testing.T) {
		m, _ := newTestModule(t)
		seedClockSettingsWithTransitions(t, m, time.Hour, 30*time.Second, true)
		seedPhasePlacements(t, m, []int{1}, MatchStateScheduled)

		req := httptest.NewRequest(http.MethodPost, "/ui/mod/game/timekeeping/next-match", nil)
		req.ParseForm()
		req.Form.Set("match", "abc")
		rec := httptest.NewRecorder()
		m.uiViewTimekeepingSetNextMatch(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("set-next-match(abc) = %d, want 400", rec.Code)
		}
	})

	t.Run("next match without active phase", func(t *testing.T) {
		m, _ := newTestModule(t)
		seedClockSettingsWithTransitions(t, m, time.Hour, 30*time.Second, true)

		req := httptest.NewRequest(http.MethodPost, "/ui/mod/game/timekeeping/next-match", nil)
		req.ParseForm()
		req.Form.Set("match", "1")
		rec := httptest.NewRecorder()
		m.uiViewTimekeepingSetNextMatch(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("set-next-match without active phase = %d, want 400", rec.Code)
		}
	})
}

// TestClockMatchScorableUntouched verifies that a match already in a
// post-run state is not pulled back into the running cycle.
func TestClockMatchScorableUntouched(t *testing.T) {
	m, _ := newTestModule(t)
	seedClockSettingsWithTransitions(t, m, time.Hour, 30*time.Second, true)
	phase := seedPhasePlacements(t, m, []int{1, 2}, MatchStateScheduled)

	// Manually park match 1 as Scorable (as a finished run would).
	if _, err := m.setMatchState(context.Background(), phase.ID, 1, MatchStateScorable); err != nil {
		t.Fatalf("set scorable: %v", err)
	}

	if !startClock(t, m) {
		t.Fatal("start failed")
	}
	requireStates(t, m, phase.ID, 1, MatchStateScorable)
	requireStates(t, m, phase.ID, 2, MatchStateRunning)

	if !cancelClock(t, m) {
		t.Fatal("cancel failed")
	}
	requireStates(t, m, phase.ID, 2, MatchStateScheduled)
	requireStates(t, m, phase.ID, 1, MatchStateScorable)
}

// TestClockMatchStaleReset verifies that stale Running/Queued
// placements in other matches are normalized to Scheduled on start.
func TestClockMatchStaleReset(t *testing.T) {
	m, _ := newTestModule(t)
	seedClockSettingsWithTransitions(t, m, time.Hour, 30*time.Second, true)
	phase := seedPhasePlacements(t, m, []int{1, 2, 3}, MatchStateScheduled)

	// Simulate a crashed run that left match 3 running and match 1 queued.
	if _, err := m.setMatchState(context.Background(), phase.ID, 3, MatchStateRunning); err != nil {
		t.Fatalf("set stale running: %v", err)
	}
	if _, err := m.setMatchState(context.Background(), phase.ID, 1, MatchStateQueued); err != nil {
		t.Fatalf("set stale queued: %v", err)
	}

	if !startClock(t, m) {
		t.Fatal("start failed")
	}
	// Auto-pick: lowest non-terminal is 1 (stale Queued), so 1 is the
	// current match and 2 the next.  Match 3's stale Running is
	// reset to Scheduled.
	requireStates(t, m, phase.ID, 1, MatchStateRunning)
	requireStates(t, m, phase.ID, 2, MatchStateQueued)
	requireStates(t, m, phase.ID, 3, MatchStateScheduled)
}

// TestClockPageTransitionsPanel verifies the settings panel renders
// with the right control state.
func TestClockPageTransitionsPanel(t *testing.T) {
	m, _ := newTestModule(t)
	seedClockSettingsWithTransitions(t, m, time.Hour, 30*time.Second, true)
	phase := seedPhasePlacements(t, m, []int{1}, MatchStateScheduled)

	// Set the pointer so the rendered input is non-zero.
	if err := m.setClockNextMatch(context.Background(), phase.ID, 1); err != nil {
		t.Fatalf("setClockNextMatch: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ui/mod/game/timekeeping/", nil).WithContext(timekeeperContext())
	rec := httptest.NewRecorder()
	m.uiViewTimekeeping(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /timekeeping = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`Clock match transitions`,
		`action="timekeeping/transitions"`,
		`action="timekeeping/next-match"`,
		`name="transitions" value="on" checked`,
		`name="match" min="0" value="1"`,
	} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Errorf("page missing %q", want)
		}
	}

	// The checkbox toggle handler round-trips through the settings row.
	treq := httptest.NewRequest(http.MethodPost, "/ui/mod/game/timekeeping/transitions", nil)
	treq.ParseForm() // unchecked box sends no field
	trec := httptest.NewRecorder()
	m.uiViewTimekeepingTransitions(trec, treq)
	if trec.Code != http.StatusSeeOther {
		t.Fatalf("transitions POST = %d, want 303", trec.Code)
	}
	if cs := settings(t, m); cs.Transitions {
		t.Error("Transitions should be off after unchecking")
	}
	treq2 := httptest.NewRequest(http.MethodPost, "/ui/mod/game/timekeeping/transitions", nil)
	treq2.ParseForm()
	treq2.Form.Set("transitions", "on")
	trec2 := httptest.NewRecorder()
	m.uiViewTimekeepingTransitions(trec2, treq2)
	if trec2.Code != http.StatusSeeOther {
		t.Fatalf("transitions POST (on) = %d, want 303", trec2.Code)
	}
	if cs := settings(t, m); !cs.Transitions {
		t.Error("Transitions should be on after checking")
	}
}
