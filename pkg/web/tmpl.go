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

// templateError adapts an error for pongo2.  pongo2 renders a value
// through fmt.Stringer and otherwise prints a "<T Value>" placeholder,
// which is what a plain error produces.  The wrapper is a struct, so
// it is always true in an {% if %} test.
type templateError struct {
	err error
}

func (e templateError) String() string { return e.err.Error() }
func (e templateError) Error() string  { return e.err.Error() }
func (e templateError) Unwrap() error  { return e.err }

// adaptErrors replaces every top-level context value that is an error
// (and not already a fmt.Stringer) with a templateError, so handlers
// can pass errors to templates directly.  A typed nil error becomes
// nil rather than panicking when rendered.
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
		if _, ok := v.(fmt.Stringer); ok {
			continue
		}
		ctx[k] = templateError{err: err}
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
