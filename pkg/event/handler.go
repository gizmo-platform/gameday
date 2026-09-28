package event

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// Handler returns an http.Handler that serves the bus event stream
// as JSON over websockets. It is suitable for mounting on a router:
//
//	router.Mount("/ui/events", bus.Handler())
//
// Query parameters:
//
//	types   comma-separated event type filter; empty means all types
//	replay  number of recent envelopes to replay on connect
func (b *Bus) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.ServeWS(w, r)
	})
}

// ServeWS upgrades an HTTP request to a websocket and streams the bus
// event stream as JSON envelopes (the Envelope type is the wire
// format). See Handler for the supported query parameters.
//
// The first message on every connection is a "subscribed" envelope
// describing the filter and the current sequence number. If a replay
// count is requested, the most recent envelopes are sent next, each
// with the replay flag set. Live events follow in sequence order.
//
// When the bus shuts down, a "close" envelope is sent and the
// connection is closed cleanly.
func (b *Bus) ServeWS(w http.ResponseWriter, r *http.Request) {
	types := parseTypes(r.URL.Query().Get("types"))
	replayN := parseCount(r.URL.Query().Get("replay"))

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	// CloseRead handles ping/pong in a background goroutine and
	// cancels rctx when the client disconnects.
	rctx := conn.CloseRead(r.Context())

	sub, err := b.Subscribe(types)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "subscribe failed")
		return
	}

	if err := b.sendEnvelope(rctx, conn, Envelope{
		Seq:  b.seq.Load(),
		TS:   time.Now(),
		Type: TypeSubscribed,
		Data: mustJSON(struct {
			Types []string `json:"types"`
			Seq   uint64   `json:"seq"`
		}{Types: types, Seq: b.seq.Load()}),
	}); err != nil {
		return
	}

	for _, env := range b.Replay(replayN) {
		env.Replay = true
		if err := b.sendEnvelope(rctx, conn, env); err != nil {
			return
		}
	}

loop:
	for {
		select {
		case env, ok := <-sub:
			if !ok {
				// Bus closed.
				b.sendEnvelope(rctx, conn, Envelope{
					Seq:  b.seq.Load(),
					TS:   time.Now(),
					Type: TypeClose,
					Data: mustJSON(struct {
						Reason string `json:"reason"`
					}{"bus closed"}),
				})
				break loop
			}
			if err := b.sendEnvelope(rctx, conn, env); err != nil {
				break loop
			}
		case <-rctx.Done():
			break loop
		}
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

func (b *Bus) sendEnvelope(ctx context.Context, conn *websocket.Conn, env Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

// parseTypes splits a comma-separated event type list, ignoring empty
// segments.
func parseTypes(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// parseCount parses a non-negative integer query parameter, treating
// missing or invalid values as zero.
func parseCount(s string) int {
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
