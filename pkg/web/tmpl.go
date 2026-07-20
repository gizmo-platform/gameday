package web

import (
	"fmt"
	"net/http"

	"github.com/flosch/pongo2/v6"
	"github.com/the-maldridge/authware"
)

func (s *Server) templateErrorHandler(w http.ResponseWriter, err error) {
	fmt.Fprintf(w, "Error while rendering template: %s\n", err)
}

func (s *Server) DoTemplate(w http.ResponseWriter, r *http.Request, tmpl string, ctx pongo2.Context) {
	if ctx == nil {
		ctx = pongo2.Context{}
	}
	ctx["user"], _ = r.Context().Value(authware.UserKey{}).(authware.User)
	ctx["profile"], _ = r.Context().Value(ProfileKey{}).(Profile)
	ctx["navElements"] = s.accessibleNavElements(r)
	ctx["pageBase"] = r.URL.Path
	t, err := s.tpl.FromCache(tmpl)
	if err != nil {
		s.templateErrorHandler(w, err)
		return
	}
	if err := t.ExecuteWriter(ctx, w); err != nil {
		s.templateErrorHandler(w, err)
	}
}

// AddTemplateLoader attempts to add a module owned template loader to
// the cache.
func (s *Server) AddTemplateLoader(t pongo2.TemplateLoader) {
	s.tpl.AddLoader(t)
}
