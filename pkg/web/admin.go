package web

import (
	"log/slog"
	"net/http"
	"sort"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
)

func (s *Server) uiViewAdminLanding(w http.ResponseWriter, r *http.Request) {
	s.DoTemplate(w, r, "view/admin/landing.p2", nil)
}

func (s *Server) uiViewAdminProfileList(w http.ResponseWriter, r *http.Request) {
	profiles, err := gorm.G[Profile](s.d.Scopes(Paginate(r))).Find(r.Context())
	if err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	ctx := pongo2.Context{
		"profiles": profiles,
	}
	s.DoTemplate(w, r, "view/admin/profile_list.p2", ctx)
}

func (s *Server) uiViewAdminProfilePermissionsForm(w http.ResponseWriter, r *http.Request) {
	id := s.StrToUint(chi.URLParam(r, "id"))

	profile, err := gorm.G[Profile](s.d).Where(&Profile{ID: id}).Preload("Permissions", nil).First(r.Context())
	if err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	slog.Debug("Loaded profile", "profile", profile)

	permissions, err := gorm.G[Permission](s.d).Find(r.Context())
	if err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	ctx := pongo2.Context{
		"pprofile":   profile,
		"permGroups": groupPermissions(permissions),
	}
	s.DoTemplate(w, r, "view/admin/profile_perms.p2", ctx)
}

// permissionGroup is one module's fieldset on the profile permissions
// page.
type permissionGroup struct {
	Module      string
	Permissions []Permission
}

// groupPermissions groups permissions by module, with modules and the
// grants within each module in alphabetical order.  Sorting is done
// here rather than in SQL because "grant" is a reserved word in
// Postgres.
func groupPermissions(perms []Permission) []permissionGroup {
	sort.Slice(perms, func(i, j int) bool {
		if perms[i].Module != perms[j].Module {
			return perms[i].Module < perms[j].Module
		}
		return perms[i].Grant < perms[j].Grant
	})

	groups := []permissionGroup{}
	for _, p := range perms {
		if n := len(groups); n == 0 || groups[n-1].Module != p.Module {
			groups = append(groups, permissionGroup{Module: p.Module})
		}
		groups[len(groups)-1].Permissions = append(groups[len(groups)-1].Permissions, p)
	}
	return groups
}

func (s *Server) uiViewAdminProfilePermissionsSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	id := s.StrToUint(chi.URLParam(r, "id"))

	perms := []Permission{}
	for _, p := range r.Form["permissions"] {
		perms = append(perms, Permission{ID: s.StrToUint(p)})
	}

	if err := s.d.Model(&Profile{ID: id}).Association("Permissions").Replace(perms); err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	http.Redirect(w, r, "/admin/profile/", http.StatusSeeOther)
}
