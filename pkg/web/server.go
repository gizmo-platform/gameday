package web

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"os"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/the-maldridge/authware"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	ModuleName = "CORE"

	PermissionAdmin = "ADMIN"
)

//go:embed ui/*
var uifs embed.FS

// Option is used to configure the server
type Option func(*Server) error

// Server manages the HTTP serving components
type Server struct {
	r chi.Router
	n *http.Server
	d *gorm.DB

	tpl *pongo2.TemplateSet

	nav []NavElement
}

// NewServer returns a running field controller.
func NewServer(opts ...Option) (*Server, error) {
	sub, _ := fs.Sub(uifs, "ui/p2")
	ldr := pongo2.NewFSLoader(sub)

	x := new(Server)
	x.r = chi.NewRouter()
	x.n = &http.Server{}
	x.tpl = pongo2.NewSet("html", ldr)

	pongo2.RegisterFilter("hasPermission", x.filterHasPermission)
	pongo2.RegisterFilter("hasPermissionExact", x.filterHasPermissionExact)

	for _, o := range opts {
		if err := o(x); err != nil {
			slog.Warn("Error configuring webserver", "error", err)
			return nil, err
		}
	}

	if err := x.d.AutoMigrate(Permission{}, Profile{}, User{}); err != nil {
		slog.Error("Error migrating web core table", "error", err)
		return nil, err
	}

	if err := x.InstallPermission(context.Background(), ModuleName, PermissionAdmin); err != nil {
		return nil, err
	}

	if err := x.doUserInit(); err != nil {
		slog.Warn("Error initializing default admin", "error", err)
		return nil, err
	}

	x.AddNavElement(NavElement{
		Weight: 99,
		Text:   "Admin",
		Children: []NavChild{{
			Text:       "Local Users",
			Target:     "/admin/user/",
			Permission: Permission{Module: ModuleName, Grant: PermissionAdmin},
		}, {
			Text:       "Profiles",
			Target:     "/admin/profile/",
			Permission: Permission{Module: ModuleName, Grant: PermissionAdmin},
		}},
	})

	authware.RegisterFactory("gameday", newAuthwareBackend(x.d))

	if mechs := os.Getenv("AUTHWARE_BASIC_MECHS"); mechs == "" {
		os.Setenv("AUTHWARE_BASIC_MECHS", "gameday")
	}
	basic, err := authware.NewAuth()
	if err != nil {
		slog.Error("Error initializing auth", "error", err)
		return nil, err
	}

	x.r.Use(middleware.Heartbeat("/-/alive"))
	x.r.Use(basic.SessionHandler())
	x.r.Use(x.profileMiddleware)

	sfs, _ := fs.Sub(uifs, "ui")
	x.r.Handle("/static/*", http.FileServer(http.FS(sfs)))

	x.r.Route("/admin", func(r chi.Router) {
		r.Use(x.RequirePermission(Permission{Module: ModuleName, Grant: PermissionAdmin}))

		r.Get("/", x.uiViewAdminLanding)
		r.Route("/user", func(r chi.Router) {
			r.Get("/", x.uiViewUserList)

			r.Get("/create", x.uiViewUserForm)
			r.Post("/create", x.uiViewUserFormSubmit)

			r.Get("/{id}/reset-password", x.uiViewUserPasswordForm)
			r.Post("/{id}/reset-password", x.uiViewUserPasswordFormSubmit)

			r.Get("/{id}/delete", x.uiViewUserDeleteForm)
			r.Post("/{id}/delete", x.uiViewUserDeleteFormSubmit)
		})
		r.Route("/profile", func(r chi.Router) {
			r.Get("/", x.uiViewAdminProfileList)

			r.Get("/{id}/permissions", x.uiViewAdminProfilePermissionsForm)
			r.Post("/{id}/permissions", x.uiViewAdminProfilePermissionsSubmit)
		})
	})

	x.r.Get("/login", x.uiViewLogin)
	x.r.Post("/login", basic.LoginFormHandler("username", "password", "/ui"))
	x.r.Get("/logout", basic.LogoutHandler("/ui"))
	x.r.Get("/ui", x.uiViewLanding)

	x.r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui", http.StatusMovedPermanently)
	})

	return x, nil
}

// Serve binds and serves http on the bound socket.  An error will be
// returned if the server cannot initialize.
func (s *Server) Serve(bind string) error {
	slog.Info("HTTP is starting")
	s.n.Addr = bind
	s.n.Handler = s.r
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

// doUserInit makes sure that a bootstrap admin user exists, but only
// fires if there are no users defined at all.
func (s *Server) doUserInit() error {
	ctx := context.Background()
	var count int64

	s.d.Model(&User{}).Count(&count)
	if count != 0 {
		return nil
	}
	slog.Info("No users exist, will create default admin")

	cAdmin, err := gorm.G[Permission](s.d).
		Where(&Permission{Module: ModuleName, Grant: PermissionAdmin}).
		First(ctx)
	if err != nil {
		return err
	}

	pw := os.Getenv("GAMEDAY_ADMIN_INIT_PW")
	if pw == "" {
		pw = "GameOn!"
		slog.Warn("No GAMEDAY_ADMIN_INIT_PW found, using default", "password", pw)
	}
	bytes, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	u := User{Login: "admin", Secret: bytes}
	p := Profile{Username: "admin"}
	perms := []Permission{{ID: cAdmin.ID}}

	if err := gorm.G[Profile](s.d).Create(ctx, &p); err != nil {
		return err
	}
	u.ProfileID = p.ID

	if err := gorm.G[User](s.d).Create(ctx, &u); err != nil {
		return err
	}

	if err := s.d.Model(&Profile{ID: p.ID}).Association("Permissions").Replace(perms); err != nil {
		return err
	}

	return nil
}
