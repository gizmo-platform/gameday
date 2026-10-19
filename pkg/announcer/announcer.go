// Package announcer turns the match clock's events into the sounds an
// event run depends on.
//
// The match clock publishes the milestones of a run — start, hurry,
// end, cancel — on the event bus.  The announcer subscribes to those
// events and plays the matching sound through the machine's audio
// output, which is what lets an operator run the field from the
// timekeeping page while the arena hears the clock.
//
// The bus is reached in one of two ways.  In-process mode takes a bus
// the same program created, which is how a server that hosts the clock
// announces it directly.  Remote mode dials the event websocket of a
// gameday server, which is how a separate machine — the one with the
// speakers wired to the arena's sound system — announces a clock it does
// not own.  Remote mode reconnects with backoff, so an announcer started
// before the server keeps working once the server appears.
//
// Only live events make sound.  Events replayed from the bus's ring
// buffer are skipped, because a reconnecting announcer would otherwise
// announce a match that ended minutes ago.
package announcer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/gizmo-platform/gameday/pkg/event"
	"github.com/gizmo-platform/gameday/pkg/sfx"
)

// EnvServer names the environment variable holding the base URL of the
// gameday server whose event stream to announce, e.g.
// "http://192.168.1.10:8080".
const EnvServer = "GAMEDAY_SERVER"

// envPort is the port the onsite server listens on, which the default
// server address uses.  It is the same variable the server itself reads.
const envPort = "GAMEDAY_PORT"

const (
	// defaultPort is the port an onsite server listens on when
	// GAMEDAY_PORT is unset.
	defaultPort = "8080"

	// eventsPath is where a gameday server serves its event stream.
	eventsPath = "/ui/events"

	// minReconnectDelay is how long the first reconnect waits.
	minReconnectDelay = 500 * time.Millisecond

	// maxReconnectDelay caps the delay between connection attempts, so
	// a long outage settles into a slow poll instead of a storm.
	maxReconnectDelay = 15 * time.Second

	// reconnectJitter is the random extra added to each delay, so an
	// arena full of announcers that lost the server at the same moment
	// do not all come back together.
	reconnectJitter = 250 * time.Millisecond
)

// sounds maps the event types the announcer reacts to onto the sound
// that announces them.
//
// The keys are the match clock's event types as they appear on the wire
// (see the game module's clock).  They are spelled out here rather than
// imported because the wire format is the contract between the two, and
// the announcer must work against a server it does not share a build
// with.
//
// Cancelling a run is announced with the fault sound: teams who hear it
// need to stop what they are doing either way, and silence would leave
// them waiting for a start that is not coming.
var sounds = map[string]string{
	"game.clock.start":  sfx.StartOfMatch,
	"game.clock.hurry":  sfx.MatchAlmostOver,
	"game.clock.end":    sfx.EndOfMatch,
	"game.clock.cancel": sfx.MatchFault,
}

// SoundFor returns the sound that announces eventType.
func SoundFor(eventType string) (string, bool) {
	name, ok := sounds[eventType]
	return name, ok
}

// EventTypes lists, sorted, the event types the announcer subscribes to.
func EventTypes() []string {
	types := make([]string, 0, len(sounds))
	for t := range sounds {
		types = append(types, t)
	}
	slices.Sort(types)
	return types
}

// player is what the announcer needs of a sound player.  Tests replace
// it to observe what would have been audible.
type player interface {
	Play(name string) error
}

// dialFunc establishes an event websocket connection.  Tests replace it
// to stand in for the server.
type dialFunc func(ctx context.Context, u string) (*websocket.Conn, error)

// Announcer plays the sounds the event bus's clock events describe.
//
// Create one with New and hand it to Run, which blocks until the context
// is cancelled.
type Announcer struct {
	// player plays the sounds.  One player is shared with whoever else
	// in the process makes sound, since only one sound is audible at a
	// time anyway.
	player player

	// bus, when set, is announced directly instead of over the network.
	bus *event.Bus

	// server is the base URL of the gameday server to announce, used
	// when bus is nil.
	server string

	// dial opens the event websocket.
	dial dialFunc
}

// Option configures an Announcer as it is created.
type Option func(*Announcer)

// WithPlayer plays sounds through p instead of a player the announcer
// creates itself.  Use it when the calling program also plays sounds on
// its own, so both share one audio output.
func WithPlayer(p *sfx.Player) Option {
	return func(a *Announcer) {
		if p != nil {
			a.player = p
		}
	}
}

// WithBus announces bus, which must be a bus running in this process.
// It takes precedence over the server address.
func WithBus(b *event.Bus) Option {
	return func(a *Announcer) {
		a.bus = b
	}
}

// WithServer announces the event stream of the gameday server at base,
// e.g. "http://localhost:8080".  An empty base is ignored, which lets a
// caller pass an unset environment variable through.
func WithServer(base string) Option {
	return func(a *Announcer) {
		if base != "" {
			a.server = base
		}
	}
}

// New returns an Announcer.
//
// Without other options it announces the server named by GAMEDAY_SERVER,
// falling back to localhost on the port the onsite server listens on.
func New(opts ...Option) *Announcer {
	a := &Announcer{
		dial: func(ctx context.Context, u string) (*websocket.Conn, error) {
			conn, _, err := websocket.Dial(ctx, u, nil)
			return conn, err
		},
	}
	for _, opt := range opts {
		opt(a)
	}
	if a.player == nil {
		a.player = sfx.New()
	}
	if a.bus == nil && a.server == "" {
		a.server = DefaultServer()
	}
	return a
}

// Validate reports whether the announcer has something it can announce:
// a bus, or a server address it could dial.  It is how a caller gets a
// malformed address reported by the call that supplied it rather than by
// a background retry loop.
func (a *Announcer) Validate() error {
	if a.bus != nil {
		return nil
	}
	_, err := eventsURL(a.server)
	return err
}

// DefaultServer returns the base URL of the gameday server an Announcer
// announces when nothing else names one: GAMEDAY_SERVER if set, otherwise
// localhost on GAMEDAY_PORT.
func DefaultServer() string {
	if server := os.Getenv(EnvServer); server != "" {
		return server
	}
	port := os.Getenv(envPort)
	if port == "" {
		port = defaultPort
	}
	return "http://localhost:" + port
}

// Server returns the base URL of the server this announcer announces, or
// the empty string when it announces a bus in this process.
func (a *Announcer) Server() string {
	return a.server
}

// Run announces events until ctx is cancelled, then returns ctx's error.
//
// A bus that closes ends the run without an error; a server that cannot
// be reached is retried until the context ends.
func (a *Announcer) Run(ctx context.Context) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if a.bus != nil {
		return a.watchBus(ctx, a.bus)
	}
	return a.watchServer(ctx)
}

// watchBus announces a bus in this process.
func (a *Announcer) watchBus(ctx context.Context, b *event.Bus) error {
	ch, err := b.Subscribe(EventTypes())
	if err != nil {
		// A bus that is already closed has nothing left to announce;
		// that is an ordinary end, not a failure.
		if errors.Is(err, event.ErrClosed) {
			return nil
		}
		return err
	}
	for {
		select {
		case env, ok := <-ch:
			if !ok {
				// The bus closed; nothing more will be announced.
				return nil
			}
			a.announce(env)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// watchServer announces a remote server's event stream, reconnecting for
// as long as the context lives.
func (a *Announcer) watchServer(ctx context.Context) error {
	u, err := eventsURL(a.server)
	if err != nil {
		return err
	}

	for attempt := 0; ; {
		connected, err := a.watchURL(ctx, u)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A connection that made it as far as its subscription proves
		// the server is there, so the next outage starts backing off
		// from scratch again.
		if connected {
			attempt = 0
		}
		delay := reconnectDelay(attempt)
		slog.Warn("Event stream ended, reconnecting",
			"url", u, "error", err, "retry_in", delay)

		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
		attempt++
	}
}

// watchURL reads one connection's worth of events.  It reports whether
// the connection got as far as being subscribed, which is how the caller
// knows whether to keep backing off.
func (a *Announcer) watchURL(ctx context.Context, u string) (bool, error) {
	conn, err := a.dial(ctx, u)
	if err != nil {
		return false, err
	}

	// The connection's own reads are not tied to ctx, so a watchdog
	// closes the connection when the run ends; that is what unblocks
	// the read below.  (CloseRead cannot be used here: it treats the
	// data messages this side wants to read as a protocol violation.)
	done := make(chan struct{})
	defer func() {
		close(done)
		conn.Close(websocket.StatusNormalClosure, "")
	}()
	go func() {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "")
		case <-done:
		}
	}()

	subscribed := false
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return subscribed, ctx.Err()
			}
			return subscribed, err
		}

		var env event.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			slog.Warn("Ignoring unreadable event", "url", u, "error", err)
			continue
		}

		switch env.Type {
		case event.TypeSubscribed:
			if !subscribed {
				slog.Info("Announcing events", "url", u, "types", strings.Join(EventTypes(), ","))
			}
			subscribed = true
			continue
		case event.TypeClose:
			return subscribed, errors.New("server closed the event stream")
		}

		a.announce(env)
	}
}

// announce plays the sound of one envelope, logging rather than failing
// on anything that goes wrong: a sound that cannot be heard must never
// take the announcer down.
func (a *Announcer) announce(env event.Envelope) {
	name, ok := SoundFor(env.Type)
	if !ok {
		return
	}
	if env.Replay {
		slog.Debug("Ignoring replayed event", "type", env.Type, "seq", env.Seq)
		return
	}
	if err := a.player.Play(name); err != nil {
		slog.Warn("Could not play sound", "type", env.Type, "sound", name, "error", err)
	}
}

// eventsURL turns the base URL of a gameday server into the websocket
// URL of its event stream, with the clock's event types as the filter.
//
// A base may be given with an http, https, ws or wss scheme, with or
// without a path, and any query it carries is preserved.
func eventsURL(base string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", fmt.Errorf("announcer: invalid server %q: %w", base, err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("announcer: server %q must be an http or https URL", base)
	}
	if u.Host == "" {
		return "", fmt.Errorf("announcer: server %q has no host", base)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = eventsPath
	}

	q := u.Query()
	q.Set("types", strings.Join(EventTypes(), ","))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// reconnectDelay is how long to wait before the connection attempt after
// attempt failed attempts, growing exponentially to a cap with jitter.
func reconnectDelay(attempt int) time.Duration {
	delay := minReconnectDelay
	for range attempt {
		delay *= 2
		if delay >= maxReconnectDelay {
			delay = maxReconnectDelay
			break
		}
	}
	if delay > maxReconnectDelay {
		delay = maxReconnectDelay
	}
	return delay + rand.N(reconnectJitter)
}
