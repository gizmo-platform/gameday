package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"github.com/the-maldridge/authware"
	"gorm.io/gorm"
)

// APIToken is a bearer token that can be used to authenticate API
// calls. Only the SHA-256 digest of the plaintext token is stored;
// the plaintext is shown exactly once at creation time.
type APIToken struct {
	ID uint

	Label string
	// Prefix is the first 8 characters of the plaintext token, kept
	// for display so tokens can be told apart.
	Prefix string
	// Hash is the SHA-256 hex digest of the plaintext token.
	Hash string `gorm:"unique"`

	CreatedAt time.Time

	User   User
	UserID uint
}

// generateAPIToken returns a new random token as (plaintext, hash,
// prefix). The plaintext is 32 random bytes hex-encoded with a "gd_"
// prefix; the hash is the SHA-256 hex digest of the plaintext.
func generateAPIToken() (plaintext, hash, prefix string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", "", err
	}
	plaintext = "gd_" + hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(plaintext))
	hash = hex.EncodeToString(sum[:])
	prefix = plaintext[:8]
	return plaintext, hash, prefix, nil
}

// newAuthwareTokenBackend returns a token factory whose authenticator
// resolves bearer tokens to the owning user. The same authwareBackend
// struct serves both the password and token interfaces.
func newAuthwareTokenBackend(db *gorm.DB) authware.TokenFactory {
	return func() (authware.TokenAuthenticator, error) {
		return &authwareBackend{db: db}, nil
	}
}

// AuthToken resolves a bearer token to the user that owns it.
func (ab *authwareBackend) AuthToken(ctx context.Context, token string) (authware.User, error) {
	sum := sha256.Sum256([]byte(token))
	digest := hex.EncodeToString(sum[:])

	t, err := gorm.G[APIToken](ab.db).Where(&APIToken{Hash: digest}).First(ctx)
	if err != nil {
		return authware.User{}, authware.ErrUnauthenticated{}
	}

	u, err := gorm.G[User](ab.db).Where(&User{ID: t.UserID}).First(ctx)
	if err != nil {
		return authware.User{}, authware.ErrUnauthenticated{}
	}

	return authware.User{Identity: u.Login, Groups: make(map[string]struct{})}, nil
}

func (s *Server) uiViewUserTokens(w http.ResponseWriter, r *http.Request) {
	id := s.StrToUint(chi.URLParam(r, "id"))
	if id == 0 {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": "invalid user id"})
		return
	}

	u, err := gorm.G[User](s.d).Where("id = ?", id).First(r.Context())
	if err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	tokens, err := gorm.G[APIToken](s.d).Where("user_id = ?", id).Order("id DESC").Find(r.Context())
	if err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	s.DoTemplate(w, r, "view/admin/user_tokens.p2", pongo2.Context{
		"target": u,
		"tokens": tokens,
	})
}

func (s *Server) uiViewUserTokenCreate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	id := s.StrToUint(chi.URLParam(r, "id"))
	if id == 0 {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": "invalid user id"})
		return
	}
	if _, err := gorm.G[User](s.d).Where("id = ?", id).First(r.Context()); err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	plaintext, hash, prefix, err := generateAPIToken()
	if err != nil {
		slog.Error("Error generating API token", "error", err)
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	t := APIToken{
		Label:     r.FormValue("label"),
		Prefix:    prefix,
		Hash:      hash,
		CreatedAt: time.Now(),
		UserID:    id,
	}
	if err := gorm.G[APIToken](s.d).Create(r.Context(), &t); err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	// The plaintext is shown exactly once, directly on this page. A
	// redirect cannot carry the one-time secret.
	s.DoTemplate(w, r, "view/admin/user_token_created.p2", pongo2.Context{
		"target": User{ID: id},
		"token":  plaintext,
	})
}

func (s *Server) uiViewUserTokenDelete(w http.ResponseWriter, r *http.Request) {
	id := s.StrToUint(chi.URLParam(r, "id"))
	tid := s.StrToUint(chi.URLParam(r, "tid"))
	if id == 0 || tid == 0 {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": "invalid id"})
		return
	}

	// Scoped to the owning user so a token can only be deleted
	// through its owner's page.
	if _, err := gorm.G[APIToken](s.d).Where("id = ? AND user_id = ?", tid, id).Delete(r.Context()); err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/user/%d/tokens", id), http.StatusSeeOther)
}
