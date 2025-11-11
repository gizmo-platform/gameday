package team

import (
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

// WithDatabase provides the database reference
func WithDatabase(d *db.DB) Option {
	return func(m *Module) {
		m.db = d
	}
}

// WithWebserver provides the webserver reference
func WithWebserver(w *web.Server) Option {
	return func(m *Module) {
		m.ws = w
	}
}
