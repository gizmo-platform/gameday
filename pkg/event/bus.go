package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrClosed is returned when operating on a closed bus.
	ErrClosed = errors.New("event: bus is closed")

	// ErrBadType is returned when an event type is empty or collides
	// with a reserved system type.
	ErrBadType = fmt.Errorf("event: invalid event type")
)

const (
	// DefaultSubBuffer is the per-subscriber channel buffer size.
	DefaultSubBuffer = 256

	// DefaultReplaySize is the number of recent envelopes retained in
	// the replay ring.
	DefaultReplaySize = 256

	// dispatchBuffer is the size of the internal dispatch channel.
	dispatchBuffer = 256
)

// Option configures a Bus at creation time.
type Option func(*Bus)

// WithSubscriberBuffer sets the per-subscriber channel buffer size.
func WithSubscriberBuffer(n int) Option {
	return func(b *Bus) {
		if n > 0 {
			b.subBuffer = n
		}
	}
}

// WithReplaySize sets the number of recent envelopes retained for
// replay (see Bus.Replay and the websocket "replay" query
// parameter). A value of 0 disables the replay ring.
func WithReplaySize(n int) Option {
	return func(b *Bus) {
		b.replaySize = n
	}
}

type subscriber struct {
	id     int
	types  map[string]struct{} // nil = all types
	ch     chan Envelope
	dropped atomic.Uint64
}

// Bus is a publish/subscribe event bus with a single dispatcher
// goroutine.
//
// Slow consumers do not block the dispatcher or the producers: if a
// subscriber's channel buffer is full, events are dropped for that
// subscriber only, and the drop is logged. The monotonically
// increasing Envelope.Seq value lets consumers detect gaps.
//
// A Bus is safe for concurrent use. Close shuts the bus down; all
// subsequent Publish and Subscribe calls return ErrClosed and all
// subscriber channels are closed.
type Bus struct {
	mu       sync.RWMutex
	subs     []*subscriber
	nextSub  int
	closed   atomic.Bool
	dispatch chan Envelope
	wg       sync.WaitGroup

	subBuffer  int
	replaySize int
	seq        atomic.Uint64

	ring ringBuf
}

// New returns a running Bus.
func New(opts ...Option) *Bus {
	b := &Bus{
		subBuffer:  DefaultSubBuffer,
		replaySize: DefaultReplaySize,
		dispatch:   make(chan Envelope, dispatchBuffer),
	}
	for _, o := range opts {
		o(b)
	}
	b.ring.init(b.replaySize)
	b.wg.Add(1)
	go b.dispatchLoop()
	return b
}

// Publish dispatches ev to all interested subscribers. The event data
// is marshaled to JSON eagerly so that producers see encoding errors
// immediately. Publish never blocks on slow consumers; it only
// returns an error if the bus is closed or the event type is invalid.
func (b *Bus) Publish(ctx context.Context, ev Event) error {
	if b.closed.Load() {
		return ErrClosed
	}
	if ev.Type == "" || ev.Type == TypeSubscribed || ev.Type == TypeClose {
		return fmt.Errorf("%w: %q", ErrBadType, ev.Type)
	}

	var raw json.RawMessage
	if ev.Data != nil {
		r, err := json.Marshal(ev.Data)
		if err != nil {
			return fmt.Errorf("event: encoding %q data: %w", ev.Type, err)
		}
		raw = r
	}

	env := Envelope{
		Seq:  b.seq.Add(1),
		TS:   time.Now(),
		Type: ev.Type,
		Data: raw,
	}

	b.ring.push(env)

	select {
	case b.dispatch <- env:
	default:
		// The dispatcher drains the channel continuously and never
		// blocks on fan-out, so a full channel means the bus is
		// shutting down.
		return ErrClosed
	}
	return nil
}

// Subscribe registers a consumer and returns its delivery channel.
// An empty or nil types list subscribes to all event types. The
// returned channel is closed when the bus is closed.
func (b *Bus) Subscribe(types []string) (chan Envelope, error) {
	if b.closed.Load() {
		return nil, ErrClosed
	}

	filter := make(map[string]struct{}, len(types))
	for _, t := range types {
		filter[t] = struct{}{}
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed.Load() {
		return nil, ErrClosed
	}

	sub := &subscriber{
		id:  b.nextSub,
		ch:  make(chan Envelope, b.subBuffer),
	}
	if len(filter) > 0 {
		sub.types = filter
	}
	b.nextSub++
	b.subs = append(b.subs, sub)

	return sub.ch, nil
}

// Replay returns up to the most recent n envelopes published to the
// bus (across all event types), oldest first. It returns an empty
// slice if the replay ring is disabled.
func (b *Bus) Replay(n int) []Envelope {
	return b.ring.last(n)
}

// Close shuts down the bus. It is idempotent. All subscriber
// channels are closed so that blocked consumers unblock.
func (b *Bus) Close() {
	if !b.closed.CompareAndSwap(false, true) {
		return
	}
	close(b.dispatch)
	b.wg.Wait()

	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sub := range b.subs {
		close(sub.ch)
	}
	b.subs = nil
}

func (b *Bus) dispatchLoop() {
	defer b.wg.Done()
	for env := range b.dispatch {
		b.fanout(env)
	}
}

func (b *Bus) fanout(env Envelope) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, sub := range b.subs {
		if sub.types != nil {
			if _, ok := sub.types[env.Type]; !ok {
				continue
			}
		}
		select {
		case sub.ch <- env:
		default:
			dropped := sub.dropped.Add(1)
			// Log the first drop, then every 1000th, to avoid
			// flooding the log with a persistently slow consumer.
			if dropped == 1 || dropped%1000 == 0 {
				slog.Warn("event: dropping events for slow subscriber",
					"subscriber", sub.id, "dropped", dropped)
			}
		}
	}
}

// ringBuf is a fixed-size circular buffer of envelopes.
type ringBuf struct {
	mu    sync.Mutex
	size  int
	buf   []Envelope
	head  int // index of the oldest element (when full)
	count int
}

func (r *ringBuf) init(size int) {
	if size > 0 {
		r.size = size
		r.buf = make([]Envelope, size)
	}
}

func (r *ringBuf) push(e Envelope) {
	if r.size == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	idx := (r.head + r.count) % r.size
	r.buf[idx] = e
	if r.count < r.size {
		r.count++
	} else {
		r.head = (r.head + 1) % r.size
	}
}

func (r *ringBuf) last(n int) []Envelope {
	if r.size == 0 || n <= 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	k := n
	if k > r.count {
		k = r.count
	}
	out := make([]Envelope, 0, k)
	for i := r.count - k; i < r.count; i++ {
		out = append(out, r.buf[(r.head+i)%r.size])
	}
	return out
}
