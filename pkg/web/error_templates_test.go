package web

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flosch/pongo2/v6"
)

func TestErrorTemplatesRender(t *testing.T) {
	sub, _ := fs.Sub(uifs, "ui/p2")
	tpl := pongo2.NewSet("html", pongo2.NewFSLoader(sub))

	cases := []struct {
		name    string
		tmpl    string
		ctx     pongo2.Context
		want    string
	}{
		{"internal", "errors/internal.p2", pongo2.Context{"error": "boom error"}, "boom error"},
		{"unauthorized", "errors/unauthorized.p2", pongo2.Context{"perm": Permission{Module: "best", Grant: "ADMIN"}}, "best:ADMIN"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tt, err := tpl.FromCache(c.tmpl)
			if err != nil {
				t.Fatalf("load %s: %v", c.tmpl, err)
			}
			var sb strings.Builder
			if err := tt.ExecuteWriter(c.ctx, &sb); err != nil {
				t.Fatalf("render %s: %v", c.tmpl, err)
			}
			out := sb.String()
			if !strings.Contains(out, c.want) {
				t.Errorf("rendered output missing %q", c.want)
			}
			if !strings.Contains(out, "<html") {
				t.Errorf("rendered output missing base layout")
			}
		})
	}
}

// pathError is an error with a pointer receiver, so a typed nil of it
// satisfies the error interface.
type pathError struct{ path string }

func (e *pathError) Error() string { return "bad path " + e.path }

// TestDoTemplateRendersErrors verifies an error passed to DoTemplate
// renders as its HTML-escaped message rather than pongo2's "<T Value>"
// placeholder, including wrapped errors, that a typed nil error
// renders empty, and that an empty message renders "unknown error".
func TestDoTemplateRendersErrors(t *testing.T) {
	ws, _ := newTestServer(t)

	var nilErr *pathError
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"errors.New", errors.New("boom error"), "boom error"},
		{"wrapped", fmt.Errorf("loading phase: %w", errors.New("record not found")), "loading phase: record not found"},
		{"pointer receiver", &pathError{path: "/x"}, "bad path /x"},
		{"typed nil", nilErr, "<code></code>"},
		{"escaped", errors.New(`bad value "<script>alert(1)</script>"`), "bad value &quot;&lt;script&gt;alert(1)&lt;/script&gt;&quot;"},
		{"empty message", errors.New(""), "<code>unknown error</code>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/", nil)
			ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": c.err})
			body := w.Body.String()
			if !strings.Contains(body, c.want) {
				t.Errorf("body missing %q:\n%s", c.want, body)
			}
			if strings.Contains(body, " Value>") {
				t.Errorf("body contains pongo2 placeholder:\n%s", body)
			}
			if strings.Contains(body, "<script>alert") {
				t.Errorf("error message rendered without HTML escaping:\n%s", body)
			}
		})
	}
}

// TestAdaptErrorsTruthiness verifies a wrapped error stays true in an
// {% if %} test, as the cloud import/push notifications rely on.
func TestAdaptErrorsTruthiness(t *testing.T) {
	ctx := pongo2.Context{"error": errors.New("x")}
	adaptErrors(ctx)
	tpl, err := pongo2.FromString("{% if error %}yes{% else %}no{% endif %}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := tpl.Execute(ctx)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "yes" {
		t.Errorf("{%% if error %%} = %q, want yes", out)
	}
}
