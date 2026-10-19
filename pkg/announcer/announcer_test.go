package announcer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/gizmo-platform/gameday/pkg/event"
	"github.com/gizmo-platform/gameday/pkg/sfx"
)

// fakePlayer records the sounds the announcer would have made audible.
type fakePlayer struct {
	mu     sync.Mutex
	played []string
	// arrived receives every sound as it is played, for tests that wait
	// for one.
	arrived chan string
}

func newFakePlayer() *fakePlayer {
	return &fakePlayer{arrived: make(chan string, 64)}
}

func (f *fakePlayer) Play(name string) error {
	f.mu.Lock()
	f.played = append(f.played, name)
	f.mu.Unlock()
	select {
	case f.arrived <- name:
	default:
	}
	return nil
}

func (f *fakePlayer) list() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.played)
}

// waitFor blocks until name is played or the timeout passes.
func (f *fakePlayer) waitFor(t *testing.T, name string) {
	t.Helper()

	timeout := time.After(5 * time.Second)
	for {
		select {
		case got := <-f.arrived:
			if got == name {
				return
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %q, played %v", name, f.list())
		}
	}
}

// expectNothing gives the announcer a moment to play something and fails
// if it did.
func (f *fakePlayer) expectNothing(t *testing.T) {
	t.Helper()

	select {
	case got := <-f.arrived:
		t.Errorf("unexpected sound %q", got)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestSoundForMapsClockEvents(t *testing.T) {
	cases := []struct {
		eventType string
		sound     string
		ok        bool
	}{
		{"game.clock.start", sfx.StartOfMatch, true},
		{"game.clock.hurry", sfx.MatchAlmostOver, true},
		{"game.clock.end", sfx.EndOfMatch, true},
		{"game.clock.cancel", sfx.MatchFault, true},
		{"game.match.state", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		sound, ok := SoundFor(c.eventType)
		if ok != c.ok || sound != c.sound {
			t.Errorf("SoundFor(%q) = (%q, %v), want (%q, %v)",
				c.eventType, sound, ok, c.sound, c.ok)
		}
	}
}

func TestEventTypesAreTheMappedTypes(t *testing.T) {
	want := []string{
		"game.clock.cancel",
		"game.clock.end",
		"game.clock.hurry",
		"game.clock.start",
	}
	if got := EventTypes(); !slices.Equal(got, want) {
		t.Errorf("EventTypes() = %v, want %v", got, want)
	}
}

func TestAnnounceIgnoresUnmappedAndReplayedEvents(t *testing.T) {
	p := newFakePlayer()
	a := &Announcer{player: p}

	a.announce(event.Envelope{Type: "game.match.state"})
	a.announce(event.Envelope{Type: event.TypeSubscribed})
	// A replayed event describes something that already happened;
	// announcing it would confuse everyone on the field.
	a.announce(event.Envelope{Type: "game.clock.start", Replay: true})
	p.expectNothing(t)

	a.announce(event.Envelope{Type: "game.clock.start"})
	p.waitFor(t, sfx.StartOfMatch)
}

// failingPlayer reports every sound as unplayable, which must be a log
// line and nothing more.
type failingPlayer struct{}

func (failingPlayer) Play(string) error { return errors.New("no audio device") }

func TestPlayerFailuresDoNotStopTheAnnouncer(t *testing.T) {
	a := &Announcer{player: failingPlayer{}}
	b := event.New()
	defer b.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- a.watchBus(ctx, b) }()

	if err := b.Publish(ctx, event.Event{Type: "game.clock.start"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// The run survives the failure: it is still running when we cancel,
	// and returns the cancellation rather than the player's error.
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("announcer did not stop when the context ended")
	}
}

func TestBusModePlaysClockSounds(t *testing.T) {
	p := newFakePlayer()
	b := event.New()
	defer b.Close()

	a := &Announcer{player: p, bus: b}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := a.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Run: %v", err)
		}
	}()

	for _, c := range []struct {
		eventType string
		sound     string
	}{
		{"game.clock.start", sfx.StartOfMatch},
		{"game.clock.hurry", sfx.MatchAlmostOver},
		{"game.clock.end", sfx.EndOfMatch},
		{"game.clock.cancel", sfx.MatchFault},
	} {
		publishUntilPlayed(t, b, p, c.eventType, c.sound)
	}

	// Nothing may be announced twice.
	if got := p.list(); len(got) != 4 {
		t.Errorf("played %v, want the four clock sounds", got)
	}
}

func TestBusModeIgnoresOtherEventTypes(t *testing.T) {
	p := newFakePlayer()
	b := event.New()
	defer b.Close()

	a := &Announcer{player: p, bus: b}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = a.Run(ctx) }()

	// Keep publishing for a while: the announcer's subscription is
	// established asynchronously, and every one of these types must be
	// ignored whichever moment it connects.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := b.Publish(ctx, event.Event{Type: "game.match.state"}); err != nil {
			t.Fatalf("publish: %v", err)
		}
		if err := b.Publish(ctx, event.Event{Type: "team.updated"}); err != nil {
			t.Fatalf("publish: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The clock, by contrast, must be audible: this proves the previous
	// loop reached the announcer at all rather than it simply never
	// connected.
	publishUntilPlayed(t, b, p, "game.clock.start", sfx.StartOfMatch)
}

func TestBusModeEndsWhenBusCloses(t *testing.T) {
	p := newFakePlayer()
	b := event.New()

	a := &Announcer{player: p, bus: b}
	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background()) }()

	b.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("announcer did not stop when the bus closed")
	}
}

// publishUntilPlayed publishes eventType until the announcer plays sound.
// It retries because a subscriber hears only what is published after it
// subscribed, and both the in-process and the websocket subscription are
// established asynchronously.
func publishUntilPlayed(t *testing.T, b *event.Bus, p *fakePlayer, eventType, sound string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := b.Publish(context.Background(), event.Event{Type: eventType}); err != nil {
			t.Fatalf("publish %s: %v", eventType, err)
		}
		select {
		case got := <-p.arrived:
			if got != sound {
				t.Fatalf("played %q, want %q", got, sound)
			}
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for %q to be announced as %q", eventType, sound)
}

// startEventServer serves an event bus's websocket endpoint.
func startEventServer(t *testing.T) (*event.Bus, string) {
	t.Helper()

	b := event.New()
	srv := httptest.NewServer(b.Handler())
	t.Cleanup(func() {
		srv.Close()
		b.Close()
	})
	return b, srv.URL
}

// newRemote returns an announcer for url whose sounds are recorded by p.
// The player is swapped in directly because the option that callers use
// takes the concrete player type, which talks to real audio hardware.
func newRemote(p *fakePlayer, url string) *Announcer {
	a := New(WithServer(url))
	a.player = p
	return a
}

func TestRemoteModePlaysClockSounds(t *testing.T) {
	b, url := startEventServer(t)

	p := newFakePlayer()
	a := newRemote(p, url)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := a.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Run: %v", err)
		}
	}()

	// The subscription is established by the server, so keep publishing
	// until the announcer has clearly connected and heard one.
	publishUntilPlayed(t, b, p, "game.clock.start", sfx.StartOfMatch)
	publishUntilPlayed(t, b, p, "game.clock.hurry", sfx.MatchAlmostOver)
	publishUntilPlayed(t, b, p, "game.clock.end", sfx.EndOfMatch)
	publishUntilPlayed(t, b, p, "game.clock.cancel", sfx.MatchFault)
}

func TestRemoteModeSurvivesADroppedConnection(t *testing.T) {
	// A server that closes the event stream must send the announcer
	// back to reconnecting rather than ending the run.
	closed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		env, _ := json.Marshal(event.Envelope{Seq: 1, Type: event.TypeSubscribed})
		_ = conn.Write(r.Context(), websocket.MessageText, env)
		_ = conn.Close(websocket.StatusNormalClosure, "")
		<-closed
	}))
	defer func() {
		close(closed)
		srv.Close()
	}()

	a := newRemote(newFakePlayer(), srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	if err := a.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run error = %v, want it to keep retrying until the deadline", err)
	}
}

func TestRemoteModeReadsFramesFromAServerThatIgnoresTheFilter(t *testing.T) {
	// The server, not the client, applies the type filter, and a server
	// need not honour it.  Junk and uninteresting frames must be skipped
	// without losing the events that matter.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		ctx := r.Context()
		write := func(v any) {
			data, _ := json.Marshal(v)
			_ = conn.Write(ctx, websocket.MessageText, data)
		}
		write(event.Envelope{Seq: 1, Type: event.TypeSubscribed})
		_ = conn.Write(ctx, websocket.MessageText, []byte("this is not json"))
		write(event.Envelope{Seq: 2, Type: "game.match.state"})
		write(event.Envelope{Seq: 3, Type: event.TypeSubscribed})
		write(event.Envelope{Seq: 4, Type: "game.clock.start"})

		// Hold the connection open until the client goes away.
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	p := newFakePlayer()
	a := newRemote(p, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = a.Run(ctx) }()

	p.waitFor(t, sfx.StartOfMatch)
	if got := p.list(); len(got) != 1 {
		t.Errorf("played %v, want exactly the one clock sound", got)
	}
}

func TestEventsURL(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		{"http://localhost:8080", "ws://localhost:8080/ui/events?types=" + typesQuery()},
		{"http://localhost:8080/", "ws://localhost:8080/ui/events?types=" + typesQuery()},
		{"https://gameday.example.com", "wss://gameday.example.com/ui/events?types=" + typesQuery()},
		{"ws://10.0.0.5:8080", "ws://10.0.0.5:8080/ui/events?types=" + typesQuery()},
		{"wss://gameday.example.com", "wss://gameday.example.com/ui/events?types=" + typesQuery()},
		// An explicit event path and any query are respected.
		{"http://host/events?replay=5", "ws://host/events?replay=5&types=" + typesQuery()},
		{" http://host ", "ws://host/ui/events?types=" + typesQuery()},
	}
	for _, c := range cases {
		got, err := eventsURL(c.base)
		if err != nil {
			t.Errorf("%q: %v", c.base, err)
			continue
		}
		if got != c.want {
			t.Errorf("eventsURL(%q) = %q, want %q", c.base, got, c.want)
		}
	}
}

func TestEventsURLRejectsUnusableServers(t *testing.T) {
	for _, base := range []string{"", "localhost:8080", "ftp://host", "//host", "http://"} {
		if _, err := eventsURL(base); err == nil {
			t.Errorf("eventsURL(%q) succeeded, want an error", base)
		}
	}
}

// typesQuery is the filter query value the announcer sends, as it appears
// in a URL.
func typesQuery() string {
	return url.QueryEscape(strings.Join(EventTypes(), ","))
}

func TestValidate(t *testing.T) {
	if err := New(WithServer("http://host:8080")).Validate(); err != nil {
		t.Errorf("a usable server should validate: %v", err)
	}
	err := New(WithServer("not a url")).Validate()
	if err == nil {
		t.Fatal("an unusable server should not validate")
	}
	if !strings.Contains(err.Error(), "not a url") {
		t.Errorf("error %q should name the bad address", err)
	}
	if err := New(WithBus(event.New())).Validate(); err != nil {
		t.Errorf("a bus should validate: %v", err)
	}
}

func TestRunReportsAnUnusableServer(t *testing.T) {
	err := New(WithServer("nope")).Run(context.Background())
	if err == nil {
		t.Fatal("Run should refuse an unusable server")
	}
}

func TestDefaultServer(t *testing.T) {
	t.Setenv(EnvServer, "")
	t.Setenv(envPort, "")
	if got, want := DefaultServer(), "http://localhost:8080"; got != want {
		t.Errorf("DefaultServer() = %q, want %q", got, want)
	}

	t.Setenv(envPort, "9000")
	if got, want := DefaultServer(), "http://localhost:9000"; got != want {
		t.Errorf("DefaultServer() = %q, want %q", got, want)
	}

	t.Setenv(EnvServer, "http://10.0.0.9:8080")
	if got, want := DefaultServer(), "http://10.0.0.9:8080"; got != want {
		t.Errorf("DefaultServer() = %q, want %q", got, want)
	}
}

func TestNewFallsBackToTheEnvironmentServer(t *testing.T) {
	t.Setenv(EnvServer, "http://from.env:1234")

	a := New()
	if got, want := a.Server(), "http://from.env:1234"; got != want {
		t.Errorf("Server() = %q, want %q", got, want)
	}

	// An explicit server wins over the environment.
	if got, want := New(WithServer("http://explicit")).Server(), "http://explicit"; got != want {
		t.Errorf("Server() = %q, want %q", got, want)
	}
}

func TestReconnectDelayGrowsAndCaps(t *testing.T) {
	for attempt := range 12 {
		d := reconnectDelay(attempt)
		if d < minReconnectDelay || d > maxReconnectDelay+reconnectJitter {
			t.Errorf("attempt %d delay %s outside [%s, %s]",
				attempt, d, minReconnectDelay, maxReconnectDelay+reconnectJitter)
		}
	}
	if !(reconnectDelay(0) < reconnectDelay(3)) {
		t.Error("the delay should grow while backing off")
	}
	if reconnectDelay(30) > maxReconnectDelay+reconnectJitter {
		t.Error("the delay should stop growing at the cap")
	}
}
