package web

import (
	"net/http"
	"log/slog"
	"context"
	"errors"

	"gorm.io/gorm"
	"github.com/flosch/pongo2/v6"
	"github.com/the-maldridge/authware"
)

// ProfileKey is used to retrieve the profile from the request
// context.
type ProfileKey struct{}

// Profile is used to store attributes related to a given user.
type Profile struct {
	Username string
}

func (s *Server) profileMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := r.Context().Value(authware.UserKey{}).(authware.User)
		if !ok {
			next.ServeHTTP(w, r)
		}
		profile, err := gorm.G[Profile](s.d).Where(&Profile{Username: user.Identity}).First(r.Context())
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
