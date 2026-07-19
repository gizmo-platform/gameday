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
