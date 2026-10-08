package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gizmo-platform/gameday/pkg/db"
	"gorm.io/gorm"
)

// newTestServer builds a web server backed by a fresh database. When
// GAMEDAY_TEST_PG_DSN is set it uses Postgres (which enforces foreign
// keys); otherwise it uses the default SQLite driver.
func newTestServer(t *testing.T) (*Server, *db.DB) {
	t.Helper()

	var (
		d   *db.DB
		err error
	)
	if dsn := os.Getenv("GAMEDAY_TEST_PG_DSN"); dsn != "" {
		d, err = db.New(db.WithDriver(db.DriverPostgres), db.WithDSN(dsn))
	} else {
		t.Setenv("GAMEDAY_DB", filepath.Join(t.TempDir(), "test.db"))
		d, err = db.New()
	}
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}

	ws, err := NewServer(WithDB(d))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return ws, d
}

// seedUserWithPermissions creates a user whose profile holds a
// permission, producing a row in the profile_permissions join table.
func seedUserWithPermissions(t *testing.T, d *db.DB) User {
	t.Helper()
	ctx := context.Background()

	login := fmt.Sprintf("deletable-%d", time.Now().UnixNano())

	perm, err := gorm.G[Permission](d.DB).Where(&Permission{Module: "CORE", Grant: "ADMIN"}).First(ctx)
	if err != nil {
		t.Fatalf("find CORE:ADMIN: %v", err)
	}

	profile := Profile{Username: login}
	if err := gorm.G[Profile](d.DB).Create(ctx, &profile); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	if err := d.Model(&profile).Association("Permissions").Replace([]Permission{perm}); err != nil {
		t.Fatalf("assign permission: %v", err)
	}

	u := User{Login: login, Secret: []byte("x"), ProfileID: profile.ID}
	if err := gorm.G[User](d.DB).Create(ctx, &u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

// TestUserDeleteRemovesProfileAndPermissions drives the user-delete
// handler and asserts the user, its profile, and the profile's
// permission join rows are all gone. On Postgres the old ordering
// (delete the profile before clearing the join rows) fails with a
// foreign-key violation; on SQLite it silently leaves orphaned join
// rows. Both are caught here.
func TestUserDeleteRemovesProfileAndPermissions(t *testing.T) {
	ws, d := newTestServer(t)
	u := seedUserWithPermissions(t, d)

	id := strconv.FormatUint(uint64(u.ID), 10)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req := httptest.NewRequest("POST", "http://test.ui/admin/user/"+id+"/delete", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	ws.uiViewUserDeleteFormSubmit(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusSeeOther, w.Body.String())
	}

	var userCount, profileCount, joinCount int64
	if err := d.Model(&User{}).Where("id = ?", u.ID).Count(&userCount).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if userCount != 0 {
		t.Errorf("user rows = %d, want 0", userCount)
	}
	if err := d.Model(&Profile{}).Where("id = ?", u.ProfileID).Count(&profileCount).Error; err != nil {
		t.Fatalf("count profiles: %v", err)
	}
	if profileCount != 0 {
		t.Errorf("profile rows = %d, want 0", profileCount)
	}
	if err := d.Table("profile_permissions").Where("profile_id = ?", u.ProfileID).Count(&joinCount).Error; err != nil {
		t.Fatalf("count join rows: %v", err)
	}
	if joinCount != 0 {
		t.Errorf("profile_permissions rows = %d, want 0", joinCount)
	}
}
