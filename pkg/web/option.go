package web

import (
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/event"
)

func WithDB(d *db.DB) Option {
	return func(s *Server) error {
		s.d = d.Raw()
		return nil
	}
}

// EventBus attaches an event bus to the server. The bus is reachable
// from modules and handlers via Server.Bus() and can be mounted for
// websocket export via Bus.Handler().
func EventBus(b *event.Bus) Option {
	return func(s *Server) error {
		s.bus = b
		return nil
	}
}

// WithTemplateDebug enables template debug mode. When enabled, the
// server's core templates are loaded from the on-disk source tree
// (pkg/web/ui/p2, relative to the process working directory) with a
// fallback to the embedded templates, and the TemplateSet is put in
// Debug mode so that templates are re-ingested on every render.
func WithTemplateDebug(debug bool) Option {
	return func(s *Server) error {
		s.debugTemplates = debug
		return nil
	}
}
