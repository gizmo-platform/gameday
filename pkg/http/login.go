package http

import (
	"net/http"
)

func (s *Server) uiViewLogin(w http.ResponseWriter, r *http.Request) {
	s.DoTemplate(w, r, "login.p2", nil)
}

func (s *Server) uiViewLanding(w http.ResponseWriter, r *http.Request) {
	s.DoTemplate(w, r, "base.p2", nil)
}
