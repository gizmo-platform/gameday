package event

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestPublishSubscribeAll(t *testing.T) {
	b := New()
	defer b.Close()

	ctx := context.Background()
	ch, err := b.Subscribe(nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := b.Publish(ctx, Event{Type: "a.b", Data: map[string]int{"x": 1}}); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, Event{Type: "c", Data: "hello"}); err != nil {
		t.Fatal(err)
	}

	var got []Envelope
	deadline := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case env := <-ch:
			got = append(got, env)
		case <-deadline:
			t.Fatalf("timed out, got %d of 2", len(got))
		}
	}

	if got[0].Seq != 1 || got[0].Type != "a.b" {
		t.Errorf("envelope 0 = %+v", got[0])
	}
	if got[1].Seq != 2 || got[1].Type != "c" {
		t.Errorf("envelope 1 = %+v", got[1])
	}
	var x map[string]any
	if err := jsonUnmarshal(got[0].Data, &x); err != nil {
		t.Errorf("decoding data: %v", err)
	}
}

func TestSubscribeTypeFilter(t *testing.T) {
	b := New()
	defer b.Close()

	ctx := context.Background()
	ch, err := b.Subscribe([]string{"keep"})
	if err != nil {
		t.Fatal(err)
	}

	for _, typ := range []string{"drop", "keep", "drop", "keep"} {
		if err := b.Publish(ctx, Event{Type: typ, Data: typ}); err != nil {
			t.Fatal(err)
		}
	}

	var got []string
	deadline := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case env := <-ch:
			got = append(got, env.Type)
		case <-deadline:
			t.Fatalf("timed out, got %v", got)
		}
	}
	if len(got) != 2 || got[0] != "keep" || got[1] != "keep" {
		t.Errorf("got %v, want [keep keep]", got)
	}
}

func TestPublishValidation(t *testing.T) {
	b := New()
	defer b.Close()

	ctx := context.Background()
	for _, typ := range []string{"", TypeSubscribed, TypeClose} {
		if err := b.Publish(ctx, Event{Type: typ}); !errors.Is(err, ErrBadType) {
			t.Errorf("Publish(%q): got %v, want ErrBadType", typ, err)
		}
	}

	if err := b.Publish(ctx, Event{
		Type: "bad",
		Data: make(chan int),
	}); err == nil {
		t.Error("Publish with unencodable data: got nil, want error")
	}
}

func TestSlowConsumerDropsWithoutBlocking(t *testing.T) {
	b := New(WithSubscriberBuffer(4))
	defer b.Close()

	ctx := context.Background()
	ch, err := b.Subscribe(nil)
	if err != nil {
		t.Fatal(err)
	}

	const n = 100
	// The subscriber buffer is 4 and nobody reads, so nearly all of
	// these publishes must drop for this subscriber. Publish must
	// return quickly for each.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			if err := b.Publish(ctx, Event{Type: "x", Data: i}); err != nil {
				t.Errorf("Publish %d: %v", i, err)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}

	// Let the dispatcher finish working through the dispatch channel
	// before draining, so nothing is delivered concurrently with the
	// drain below.
	time.Sleep(300 * time.Millisecond)

	// Whatever arrives must be in order with no gaps, and no more
	// than buffer worth of envelopes can have been delivered.
	var delivered []uint64
drain:
	for {
		select {
		case env := <-ch:
			delivered = append(delivered, env.Seq)
		default:
			break drain
		}
	}
	if len(delivered) > 4 {
		t.Errorf("delivered %d envelopes to a 4-slot buffer", len(delivered))
	}
	for i := 1; i < len(delivered); i++ {
		if delivered[i] != delivered[i-1]+1 {
			t.Errorf("gap in delivered seqs: %v", delivered)
		}
	}
}

func TestReplay(t *testing.T) {
	b := New(WithReplaySize(5))
	defer b.Close()

	ctx := context.Background()
	for i := 0; i < 8; i++ {
		if err := b.Publish(ctx, Event{Type: "r", Data: i}); err != nil {
			t.Fatal(err)
		}
	}

	all := b.Replay(100)
	if len(all) != 5 {
		t.Fatalf("Replay(100) returned %d envelopes, want 5", len(all))
	}
	for i, env := range all {
		if env.Seq != uint64(i+4) {
			t.Errorf("envelope %d seq = %d, want %d", i, env.Seq, i+4)
		}
	}

	last := b.Replay(2)
	if len(last) != 2 || last[0].Seq != 7 || last[1].Seq != 8 {
		t.Errorf("Replay(2) = %v", seqs(last))
	}
}

func TestReplayDisabled(t *testing.T) {
	b := New(WithReplaySize(0))
	defer b.Close()

	ctx := context.Background()
	if err := b.Publish(ctx, Event{Type: "r"}); err != nil {
		t.Fatal(err)
	}
	if got := b.Replay(10); got != nil {
		t.Errorf("Replay with disabled ring = %v, want nil", got)
	}
}

func TestClose(t *testing.T) {
	b := New()

	ctx := context.Background()
	ch1, err := b.Subscribe(nil)
	if err != nil {
		t.Fatal(err)
	}
	ch2, err := b.Subscribe([]string{"a"})
	if err != nil {
		t.Fatal(err)
	}

	b.Close()
	b.Close() // idempotent

	if err := b.Publish(ctx, Event{Type: "a"}); !errors.Is(err, ErrClosed) {
		t.Errorf("Publish after close: got %v, want ErrClosed", err)
	}
	if _, err := b.Subscribe(nil); !errors.Is(err, ErrClosed) {
		t.Errorf("Subscribe after close: got %v, want ErrClosed", err)
	}

	if !isClosed(ch1) {
		t.Error("subscriber channel 1 not closed")
	}
	if !isClosed(ch2) {
		t.Error("subscriber channel 2 not closed")
	}
}

func seqs(envs []Envelope) []uint64 {
	out := make([]uint64, len(envs))
	for i, e := range envs {
		out[i] = e.Seq
	}
	return out
}

func isClosed(ch chan Envelope) bool {
	select {
	case _, ok := <-ch:
		return !ok
	case <-time.After(time.Second):
		return false
	}
}

func jsonUnmarshal(raw []byte, v any) error {
	return json.Unmarshal(raw, v)
}
