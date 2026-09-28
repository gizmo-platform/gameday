package web

import (
	"context"
	"testing"

	"github.com/gizmo-platform/gameday/pkg/event"
)

func TestEventBusOption(t *testing.T) {
	b := event.New()
	defer b.Close()

	s := new(Server)
	if err := EventBus(b)(s); err != nil {
		t.Fatal(err)
	}
	if got := s.Bus(); got != b {
		t.Fatalf("Bus() = %v, want the configured bus", got)
	}

	if err := s.Bus().Publish(context.Background(), event.Event{Type: "test.ping", Data: "ok"}); err != nil {
		t.Fatalf("publishing through the server's bus: %v", err)
	}
}

func TestBusNilByDefault(t *testing.T) {
	s := new(Server)
	if err := WithTemplateDebug(false)(s); err != nil {
		t.Fatal(err)
	}
	if got := s.Bus(); got != nil {
		t.Fatalf("Bus() = %v, want nil when EventBus is not configured", got)
	}
}
