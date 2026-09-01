package web

import (
	"io/fs"
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
