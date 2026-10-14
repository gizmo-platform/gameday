package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/flosch/pongo2/v6"
	"github.com/the-maldridge/authware"
	"gorm.io/gorm"
)

type Permission struct {
	ID     uint
	Module string
	Grant  string
}

func (p Permission) String() string {
	return p.Module + ":" + p.Grant
}

func (p Permission) Is(t Permission) bool {
	return p.Module == t.Module && p.Grant == t.Grant
}

func (p Permission) IsEmpty() bool {
	return p.Module == "" && p.Grant == ""
}

func (s *Server) InstallPermission(ctx context.Context, module, grant string) error {
	p := &Permission{Module: module, Grant: grant}
	_, err := gorm.G[Permission](s.d).Where(p).First(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := gorm.G[Permission](s.d).Create(ctx, p); err != nil {
			slog.Warn("Error installing permission", "error", err)
			return err
		}
	} else if err != nil {
		slog.Warn("Error testing permission existence", "error", err)
		return err
	}

	return nil
}

// denialStatus returns 401 when the request has no authenticated user and
// 403 when a user is present but lacks the permission.
func denialStatus(r *http.Request) int {
	if _, ok := r.Context().Value(authware.UserKey{}).(authware.User); !ok {
		return http.StatusUnauthorized
	}
	return http.StatusForbidden
}

// wantsJSON reports whether the request prefers a JSON response, so
// that API endpoints can be denied with a machine-readable body
// instead of the HTML unauthorized page.
func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func (s *Server) renderUnauthorized(w http.ResponseWriter, r *http.Request, p Permission) {
	status := denialStatus(r)
	if wantsJSON(r) {
		msg := "You do not have permission to access this resource."
		if status == http.StatusUnauthorized {
			msg = "Please log in to access this resource."
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
		return
	}
	w.WriteHeader(status)
	s.DoTemplate(w, r, "errors/unauthorized.p2", pongo2.Context{"perm": p})
}

func (s *Server) RequirePermission(p Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			profile, _ := r.Context().Value(ProfileKey{}).(Profile)
			if !profile.HasPermission(p) {
				s.renderUnauthorized(w, r, p)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) GuardRoute(p Permission, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, _ := r.Context().Value(ProfileKey{}).(Profile)
		if !profile.HasPermission(p) {
			s.renderUnauthorized(w, r, p)
			return
		}
		handler(w, r)
	}
}

func (s *Server) accessibleNavElements(r *http.Request) []NavElement {
	profile, _ := r.Context().Value(ProfileKey{}).(Profile)

	out := []NavElement{}
	for _, elem := range s.nav {
		e := NavElement{Weight: elem.Weight, Text: elem.Text}
		for _, child := range elem.Children {
			if profile.HasPermission(child.Permission) || child.Permission.IsEmpty() {
				e.Children = append(e.Children, child)
			}
		}
		if len(e.Children) > 0 {
			out = append(out, e)
		}
	}

	return out
}

func (s *Server) filterHasPermission(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	profile, ok := in.Interface().(Profile)
	if !ok {
		slog.Warn("Tried to convert something that isn't a profile", "something", in)
		return pongo2.AsValue(false), nil
	}

	st, _ := param.Interface().(string)
	parts := strings.Split(st, ":")
	if len(parts) != 2 {
		return pongo2.AsValue(false), nil
	}
	return pongo2.AsValue(profile.HasPermission(Permission{Module: parts[0], Grant: parts[1]})), nil
}

func (s *Server) filterHasPermissionExact(in *pongo2.Value, param *pongo2.Value) (*pongo2.Value, *pongo2.Error) {
	profile, ok := in.Interface().(Profile)
	if !ok {
		slog.Warn("Tried to convert something that isn't a profile", "something", in)
		return pongo2.AsValue(false), nil
	}

	st, _ := param.Interface().(string)
	parts := strings.Split(st, ":")
	if len(parts) != 2 {
		return pongo2.AsValue(false), nil
	}
	return pongo2.AsValue(profile.HasPermissionExact(Permission{Module: parts[0], Grant: parts[1]})), nil
}
