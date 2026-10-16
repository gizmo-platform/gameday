package web

import (
	"fmt"
	"net/http"
	"reflect"

	"github.com/flosch/pongo2/v6"
	"github.com/the-maldridge/authware"
)

func (s *Server) templateErrorHandler(w http.ResponseWriter, err error) {
	fmt.Fprintf(w, "Error while rendering template: %s\n", err)
}

// adaptErrors replaces every top-level context value that is an error
// with its message, so handlers can pass errors to templates directly.
// pongo2 would otherwise print a plain error as a "<T Value>"
// placeholder.  The message must be a plain string: pongo2 only
// autoescapes string values, and error messages often carry user input
// (form values, cloud response bodies).  An empty message becomes
// "unknown error" so {% if error %} stays true, and a typed nil error
// becomes nil rather than panicking when rendered.
func adaptErrors(ctx pongo2.Context) {
	for k, v := range ctx {
		err, ok := v.(error)
		if !ok {
			continue
		}
		if rv := reflect.ValueOf(v); rv.Kind() == reflect.Ptr && rv.IsNil() {
			ctx[k] = nil
			continue
		}
		msg := err.Error()
		if msg == "" {
			msg = "unknown error"
		}
		ctx[k] = msg
	}
}

// DoTemplate renders tmpl with the per-request keys user, profile,
// navElements, and pageBase injected, overwriting any caller-supplied
// values under those names.  Error values in the context render as
// their message (see adaptErrors).
func (s *Server) DoTemplate(w http.ResponseWriter, r *http.Request, tmpl string, ctx pongo2.Context) {
	if ctx == nil {
		ctx = pongo2.Context{}
	}
	adaptErrors(ctx)
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
