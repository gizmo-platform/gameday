package game

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"time"

	"github.com/flosch/pongo2/v6"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/event"
)

const (
	// ClockSettingsID is the fixed primary key of the clock
	// settings singleton.
	ClockSettingsID = 1

	// ActiveClockID is the fixed primary key of the active clock
	// singleton.
	ActiveClockID = 1

	// eventTypeClockStart is published when a clock run begins.
	eventTypeClockStart = "game.clock.start"

	// eventTypeClockHurry is published when a running clock enters
	// its hurry window.
	eventTypeClockHurry = "game.clock.hurry"

	// eventTypeClockEnd is published when a running clock expires on
	// its own.
	eventTypeClockEnd = "game.clock.end"

	// eventTypeClockCancel is published when a running clock is
	// cancelled by the timekeeper.
	eventTypeClockCancel = "game.clock.cancel"
)

// errClockNotConfigured is returned when the clock settings singleton
// does not exist yet (the setup file has no clock: section).
var errClockNotConfigured = errors.New("clock not configured")

// ClockSettings is the persistent configuration of the match clock,
// loaded from the `clock:` section of the game setup file.  It is
// stored as a singleton row.
type ClockSettings struct {
	ID uint `gorm:"primaryKey"`

	// Duration is the total length of a clock run.
	Duration time.Duration

	// Hurry is the window before the end of the clock that triggers
	// the hurry state.
	Hurry time.Duration
}

// ActiveClock is the running-state of the match clock.  It is stored
// as a singleton row; its presence (with an unexpired EndTime) means
// a clock is currently running.
type ActiveClock struct {
	ID uint `gorm:"primaryKey"`

	// StartTime is when the clock was started.
	StartTime time.Time

	// EndTime is when the clock expires (StartTime + Duration).
	EndTime time.Time

	// HurryTime is when the hurry window began (EndTime - Hurry).
	HurryTime time.Time
}

// clockEvent is the base payload for all clock events.
type clockEvent struct {
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	HurryTime time.Time `json:"hurry_time"`
}

// clockCancelEvent extends clockEvent with the cancel time.
type clockCancelEvent struct {
	clockEvent
	CancelTime time.Time `json:"cancel_time"`
}

// clockEventFromClock builds the event payload from an ActiveClock row.
func clockEventFromClock(ac *ActiveClock) clockEvent {
	return clockEvent{
		StartTime: ac.StartTime,
		EndTime:   ac.EndTime,
		HurryTime: ac.HurryTime,
	}
}

// clockSettings loads the clock settings singleton.
func (m *Module) clockSettings(ctx context.Context) (*ClockSettings, error) {
	cs, err := gorm.G[ClockSettings](m.db.DB).
		Where("id = ?", ClockSettingsID).
		First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errClockNotConfigured
		}
		return nil, err
	}
	return &cs, nil
}

// activeClock loads the active clock singleton, or nil if absent.
func (m *Module) activeClock(ctx context.Context) (*ActiveClock, error) {
	ac, err := gorm.G[ActiveClock](m.db.DB).
		Where("id = ?", ActiveClockID).
		First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ac, nil
}

// activeClockRunning reports whether a clock is currently running,
// i.e. a row exists and its EndTime has not passed.  Expired rows are
// treated as idle everywhere, which makes a stale row left over from
// a crash or restart harmless.
func activeClockRunning(ac *ActiveClock, now time.Time) bool {
	return ac != nil && now.Before(ac.EndTime)
}

// activeClockStill reports whether the clock row for the given run
// (matched by StartTime) is still present.  It is used by scheduled
// timers to avoid firing for runs that were cancelled or replaced.
func (m *Module) activeClockStill(ctx context.Context, ac *ActiveClock) bool {
	cur, err := m.activeClock(ctx)
	if err != nil || cur == nil {
		return false
	}
	// Equal (not ==): the row's time was round-tripped through the
	// database and lost any monotonic clock reading.
	return cur.StartTime.Equal(ac.StartTime)
}

// publishClockEvent publishes a clock event on the event bus.  A
// missing bus or a publish failure is logged and never fails the
// request.
func (m *Module) publishClockEvent(ctx context.Context, typ string, data any) {
	if m.ws == nil || m.ws.Bus() == nil {
		return
	}
	if err := m.ws.Bus().Publish(ctx, event.Event{Type: typ, Data: data}); err != nil {
		slog.Error("Failed to publish clock event", "type", typ, "error", err)
	}
}

// scheduleClockTimers arms one-shot timers for the hurry and end
// events of a newly started clock.  The end timer also performs the
// auto-stop.  Both timers re-check the running-state row before
// firing, so a cancelled or replaced run never produces a spurious
// event.
func (m *Module) scheduleClockTimers(ac *ActiveClock) {
	if delay := time.Until(ac.HurryTime); delay >= 0 {
		go func() {
			time.Sleep(delay)
			if m.activeClockStill(context.Background(), ac) {
				m.publishClockEvent(context.Background(), eventTypeClockHurry, clockEventFromClock(ac))
			}
		}()
	}
	if delay := time.Until(ac.EndTime); delay >= 0 {
		go func() {
			time.Sleep(delay)
			if m.activeClockStill(context.Background(), ac) {
				m.autoStopClock(ac.EndTime)
				m.publishClockEvent(context.Background(), eventTypeClockEnd, clockEventFromClock(ac))
			}
		}()
	}
}

// autoStopClock deletes the running-state row for the clock that
// ended at the given EndTime.  The conditional delete makes the
// operation a no-op if the row was cancelled or replaced.
func (m *Module) autoStopClock(endTime time.Time) {
	_, err := gorm.G[ActiveClock](m.db.DB).
		Where(&ActiveClock{ID: ActiveClockID, EndTime: endTime}).
		Delete(context.Background())
	if err != nil {
		slog.Error("Failed to auto-stop clock", "error", err)
	}
}

// uiViewTimekeeping renders the timekeeping page: the ticking clock
// fragment plus the start/cancel controls.
func (m *Module) uiViewTimekeeping(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()

	cs, err := m.clockSettings(ctx)
	if err != nil && !errors.Is(err, errClockNotConfigured) {
		slog.Error("Error loading clock settings", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	ac, err := m.activeClock(ctx)
	if err != nil {
		slog.Error("Error loading active clock", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	var durationMS, hurryMS, endMS, startMS int64
	if cs != nil {
		durationMS = cs.Duration.Milliseconds()
	}
	if ac != nil {
		startMS = ac.StartTime.UnixMilli()
		hurryMS = ac.HurryTime.UnixMilli()
		endMS = ac.EndTime.UnixMilli()
	}

	m.ws.DoTemplate(w, r, "views/game/timekeeping.p2", pongo2.Context{
		"configured":  cs != nil,
		"running":     activeClockRunning(ac, now),
		"duration_ms": durationMS,
		"hurry_ms":    hurryMS,
		"end_ms":      endMS,
		"start_ms":    startMS,
	})
}

// uiViewClockDisplay renders the public, display-only clock: a
// full-screen timer with no chrome or controls, intended for
// projectors and downstream compositing.
func (m *Module) uiViewClockDisplay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()

	cs, err := m.clockSettings(ctx)
	if err != nil && !errors.Is(err, errClockNotConfigured) {
		slog.Error("Error loading clock settings", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	ac, err := m.activeClock(ctx)
	if err != nil {
		slog.Error("Error loading active clock", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	var durationMS, hurryMS, endMS, startMS int64
	if cs != nil {
		durationMS = cs.Duration.Milliseconds()
	}
	if ac != nil {
		startMS = ac.StartTime.UnixMilli()
		hurryMS = ac.HurryTime.UnixMilli()
		endMS = ac.EndTime.UnixMilli()
	}

	m.ws.DoTemplate(w, r, "views/game/clock_display.p2", pongo2.Context{
		"is_standalone": true,
		"running":       activeClockRunning(ac, now),
		"duration_ms":   durationMS,
		"hurry_ms":      hurryMS,
		"end_ms":        endMS,
		"start_ms":      startMS,
	})
}

// uiViewTimekeepingStart starts a new clock run from the configured
// duration and hurry window.
func (m *Module) uiViewTimekeepingStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cs, err := m.clockSettings(ctx)
	if err != nil {
		m.renderTimekeepingError(w, r, err)
		return
	}
	if cs.Duration <= 0 {
		m.renderTimekeepingError(w, r, errors.New("clock duration must be positive"))
		return
	}
	if cs.Hurry >= cs.Duration {
		m.renderTimekeepingError(w, r, errors.New("clock hurry must be less than the duration"))
		return
	}

	ac, err := m.activeClock(ctx)
	if err != nil {
		slog.Error("Error loading active clock", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	if activeClockRunning(ac, time.Now()) {
		m.renderTimekeepingError(w, r, errors.New("a clock is already running"))
		return
	}

	start := time.Now()
	ac = &ActiveClock{
		ID:        ActiveClockID,
		StartTime: start,
		EndTime:   start.Add(cs.Duration),
		HurryTime: start.Add(cs.Duration).Add(-cs.Hurry),
	}

	// The upsert on the fixed primary key also replaces any stale
	// (expired) row left over from a crash or restart.
	if err := db.InsertOrUpdate[ActiveClock](ctx, m.db.DB, ac); err != nil {
		slog.Error("Error starting clock", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	m.scheduleClockTimers(ac)
	// Publish before the redirect: a client that follows the 303
	// re-renders the page, and the event (already in the replay
	// ring) plus the fresh DB row must both be in place by then.
	m.publishClockEvent(ctx, eventTypeClockStart, clockEventFromClock(ac))

	http.Redirect(w, r, path.Join(m.basePath, "/timekeeping"), http.StatusSeeOther)
}

// uiViewTimekeepingCancel stops a running clock.
func (m *Module) uiViewTimekeepingCancel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ac, err := m.activeClock(ctx)
	if err != nil {
		slog.Error("Error loading active clock", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	if !activeClockRunning(ac, time.Now()) {
		m.renderTimekeepingError(w, r, errors.New("no clock is running"))
		return
	}

	cancelled := clockCancelEvent{
		clockEvent: clockEventFromClock(ac),
		CancelTime: time.Now(),
	}

	// Condition the delete on the StartTime we observed so a
	// concurrent start or the expiry timer cannot make this a
	// no-op that still claims a cancel.
	affected, err := gorm.G[ActiveClock](m.db.DB).
		Where(&ActiveClock{ID: ActiveClockID, StartTime: ac.StartTime}).
		Delete(ctx)
	if err != nil {
		slog.Error("Error cancelling clock", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	if affected == 0 {
		m.renderTimekeepingError(w, r, errors.New("clock state changed before cancel"))
		return
	}

	m.publishClockEvent(ctx, eventTypeClockCancel, cancelled)

	http.Redirect(w, r, path.Join(m.basePath, "/timekeeping"), http.StatusSeeOther)
}

// renderTimekeepingError renders the clock error page for a failed
// clock operation.
func (m *Module) renderTimekeepingError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("Clock operation failed", "error", err)
	w.WriteHeader(http.StatusBadRequest)
	m.ws.DoTemplate(w, r, "views/game/clock_error.p2", pongo2.Context{
		"error": err,
		"back":  path.Join(m.basePath, "/timekeeping"),
	})
}
