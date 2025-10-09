package http

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/the-maldridge/authware"
)

//go:embed ui/*
var uifs embed.FS

// Server manages the HTTP serving components
type Server struct {
	r   chi.Router
	n   *http.Server
	swg *sync.WaitGroup

	tpl *pongo2.TemplateSet
}

// NewServer returns a running field controller.
func NewServer(opts ...Option) (*Server, error) {
	sub, _ := fs.Sub(uifs, "ui/p2")
	ldr := pongo2.NewFSLoader(sub)

	x := new(Server)
	x.r = chi.NewRouter()
	x.n = &http.Server{}
	x.tpl = pongo2.NewSet("html", ldr)

	basic, err := authware.NewAuth()
	if err != nil {
		slog.Error("Error initializing auth", "error", err)
		return nil, err
	}

	x.r.Use(middleware.Heartbeat("/-/alive"))
	x.r.Use(basic.SessionHandler())

	sfs, _ := fs.Sub(uifs, "ui")
	x.r.Handle("/static/*", http.FileServer(http.FS(sfs)))

	x.r.Get("/login", x.uiViewLogin)
	x.r.Post("/login", basic.LoginFormHandler("username", "password", "/ui"))
	x.r.Get("/logout", basic.LogoutHandler("/ui"))
	x.r.Get("/ui", x.uiViewLanding)

	for _, o := range opts {
		if err := o(x); err != nil {
			return nil, err
		}
	}

	return x, nil
}

// Serve binds and serves http on the bound socket.  An error will be
// returned if the server cannot initialize.
func (s *Server) Serve(bind string) error {
	slog.Info("HTTP is starting")
	s.n.Addr = bind
	s.n.Handler = s.r
	s.swg.Done()
	return s.n.ListenAndServe()
}

// Mount attaches a set of routes to the subpath specified by the path
// argument.
func (s *Server) Mount(path string, router chi.Router) {
	s.r.Mount(path, router)
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	slog.Info("HTTP Stopping...")
	return s.n.Shutdown(ctx)
}
