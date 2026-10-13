package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/the-maldridge/authware"
	"gorm.io/gorm"
)

// TestAuthTokenResolvesUser drives the token authenticator directly:
// a valid token resolves to the owning user's login, and an unknown
// token is rejected.
func TestAuthTokenResolvesUser(t *testing.T) {
	ws, d := newTestServer(t)
	ctx := context.Background()

	u := User{Login: "token-user", Secret: []byte("x")}
	if err := gorm.G[User](d.DB).Create(ctx, &u); err != nil {
		t.Fatalf("create user: %v", err)
	}

	plaintext, hash, prefix, err := generateAPIToken()
	if err != nil {
		t.Fatalf("generateAPIToken: %v", err)
	}
	if !strings.HasPrefix(plaintext, "gd_") {
		t.Errorf("plaintext = %q, want gd_ prefix", plaintext)
	}
	if prefix != plaintext[:8] {
		t.Errorf("prefix = %q, want %q", prefix, plaintext[:8])
	}

	tok := APIToken{Label: "test", Prefix: prefix, Hash: hash, CreatedAt: time.Now(), UserID: u.ID}
	if err := gorm.G[APIToken](d.DB).Create(ctx, &tok); err != nil {
		t.Fatalf("create token: %v", err)
	}

	ab := &authwareBackend{db: d.DB}

	got, err := ab.AuthToken(ctx, plaintext)
	if err != nil {
		t.Fatalf("AuthToken(valid): %v", err)
	}
	if got.Identity != u.Login {
		t.Errorf("Identity = %q, want %q", got.Identity, u.Login)
	}

	if _, err := ab.AuthToken(ctx, "gd_bogus"); err == nil {
		t.Error("AuthToken(bogus): expected error, got nil")
	}

	_ = ws
}

// TestUserDeleteRemovesTokens drives the user-delete handler and
// asserts the user's API tokens are removed alongside the user.
func TestUserDeleteRemovesTokens(t *testing.T) {
	ws, d := newTestServer(t)
	ctx := context.Background()

	u := User{Login: "token-deletable", Secret: []byte("x")}
	p := Profile{Username: "token-deletable"}
	if err := gorm.G[Profile](d.DB).Create(ctx, &p); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	u.ProfileID = p.ID
	if err := gorm.G[User](d.DB).Create(ctx, &u); err != nil {
		t.Fatalf("create user: %v", err)
	}

	_, hash, prefix, err := generateAPIToken()
	if err != nil {
		t.Fatalf("generateAPIToken: %v", err)
	}
	tok := APIToken{Label: "test", Prefix: prefix, Hash: hash, CreatedAt: time.Now(), UserID: u.ID}
	if err := gorm.G[APIToken](d.DB).Create(ctx, &tok); err != nil {
		t.Fatalf("create token: %v", err)
	}

	id := fmt.Sprintf("%d", u.ID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req := httptest.NewRequest("POST", "http://test.ui/admin/user/"+id+"/delete", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	ws.uiViewUserDeleteFormSubmit(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusSeeOther, w.Body.String())
	}

	var tokenCount int64
	if err := d.Model(&APIToken{}).Where("user_id = ?", u.ID).Count(&tokenCount).Error; err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if tokenCount != 0 {
		t.Errorf("token rows = %d, want 0", tokenCount)
	}
}

// TestTokenCreateHandler drives the token-create handler and asserts
// a token row is stored (hash only) and the plaintext is rendered
// exactly once on the response.
func TestTokenCreateHandler(t *testing.T) {
	ws, d := newTestServer(t)
	ctx := context.Background()

	u := User{Login: "token-creator", Secret: []byte("x")}
	if err := gorm.G[User](d.DB).Create(ctx, &u); err != nil {
		t.Fatalf("create user: %v", err)
	}

	id := fmt.Sprintf("%d", u.ID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req := httptest.NewRequest("POST", "http://test.ui/admin/user/"+id+"/tokens", strings.NewReader("label=ci"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	ws.uiViewUserTokenCreate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}

	tokens, err := gorm.G[APIToken](d.DB).Where(&APIToken{UserID: u.ID}).Find(ctx)
	if err != nil {
		t.Fatalf("find tokens: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("token rows = %d, want 1", len(tokens))
	}
	if tokens[0].Label != "ci" {
		t.Errorf("label = %q, want %q", tokens[0].Label, "ci")
	}
	if strings.Contains(w.Body.String(), tokens[0].Hash) {
		t.Error("response contains the stored hash, want plaintext only")
	}
	if !strings.Contains(w.Body.String(), "gd_") {
		t.Error("response does not contain the plaintext token")
	}
}

// TestBearerTokenEndToEnd drives the full router with a Bearer token
// and asserts authware resolves it to the owning user's profile and
// permissions. The authware factory registry is process-global and
// first-registration-wins, so a unique mechanism name that closes
// over this test's DB is registered before NewServer runs.
func TestBearerTokenEndToEnd(t *testing.T) {
	name := fmt.Sprintf("testtok%d", time.Now().UnixNano())

	var d *db.DB
	if dsn := os.Getenv("GAMEDAY_TEST_PG_DSN"); dsn != "" {
		var err error
		d, err = db.New(db.WithDriver(db.DriverPostgres), db.WithDSN(dsn))
		if err != nil {
			t.Fatalf("db.New: %v", err)
		}
	} else {
		t.Setenv("GAMEDAY_DB", filepath.Join(t.TempDir(), "test.db"))
		var err error
		d, err = db.New()
		if err != nil {
			t.Fatalf("db.New: %v", err)
		}
	}

	authware.RegisterTokenFactory(name, func() (authware.TokenAuthenticator, error) {
		return &authwareBackend{db: d.DB}, nil
	})
	t.Setenv("AUTHWARE_TOKEN_MECHS", name)

	ws, err := NewServer(WithDB(d))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ctx := context.Background()
	admin, err := gorm.G[User](d.DB).Where(&User{Login: "admin"}).First(ctx)
	if err != nil {
		t.Fatalf("find admin: %v", err)
	}

	plaintext, hash, prefix, err := generateAPIToken()
	if err != nil {
		t.Fatalf("generateAPIToken: %v", err)
	}
	tok := APIToken{Label: "e2e", Prefix: prefix, Hash: hash, CreatedAt: time.Now(), UserID: admin.ID}
	if err := gorm.G[APIToken](d.DB).Create(ctx, &tok); err != nil {
		t.Fatalf("create token: %v", err)
	}

	do := func(auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://test.ui/admin/user/", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		ws.r.ServeHTTP(w, req)
		return w
	}

	// A valid token resolves to the admin profile with CORE:ADMIN,
	// so the guarded user list renders.
	w := do("Bearer " + plaintext)
	if w.Code != http.StatusOK {
		t.Fatalf("valid token: status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "Create User") {
		t.Error("valid token: body does not contain the user list")
	}

	// An invalid token is rejected with 401 before the permission
	// check runs.
	w = do("Bearer gd_bogus")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("bogus token: status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	// No credentials at all: the permission guard renders the
	// unauthorized page, not the user list.
	w = do("")
	if w.Code != http.StatusOK {
		t.Fatalf("no auth: status = %d, want %d", w.Code, http.StatusOK)
	}
	if strings.Contains(w.Body.String(), "Create User") {
		t.Error("no auth: body contains the user list, want the unauthorized page")
	}
}
