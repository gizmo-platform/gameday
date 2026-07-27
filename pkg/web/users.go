package web

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"github.com/the-maldridge/authware"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// User is an internal user that is managed fully internally.
type User struct {
	ID uint

	Login  string `gorm:"unique"`
	Secret []byte

	Profile   Profile
	ProfileID uint
}

func newAuthwareBackend(db *gorm.DB) authware.Factory {
	return func() (authware.Authenticator, error) {
		return &authwareBackend{db: db}, nil
	}
}

type authwareBackend struct {
	db *gorm.DB
}

func (ab *authwareBackend) AuthUserPassword(ctx context.Context, user, password string) error {
	u, err := gorm.G[User](ab.db).Where(&User{Login: user}).First(ctx)
	if err != nil {
		slog.Warn("Could not find local user", "user", user)
		return authware.ErrUnauthenticated{}
	}

	if err := bcrypt.CompareHashAndPassword(u.Secret, []byte(password)); err != nil {
		return authware.ErrUnauthenticated{}
	}

	return nil
}

func (ab *authwareBackend) UserGroups(_ context.Context, _ string) (map[string]struct{}, error) {
	return make(map[string]struct{}), nil
}

func (ab *authwareBackend) Name() string { return "gameday" }

func (s *Server) uiViewUserList(w http.ResponseWriter, r *http.Request) {
	users, err := gorm.G[User](s.d.Scopes(Paginate(r))).Preload("Profile", nil).Find(r.Context())
	if err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	ctx := pongo2.Context{
		"users": users,
	}
	s.DoTemplate(w, r, "view/admin/user_list.p2", ctx)
}

func (s *Server) uiViewUserForm(w http.ResponseWriter, r *http.Request) {
	id := s.StrToUint(chi.URLParam(r, "id"))
	ctx := pongo2.Context{"user": User{}}

	user, err := gorm.G[User](s.d).Where(&User{ID: id}).First(r.Context())
	if err == nil {
		ctx["user"] = user
	}

	slog.Debug("user value", "value", ctx)
	s.DoTemplate(w, r, "view/admin/user_form.p2", ctx)
}

func (s *Server) uiViewUserFormSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	bytes, err := bcrypt.GenerateFromPassword([]byte(r.FormValue("secret")), bcrypt.DefaultCost)
	if err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	u := User{
		Login:  r.FormValue("login"),
		Secret: bytes,
	}

	profile := Profile{Username: u.Login}
	if err := gorm.G[Profile](s.d).Create(r.Context(), &profile); err != nil {
		slog.Error("Error creating profile", "error", err)
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	u.ProfileID = profile.ID
	if err := gorm.G[User](s.d).Create(r.Context(), &u); err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	http.Redirect(w, r, "/admin/user/", http.StatusSeeOther)
}

func (s *Server) uiViewUserPasswordForm(w http.ResponseWriter, r *http.Request) {
	s.DoTemplate(w, r, "view/admin/user_password.p2", nil)
}

func (s *Server) uiViewUserPasswordFormSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	bytes, err := bcrypt.GenerateFromPassword([]byte(r.FormValue("secret")), bcrypt.DefaultCost)
	if err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	id := s.StrToUint(chi.URLParam(r, "id"))
	if _, err := gorm.G[User](s.d).Where(&User{ID: id}).Update(r.Context(), "Secret", bytes); err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}
	http.Redirect(w, r, "/admin/user/", http.StatusSeeOther)
}

func (s *Server) uiViewUserDeleteForm(w http.ResponseWriter, r *http.Request) {
	id := s.StrToUint(chi.URLParam(r, "id"))
	u, err := gorm.G[User](s.d).Where(&User{ID: id}).First(r.Context())
	if err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}
	s.DoTemplate(w, r, "view/admin/user_delete.p2", pongo2.Context{"user": u})
}

func (s *Server) uiViewUserDeleteFormSubmit(w http.ResponseWriter, r *http.Request) {
	id := s.StrToUint(chi.URLParam(r, "id"))
	u, err := gorm.G[User](s.d).Where(&User{ID: id}).First(r.Context())
	if err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	if _, err := gorm.G[Profile](s.d).Where(&Profile{ID: u.ProfileID}).Delete(r.Context()); err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	if _, err := gorm.G[User](s.d).Where(u).Delete(r.Context()); err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	http.Redirect(w, r, "/admin/user/", http.StatusSeeOther)
}
