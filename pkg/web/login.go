package web

import (
	"net/http"
	"os"
	"strings"

	"github.com/flosch/pongo2/v6"
)

func (s *Server) uiViewLogin(w http.ResponseWriter, r *http.Request) {
	s.DoTemplate(w, r, "login.p2", nil)
}

// uiViewLanding renders the landing page.  Onsite servers show a
// welcome box with a login notice and the URLs other computers can use
// to reach this server; cloud servers show the operator's
// GAMEDAY_CLOUD_WELCOME_MSG and a scorecards hint for logged out
// users.
func (s *Server) uiViewLanding(w http.ResponseWriter, r *http.Request) {
	ctx := pongo2.Context{}
	switch s.scope {
	case "onsite":
		ctx["onsite"] = true
		ctx["urls"] = s.advertisedURLs()
	case "cloud":
		ctx["cloud"] = true
		ctx["welcomeMsg"] = strings.TrimSpace(os.Getenv("GAMEDAY_CLOUD_WELCOME_MSG"))
	}
	s.DoTemplate(w, r, "landing.p2", ctx)
}
