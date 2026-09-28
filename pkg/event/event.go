// Package event implements a lightweight publish/subscribe event bus.
//
// The bus is framework agnostic: producers publish typed events with
// Bus.Publish, Go consumers receive envelopes with Bus.Subscribe, and
// the bus can export its event stream as JSON over websockets via
// Bus.Handler.
package event

import (
	"encoding/json"
	"time"
)

// Event is the unit of data a producer publishes to the bus.
type Event struct {
	// Type is a dot-separated event type, e.g. "game.match.complete".
	// It must not be empty and must not collide with a reserved
	// system type (TypeSubscribed, TypeClose).
	Type string
	// Data is the event payload. It must be JSON-encodable; Publish
	// validates it eagerly and returns an error if it cannot be
	// encoded.
	Data any
}

// Envelope wraps an event as it is delivered to consumers and sent on
// the websocket wire. It is the JSON wire format.
type Envelope struct {
	Seq  uint64          `json:"seq"`
	TS   time.Time       `json:"ts"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
	// Replay is set on envelopes that were delivered from the replay
	// ring (see the "replay" query parameter on the websocket
	// endpoint) rather than published live.
	Replay bool `json:"replay,omitempty"`
}

// System event types reserved by the bus. Producers must not publish
// events with these types.
const (
	// TypeSubscribed is the first envelope sent to a new websocket
	// subscriber. Its data is {"types":[...],"seq":N} where types is
	// the filter the subscriber requested (empty for all) and seq is
	// the highest sequence number assigned so far.
	TypeSubscribed = "subscribed"

	// TypeClose is sent when the bus shuts down, immediately before
	// the websocket connection is closed. Its data is
	// {"reason": "..."}.
	TypeClose = "close"
)
