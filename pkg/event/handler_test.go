package event

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func startWS(t *testing.T, b *Bus, query string) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(b.Handler())
	t.Cleanup(srv.Close)

	url := strings.Replace(srv.URL, "http://", "ws://", 1) + "/?" + query
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func readEnvelope(t *testing.T, conn *websocket.Conn, timeout time.Duration) Envelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return env
}

func TestWSSubscribedFirst(t *testing.T) {
	b := New()
	defer b.Close()

	conn := startWS(t, b, "types=a,b")
	env := readEnvelope(t, conn, 2*time.Second)
	if env.Type != TypeSubscribed {
		t.Fatalf("first envelope type = %q, want %q", env.Type, TypeSubscribed)
	}
	var payload struct {
		Types []string `json:"types"`
		Seq   uint64   `json:"seq"`
	}
	if err := json.Unmarshal(env.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Types) != 2 || payload.Types[0] != "a" || payload.Types[1] != "b" {
		t.Errorf("payload types = %v", payload.Types)
	}
}

func TestWSLiveEventsAndFilter(t *testing.T) {
	b := New()
	defer b.Close()

	conn := startWS(t, b, "types=keep")

	// First message is the subscription confirmation.
	readEnvelope(t, conn, 2*time.Second)

	ctx := context.Background()
	if err := b.Publish(ctx, Event{Type: "drop", Data: 1}); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, Event{Type: "keep", Data: 2}); err != nil {
		t.Fatal(err)
	}

	env := readEnvelope(t, conn, 2*time.Second)
	if env.Type != "keep" {
		t.Fatalf("envelope type = %q, want keep", env.Type)
	}
	var v int
	if err := json.Unmarshal(env.Data, &v); err != nil || v != 2 {
		t.Errorf("data = %v, want 2", env.Data)
	}
}

func TestWSReplay(t *testing.T) {
	b := New(WithReplaySize(10))
	defer b.Close()

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := b.Publish(ctx, Event{Type: "r", Data: i}); err != nil {
			t.Fatal(err)
		}
	}

	conn := startWS(t, b, "replay=2")
	readEnvelope(t, conn, 2*time.Second) // subscribed

	var seqs []uint64
	for i := 0; i < 2; i++ {
		env := readEnvelope(t, conn, 2*time.Second)
		if env.Type != "r" {
			t.Fatalf("replay %d type = %q", i, env.Type)
		}
		if !env.Replay {
			t.Errorf("replay %d: replay flag not set", i)
		}
		seqs = append(seqs, env.Seq)
	}
	// The two replayed envelopes must be the most recent two, in order.
	last := b.Replay(1)
	if len(last) != 1 || seqs[1] != last[0].Seq {
		t.Errorf("replayed seqs = %v, want the most recent published seq %v", seqs, last)
	}
	if seqs[0] >= seqs[1] {
		t.Errorf("replayed seqs not in order: %v", seqs)
	}
}

func TestWSCloseOnBusClose(t *testing.T) {
	b := New()

	conn := startWS(t, b, "")
	readEnvelope(t, conn, 2*time.Second) // subscribed

	go b.Close()

	env := readEnvelope(t, conn, 3*time.Second)
	if env.Type != TypeClose {
		t.Fatalf("final envelope type = %q, want %q", env.Type, TypeClose)
	}

	// The connection should now be closed.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Error("expected read error after close, got nil")
	}
}

func TestWSClientDisconnectStopsWriter(t *testing.T) {
	b := New()
	defer b.Close()

	conn := startWS(t, b, "")
	readEnvelope(t, conn, 2*time.Second) // subscribed

	conn.Close(websocket.StatusNormalClosure, "")

	// After the client disconnects, the server-side writer goroutine
	// must exit; publishing further must not panic or block.
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if err := b.Publish(ctx, Event{Type: "x", Data: i}); err != nil {
			t.Fatalf("publish %d after disconnect: %v", i, err)
		}
	}
}

// Handler must serve at the mount path with chi-style mounting.
func TestHandlerMount(t *testing.T) {
	b := New()
	defer b.Close()

	mux := httptest.NewServer(b.Handler())
	defer mux.Close()

	url := strings.Replace(mux.URL, "http://", "ws://", 1) + "/"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	env := readEnvelope(t, conn, 2*time.Second)
	if env.Type != TypeSubscribed {
		t.Fatalf("first envelope type = %q", env.Type)
	}
}
