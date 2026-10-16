package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
)

// TestGroupPermissions verifies permissions are grouped by module with
// modules and grants in alphabetical order.
func TestGroupPermissions(t *testing.T) {
	groups := groupPermissions([]Permission{
		{Module: "GAME", Grant: "TIMEKEEPER"},
		{Module: "CORE", Grant: "ADMIN"},
		{Module: "GAME", Grant: "ADMIN"},
		{Module: "BEST", Grant: "ADMIN"},
	})

	got := []string{}
	for _, g := range groups {
		grants := []string{}
		for _, p := range g.Permissions {
			grants = append(grants, p.Grant)
		}
		got = append(got, g.Module+"="+strings.Join(grants, ","))
	}
	want := "BEST=ADMIN CORE=ADMIN GAME=ADMIN,TIMEKEEPER"
	if strings.Join(got, " ") != want {
		t.Errorf("groups = %v, want %s", got, want)
	}
}

// TestProfilePermissionsForm renders the profile permissions page and
// asserts one fieldset per module with a title-cased legend, a
// title-cased checkbox per grant, and the profile's grants checked.
func TestProfilePermissionsForm(t *testing.T) {
	ws, d := newTestServer(t)
	ctx := context.Background()

	for _, p := range [][2]string{{"GAME", "ADMIN"}, {"GAME", "TIMEKEEPER"}, {"TEAM", "ADMIN"}} {
		if err := ws.InstallPermission(ctx, p[0], p[1]); err != nil {
			t.Fatalf("install %v: %v", p, err)
		}
	}
	timekeeper, err := gorm.G[Permission](d.DB).Where(&Permission{Module: "GAME", Grant: "TIMEKEEPER"}).First(ctx)
	if err != nil {
		t.Fatalf("load permission: %v", err)
	}
	prof := Profile{Username: fmt.Sprintf("perms-%d", time.Now().UnixNano()), Permissions: []Permission{timekeeper}}
	if err := d.Create(&prof).Error; err != nil {
		t.Fatalf("create profile: %v", err)
	}

	id := fmt.Sprint(prof.ID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req := httptest.NewRequest("GET", "/admin/profile/"+id+"/permissions", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	ws.uiViewAdminProfilePermissionsForm(w, req)
	body := w.Body.String()

	for _, module := range []string{"Core", "Game", "Team"} {
		if !strings.Contains(body, `<legend class="label">`+module+`</legend>`) {
			t.Errorf("missing fieldset legend %q", module)
		}
	}
	if got := strings.Count(body, "<fieldset"); got != 3 {
		t.Errorf("fieldsets = %d, want 3", got)
	}
	if !regexp.MustCompile(`value="\d+"\s*checked\s*/>\s*Timekeeper`).MatchString(body) {
		t.Error("Timekeeper checkbox not checked for a profile that holds it")
	}
	if regexp.MustCompile(`checked\s*/>\s*Admin`).MatchString(body) {
		t.Error("an Admin checkbox is checked for a profile without it")
	}
	if strings.Contains(body, "TIMEKEEPER") || strings.Contains(body, "<select") {
		t.Error("page still renders raw grant names or the old select")
	}

	// Submitting checkboxes replaces the profile's permissions.
	game, _ := gorm.G[Permission](d.DB).Where(&Permission{Module: "GAME", Grant: "ADMIN"}).First(ctx)
	form := fmt.Sprintf("permissions=%d&permissions=%d", game.ID, timekeeper.ID)
	req = httptest.NewRequest("POST", "/admin/profile/"+id+"/permissions", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w = httptest.NewRecorder()
	ws.uiViewAdminProfilePermissionsSubmit(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("submit = %d, want 303: %s", w.Code, w.Body.String())
	}
	got, err := gorm.G[Profile](d.DB).Where(&Profile{ID: prof.ID}).Preload("Permissions", nil).First(ctx)
	if err != nil {
		t.Fatalf("reload profile: %v", err)
	}
	if len(got.Permissions) != 2 {
		t.Errorf("profile permissions = %v, want GAME:ADMIN and GAME:TIMEKEEPER", got.Permissions)
	}
}
