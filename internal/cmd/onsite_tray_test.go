package cmd

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gizmo-platform/gameday/pkg/announcer"
	"github.com/gizmo-platform/gameday/pkg/sfx"
	"github.com/gogpu/systray"
)

// listener records every listen call and signals when the context a
// listener was given has been cancelled.
type listener struct {
	mu        sync.Mutex
	started   int
	stopped   int
	cancelled chan struct{}
}

func newListener() *listener {
	return &listener{cancelled: make(chan struct{}, 4)}
}

func (l *listener) listen(ctx context.Context) {
	l.mu.Lock()
	l.started++
	l.mu.Unlock()

	<-ctx.Done()

	l.mu.Lock()
	l.stopped++
	l.mu.Unlock()
	select {
	case l.cancelled <- struct{}{}:
	default:
	}
}

func (l *listener) counts() (started, stopped int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.started, l.stopped
}

// waitStarted blocks until the listener has been started at least want
// times.
func (l *listener) waitStarted(t *testing.T, want int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if started, _ := l.counts(); started >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	started, _ := l.counts()
	t.Fatalf("listener started %d times, want %d", started, want)
}

func TestClockPlayerStartsOn(t *testing.T) {
	l := newListener()
	c := startClockPlayer(context.Background(), l.listen)
	defer c.stop()

	if !c.CanRun() {
		t.Error("a switch with a listener should be able to run")
	}
	if !c.IsOn() {
		t.Error("the switch should start on, matching the checked menu box")
	}
	l.waitStarted(t, 1)
}

func TestClockPlayerToggleStopsAndRestartsTheListener(t *testing.T) {
	l := newListener()
	c := startClockPlayer(context.Background(), l.listen)
	defer c.stop()
	l.waitStarted(t, 1)

	// First toggle: off.  The listener's context must be cancelled.
	if on := c.toggle(); on {
		t.Error("toggle from on should turn sounds off")
	}
	if c.IsOn() {
		t.Error("IsOn should report off")
	}
	select {
	case <-l.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("turning the switch off did not stop the listener")
	}

	// Second toggle: on again, which starts a fresh listener rather than
	// reviving the cancelled one.
	if on := c.toggle(); !on {
		t.Error("toggle from off should turn sounds back on")
	}
	if !c.IsOn() {
		t.Error("IsOn should report on after two toggles")
	}
	l.waitStarted(t, 2)
}

func TestClockPlayerWithoutAListenerIsOff(t *testing.T) {
	c := &clockPlayer{}
	if c.CanRun() {
		t.Error("a switch with nothing to announce should not report it can run")
	}
	if c.IsOn() {
		t.Error("nothing is being announced")
	}
	// Toggling must stay off and must not panic.
	if on := c.toggle(); on {
		t.Error("toggling a switch with nothing to announce should stay off")
	}
	c.stop()
}

func TestClockPlayerCancellingTheAppletStopsTheListener(t *testing.T) {
	ctx, cancelApplet := context.WithCancel(context.Background())
	l := newListener()
	c := startClockPlayer(ctx, l.listen)
	defer c.stop()
	l.waitStarted(t, 1)

	cancelApplet()
	select {
	case <-l.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("the applet ending did not stop the listener")
	}
}

func TestClockPlayerStopWaitsForTheListener(t *testing.T) {
	l := newListener()
	c := startClockPlayer(context.Background(), l.listen)
	l.waitStarted(t, 1)

	c.stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, stopped := l.counts(); stopped == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("stop returned before the listener had finished")
}

func TestNewClockPlayerRejectsAnUnusableServer(t *testing.T) {
	// The tray must come up as an indicator when it was pointed at
	// something it cannot listen to, rather than retrying forever behind
	// a menu box that can never do anything.
	t.Setenv(announcer.EnvServer, "not a url")

	c := newClockPlayer(context.Background(), nil)
	if c.CanRun() {
		t.Error("an unusable server should leave the tray with nothing to toggle")
	}
}

func TestNewClockPlayerAcceptsAUsableServer(t *testing.T) {
	t.Setenv(announcer.EnvServer, "http://127.0.0.1:8080")

	c := newClockPlayer(context.Background(), nil)
	defer c.stop()

	if !c.CanRun() {
		t.Error("a usable server should give the tray something to toggle")
	}
	// The announcer retries in the background, so stopping the switch is
	// what ends it here.
	if !c.IsOn() {
		t.Error("the switch should start on")
	}
}

func TestTitleCase(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"start-of-match", "Start Of Match"},
		{"end-of-match", "End Of Match"},
		{"match-almost-over", "Match Almost Over"},
		{"match-fault", "Match Fault"},
		{"beep", "Beep"},
		{"", ""},
	}
	for _, c := range cases {
		if got := titleCase(c.name); got != c.want {
			t.Errorf("titleCase(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTestSoundTitlesAreReadable(t *testing.T) {
	// The test entries are what an operator uses to check levels, so
	// every shipped sound must get a label free of file-name punctuation.
	for _, name := range []string{
		"end-of-match",
		"match-almost-over",
		"match-fault",
		"start-of-match",
	} {
		title := titleCase(name)
		if strings.ContainsAny(title, "-._") {
			t.Errorf("titleCase(%q) = %q, want no file-name punctuation", name, title)
		}
	}
}

// TestTestSoundLabelSaysWhatChoosingItDoes covers the label the entries
// share the menu with the other controls are given.
func TestTestSoundLabelSaysWhatChoosingItDoes(t *testing.T) {
	if got, want := testSoundLabel(sfx.StartOfMatch), "Play Start Of Match"; got != want {
		t.Errorf("testSoundLabel(%q) = %q, want %q", sfx.StartOfMatch, got, want)
	}
}

// TestAddTestSoundItemsAddsOneEntryPerSound guards the reason the sounds
// are top-level entries rather than a submenu: a submenu's contents are up
// to the tray host, and a host that never fetches them leaves the operator
// with an empty menu and no sound.  Every sound must therefore be added to
// the menu it is given, and the count reported so the caller knows whether
// a separator is warranted.
func TestAddTestSoundItemsAddsOneEntryPerSound(t *testing.T) {
	player := sfxTestPlayer(t)

	if got, want := addTestSoundItems(systray.NewMenu(), player), len(player.Sounds()); got != want {
		t.Errorf("added %d entries, want %d", got, want)
	}
}

// TestAddTestSoundItemsReportsAnEmptyLibrary checks a library with no
// sounds adds nothing, so the menu skips the separator rather than
// announcing a group of test entries that cannot exist.
func TestAddTestSoundItemsReportsAnEmptyLibrary(t *testing.T) {
	empty := sfx.New(sfx.WithFS(fstest.MapFS{}))

	if got := addTestSoundItems(systray.NewMenu(), empty); got != 0 {
		t.Errorf("added %d entries for an empty library, want 0", got)
	}
}
