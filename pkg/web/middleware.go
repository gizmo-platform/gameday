package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/flosch/pongo2/v6"
	"github.com/the-maldridge/authware"
	"gorm.io/gorm"
)

// ProfileKey is used to retrieve the profile from the request
// context.
type ProfileKey struct{}

// Profile is used to store attributes related to a given user.
type Profile struct {
	ID          uint
	Username    string
	Permissions []Permission `gorm:"many2many:profile_permissions;"`
}

// HasPermission checks if a profile has a given permission.
func (p Profile) HasPermission(test Permission) bool {
	for _, perm := range p.Permissions {
		if perm.Is(test) ||
			perm.Is(Permission{Module: "CORE", Grant: "ADMIN"}) ||
			perm.Is(Permission{Module: test.Module, Grant: "ADMIN"}) {
			return true
		}
	}
	return false
}

// HasPermissionExact checks if a profile has a given permission
// directly and is not satisfied by other admin roles.
func (p Profile) HasPermissionExact(test Permission) bool {
	for _, perm := range p.Permissions {
		if perm.Is(test) {
			return true
		}
	}
	return false
}

func (s *Server) profileMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := r.Context().Value(authware.UserKey{}).(authware.User)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		profile, err := gorm.G[Profile](s.d).
			Where(&Profile{Username: user.Identity}).
			Preload("Permissions", nil).
			First(r.Context())
		if errors.Is(err, gorm.ErrRecordNotFound) {
			profile := Profile{Username: user.Identity}
			slog.Info("Creating profile", "profile", profile)
			if err := gorm.G[Profile](s.d).Create(r.Context(), &profile); err != nil {
				slog.Error("Error creating user", "error", err)
				s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
				return
			}

		}
		ctx := context.WithValue(r.Context(), ProfileKey{}, profile)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
