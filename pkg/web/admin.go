package web

import (
	"net/http"

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
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	ctx := pongo2.Context{
		"profiles": profiles,
	}
	s.DoTemplate(w, r, "view/admin/profile_list.p2", ctx)
}

func (s *Server) uiViewAdminProfilePermissionsForm(w http.ResponseWriter, r *http.Request) {
	id := s.StrToUint(chi.URLParam(r, "id"))

	profile, err := gorm.G[Profile](s.d).Where(&Profile{ID: id}).First(r.Context())
	if err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	permissions, err := gorm.G[Permission](s.d).Find(r.Context())
	if err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}

	ctx := pongo2.Context{
		"profile":     profile,
		"permissions": permissions,
	}
	s.DoTemplate(w, r, "view/admin/profile_perms.p2", ctx)
}

func (s *Server) uiViewAdminProfilePermissionsSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	id := s.StrToUint(chi.URLParam(r, "id"))

	perms := []Permission{}
	for _, p := range r.Form["permissions"] {
		perms = append(perms, Permission{ID: s.StrToUint(p)})
	}

	if err := s.d.Model(&Profile{ID: id}).Association("Permissions").Replace(perms); err != nil {
		s.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err.Error()})
		return
	}
	http.Redirect(w, r, "/admin/profile/", http.StatusSeeOther)
}
