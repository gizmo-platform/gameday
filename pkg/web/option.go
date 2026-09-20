package web

import (
	"github.com/gizmo-platform/gameday/pkg/db"
)

func WithDB(d *db.DB) Option {
	return func(s *Server) error {
		s.d = d.Raw()
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
