package game

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/event"
	"github.com/gizmo-platform/gameday/pkg/web"
)

func seedClockSettings(t *testing.T, m *Module, duration, hurry time.Duration) {
	t.Helper()
	if err := db.InsertOrUpdate[ClockSettings](context.Background(), m.db.DB, &ClockSettings{
		ID:       ClockSettingsID,
		Duration: duration,
		Hurry:    hurry,
	}); err != nil {
		t.Fatalf("seed clock settings: %v", err)
	}
}

// startClock issues a start request and reports whether it succeeded
// (redirect) or failed (error page).
func startClock(t *testing.T, m *Module) bool {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/ui/mod/game/timekeeping/start", nil)
	rec := httptest.NewRecorder()
	m.uiViewTimekeepingStart(rec, req)
	return rec.Code == http.StatusSeeOther
}

// cancelClock issues a cancel request and reports whether it
// succeeded (redirect) or failed (error page).
func cancelClock(t *testing.T, m *Module) bool {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/ui/mod/game/timekeeping/cancel", nil)
	rec := httptest.NewRecorder()
	m.uiViewTimekeepingCancel(rec, req)
	return rec.Code == http.StatusSeeOther
}

// timekeeperContext builds a request context carrying a profile with
// the GAME:TIMEKEEPER grant, so hasPermission template checks pass.
func timekeeperContext() context.Context {
	prof := web.Profile{
		Username:    "tester",
		Permissions: []web.Permission{{Module: ModuleName, Grant: PermissionTimekeeper}},
	}
	return context.WithValue(context.Background(), web.ProfileKey{}, prof)
}

func getClock(t *testing.T, m *Module) *ActiveClock {
	t.Helper()
	ac, err := m.activeClock(context.Background())
	if err != nil {
		t.Fatalf("activeClock: %v", err)
	}
	return ac
}

func requireNoActiveClock(t *testing.T, m *Module) {
	t.Helper()
	if ac := getClock(t, m); ac != nil {
		t.Fatalf("expected no active clock, got %+v", ac)
	}
}

func requireActiveClock(t *testing.T, m *Module) *ActiveClock {
	t.Helper()
	ac := getClock(t, m)
	if ac == nil {
		t.Fatal("expected an active clock, got none")
	}
	return ac
}

func TestClockYAMLParse(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "test_data", "2p_with_objective.yml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if c.Clock.Duration != 150*time.Second {
		t.Errorf("Duration = %v, want 2m30s", c.Clock.Duration)
	}
	if c.Clock.Hurry != 30*time.Second {
		t.Errorf("Hurry = %v, want 30s", c.Clock.Hurry)
	}

	// A fixture without a clock: section must parse to zero values.
	b, err = os.ReadFile(filepath.Join("..", "..", "test_data", "2p_no_objective.yml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var plain Config
	if err := yaml.Unmarshal(b, &plain); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if plain.Clock != (ClockConfig{}) {
		t.Errorf("Clock = %+v, want zero", plain.Clock)
	}
}

func TestClockSettingsGormRoundTrip(t *testing.T) {
	m, _ := newTestModule(t)
	want := time.Duration(150*1e9 + 12345) // odd value to catch truncation
	if err := db.InsertOrUpdate[ClockSettings](context.Background(), m.db.DB, &ClockSettings{
		ID:       ClockSettingsID,
		Duration: want,
		Hurry:    30 * time.Second,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := m.clockSettings(context.Background())
	if err != nil {
		t.Fatalf("clockSettings: %v", err)
	}
	if got.Duration != want {
		t.Errorf("Duration = %v, want %v", got.Duration, want)
	}
	if got.Hurry != 30*time.Second {
		t.Errorf("Hurry = %v, want 30s", got.Hurry)
	}
}

func TestClockStartCancel(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	m, _ := newTestModule(t, web.EventBus(bus))
	seedClockSettings(t, m, time.Hour, 30*time.Second)

	ch, err := bus.Subscribe(nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if !startClock(t, m) {
		t.Fatal("start failed")
	}

	ac := requireActiveClock(t, m)
	if diff := ac.EndTime.Sub(ac.StartTime); diff != time.Hour {
		t.Errorf("EndTime-StartTime = %v, want 1h", diff)
	}
	wantHurry := ac.EndTime.Add(-30 * time.Second)
	if !ac.HurryTime.Equal(wantHurry) {
		t.Errorf("HurryTime = %v, want %v", ac.HurryTime, wantHurry)
	}

	startEnv := recvEvent(t, ch, eventTypeClockStart, 2*time.Second)
	var start clockEvent
	if err := json.Unmarshal(startEnv.Data, &start); err != nil {
		t.Fatalf("unmarshal start data: %v", err)
	}
	if !start.StartTime.Equal(ac.StartTime) || !start.EndTime.Equal(ac.EndTime) || !start.HurryTime.Equal(ac.HurryTime) {
		t.Errorf("start event payload %+v does not match clock %+v", start, ac)
	}

	if !cancelClock(t, m) {
		t.Fatal("cancel failed")
	}
	requireNoActiveClock(t, m)

	cancelEnv := recvEvent(t, ch, eventTypeClockCancel, 2*time.Second)
	var cancel clockCancelEvent
	if err := json.Unmarshal(cancelEnv.Data, &cancel); err != nil {
		t.Fatalf("unmarshal cancel data: %v", err)
	}
	if cancel.CancelTime.IsZero() {
		t.Error("cancel_time is zero")
	}
	if !cancel.StartTime.Equal(ac.StartTime) {
		t.Errorf("cancel start_time = %v, want %v", cancel.StartTime, ac.StartTime)
	}

	// No hurry or end events may have fired for a cancelled clock.
	waitNoEvent(t, ch, 250*time.Millisecond)
}

func TestClockAutoStop(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	m, _ := newTestModule(t, web.EventBus(bus))
	seedClockSettings(t, m, 200*time.Millisecond, 100*time.Millisecond)

	ch, err := bus.Subscribe(nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if !startClock(t, m) {
		t.Fatal("start failed")
	}
	ac := requireActiveClock(t, m)
	recvEvent(t, ch, eventTypeClockStart, 2*time.Second)

	recvEvent(t, ch, eventTypeClockHurry, 2*time.Second)

	// The row must be gone by EndTime.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if getClock(t, m) == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	requireNoActiveClock(t, m)

	endEnv := recvEvent(t, ch, eventTypeClockEnd, 2*time.Second)
	var end clockEvent
	if err := json.Unmarshal(endEnv.Data, &end); err != nil {
		t.Fatalf("unmarshal end data: %v", err)
	}
	if !end.EndTime.Equal(ac.EndTime) {
		t.Errorf("end event end_time = %v, want %v", end.EndTime, ac.EndTime)
	}

	// The row deletion also means a fresh start succeeds immediately.
	if !startClock(t, m) {
		t.Fatal("second start failed after auto-stop")
	}
	requireActiveClock(t, m)
}

func TestClockStartWhileRunning(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	m, _ := newTestModule(t, web.EventBus(bus))
	seedClockSettings(t, m, time.Hour, 30*time.Second)

	ch, err := bus.Subscribe(nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if !startClock(t, m) {
		t.Fatal("start failed")
	}
	recvEvent(t, ch, eventTypeClockStart, 2*time.Second)

	if startClock(t, m) {
		t.Fatal("second start should have been rejected")
	}

	// The original run must be untouched and still running.
	ac := requireActiveClock(t, m)
	if !activeClockRunning(ac, time.Now()) {
		t.Error("original clock should still be running")
	}
}

func TestClockCancelWhenIdle(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	m, _ := newTestModule(t, web.EventBus(bus))
	seedClockSettings(t, m, time.Hour, 30*time.Second)

	ch, _ := bus.Subscribe(nil)

	if cancelClock(t, m) {
		t.Fatal("cancel should have been rejected while idle")
	}
	waitNoEvent(t, ch, 250*time.Millisecond)
}

func TestClockStartUnconfigured(t *testing.T) {
	m, _ := newTestModule(t)

	if startClock(t, m) {
		t.Fatal("start should have been rejected without configuration")
	}
}

func TestClockStartInvalidSettings(t *testing.T) {
	m, _ := newTestModule(t)
	seedClockSettings(t, m, 0, 0)
	if startClock(t, m) {
		t.Fatal("start should have been rejected for zero duration")
	}

	m2, _ := newTestModule(t)
	seedClockSettings(t, m2, 30*time.Second, 30*time.Second)
	if startClock(t, m2) {
		t.Fatal("start should have been rejected when hurry >= duration")
	}
}

func TestClockPageRenders(t *testing.T) {
	m, _ := newTestModule(t)
	seedClockSettings(t, m, 150*time.Second, 30*time.Second)

	req := httptest.NewRequest(http.MethodGet, "/ui/mod/game/timekeeping/", nil).WithContext(timekeeperContext())
	rec := httptest.NewRecorder()
	m.uiViewTimekeeping(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /timekeeping = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`id="clock"`, `data-running="false"`, `data-duration="150000"`, `>Start<`} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Errorf("page missing %q", want)
		}
	}

	if !startClock(t, m) {
		t.Fatal("start failed")
	}
	rec = httptest.NewRecorder()
	m.uiViewTimekeeping(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /timekeeping (running) = %d, want 200", rec.Code)
	}
	body = rec.Body.String()
	for _, want := range []string{`data-running="true"`, `id="cancel-btn"`, `id="cancel-modal"`} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestClockCancelAfterExpiry(t *testing.T) {
	m, _ := newTestModule(t)
	seedClockSettings(t, m, 30*time.Millisecond, 10*time.Millisecond)

	if !startClock(t, m) {
		t.Fatal("start failed")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if getClock(t, m) == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	requireNoActiveClock(t, m)

	// An expired state is idle: cancel must be rejected.
	if cancelClock(t, m) {
		t.Fatal("cancel after expiry should be rejected")
	}

	// ...and a new start must succeed.
	if !startClock(t, m) {
		t.Fatal("start after expiry should succeed")
	}
}

// TestClockPageDrivesFromEvents verifies the server-side contract the
// event-bus-driven clock fragment depends on: the page renders the
// correct initial state, the start/end/cancel payloads carry the
// times the fragment keys on (end_time identity), and the replay ring
// lets a late-joining client recover recent state.
func TestClockPageDrivesFromEvents(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	m, _ := newTestModule(t, web.EventBus(bus))
	seedClockSettings(t, m, 200*time.Millisecond, 100*time.Millisecond)

	ch, err := bus.Subscribe(nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	page := func() string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/ui/mod/game/timekeeping/", nil).WithContext(timekeeperContext())
		rec := httptest.NewRecorder()
		m.uiViewTimekeeping(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /timekeeping = %d, want 200", rec.Code)
		}
		return rec.Body.String()
	}

	// Idle page: start control enabled, cancel control present but
	// shown only when running, duration in milliseconds.
	body := page()
	for _, want := range []string{
		`id="clock"`,
		`data-running="false"`,
		`data-duration="200"`,
		`data-end="0"`,
		`id="start-form"`,
		`type="submit">Start<`,
		`id="cancel-wrap"`,
		`id="cancel-btn"`,
	} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Errorf("idle page missing %q", want)
		}
	}

	if !startClock(t, m) {
		t.Fatal("start failed")
	}
	ac := requireActiveClock(t, m)

	startEnv := recvEvent(t, ch, eventTypeClockStart, 2*time.Second)
	var se clockEvent
	if err := json.Unmarshal(startEnv.Data, &se); err != nil {
		t.Fatalf("unmarshal start data: %v", err)
	}
	if !se.StartTime.Equal(ac.StartTime) || !se.EndTime.Equal(ac.EndTime) || !se.HurryTime.Equal(ac.HurryTime) {
		t.Errorf("start payload %+v does not match clock %+v", se, ac)
	}

	body = page()
	for _, want := range []string{
		`data-running="true"`,
		`id="start-form"`,
		`id="cancel-btn"`,
	} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Errorf("running page missing %q", want)
		}
	}
	// The fragment adopts the running state from the rendered row on
	// load, so the start/end/hurry epoch-ms must be present too.
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`data-start="[0-9]+"`),
		regexp.MustCompile(`data-end="[0-9]+"`),
		regexp.MustCompile(`data-hurry="[0-9]+"`),
	} {
		if !re.MatchString(body) {
			t.Errorf("running page missing %s", re.String())
		}
	}

	// Wait for the auto-stop to remove the row.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if getClock(t, m) == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	requireNoActiveClock(t, m)

	endEnv := recvEvent(t, ch, eventTypeClockEnd, 2*time.Second)
	var ee clockEvent
	if err := json.Unmarshal(endEnv.Data, &ee); err != nil {
		t.Fatalf("unmarshal end data: %v", err)
	}
	if !ee.EndTime.Equal(ac.EndTime) {
		t.Errorf("end payload end_time = %v, want %v", ee.EndTime, ac.EndTime)
	}

	// The cancel path must publish the same payload contract.
	if !startClock(t, m) {
		t.Fatal("start after auto-stop failed")
	}
	ac2 := requireActiveClock(t, m)
	if !cancelClock(t, m) {
		t.Fatal("cancel failed")
	}
	requireNoActiveClock(t, m)

	cancelEnv := recvEvent(t, ch, eventTypeClockCancel, 2*time.Second)
	var ce clockCancelEvent
	if err := json.Unmarshal(cancelEnv.Data, &ce); err != nil {
		t.Fatalf("unmarshal cancel data: %v", err)
	}
	if !ce.EndTime.Equal(ac2.EndTime) || ce.CancelTime.IsZero() {
		t.Errorf("cancel payload %+v does not match clock %+v", ce, ac2)
	}

	// After the cancel the page renders idle again.
	body = page()
	if !bytes.Contains([]byte(body), []byte(`data-running="false"`)) {
		t.Error("page after cancel should render idle")
	}

	// A late-joining client must be able to recover the recent state
	// from the replay ring (the fragment requests replay=8).
	seen := map[string]bool{}
	for _, env := range bus.Replay(8) {
		seen[env.Type] = true
	}
	for _, typ := range []string{eventTypeClockStart, eventTypeClockEnd, eventTypeClockCancel} {
		if !seen[typ] {
			t.Errorf("replay ring missing %q", typ)
		}
	}
}

// recvEvent waits for an envelope of the given type with a timeout.
func recvEvent(t *testing.T, ch <-chan event.Envelope, typ string, timeout time.Duration) event.Envelope {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("timed out waiting for %q", typ)
		}
		select {
		case env, ok := <-ch:
			if !ok {
				t.Fatalf("event channel closed while waiting for %q", typ)
			}
			if env.Type == typ {
				return env
			}
			t.Logf("skipping unexpected event %q while waiting for %q", env.Type, typ)
		case <-time.After(remaining):
			t.Fatalf("timed out waiting for %q", typ)
		}
	}
}

// waitNoEvent fails if any event arrives within the window.
func waitNoEvent(t *testing.T, ch <-chan event.Envelope, window time.Duration) {
	t.Helper()
	select {
	case env, ok := <-ch:
		if ok {
			t.Fatalf("unexpected event %q within %v", env.Type, window)
		}
	case <-time.After(window):
	}
}
