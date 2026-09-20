package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/flosch/pongo2/v6"
)

func TestDebugTemplateLoader(t *testing.T) {
	embedded := fstest.MapFS{
		"ui/p2/views/x.p2":       &fstest.MapFile{Data: []byte("embedded X")},
		"ui/p2/only_embedded.p2": &fstest.MapFile{Data: []byte("embedded only")},
	}

	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "views"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "views", "x.p2"), []byte("disk X"), 0o644); err != nil {
		t.Fatal(err)
	}

	ldr := DebugTemplateLoader(tmp, embedded)

	cases := []struct {
		base, name, want string
	}{
		{"", "views/x.p2", "views/x.p2"},
		{"views/x.p2", "../base.p2", "base.p2"},
		{"partials/scorecard.p2", "scorecard_widget_count.p2", "partials/scorecard_widget_count.p2"},
	}
	for _, c := range cases {
		if got := ldr.Abs(c.base, c.name); got != c.want {
			t.Errorf("Abs(%q, %q) = %q, want %q", c.base, c.name, got, c.want)
		}
	}

	tpl := pongo2.NewSet("html", ldr)
	tpl.Debug = true

	cases2 := []struct {
		name, want string
	}{
		{"views/x.p2", "disk X"},
		{"only_embedded.p2", "embedded only"},
	}
	for _, c := range cases2 {
		tt, err := tpl.FromCache(c.name)
		if err != nil {
			t.Fatalf("load %s: %v", c.name, err)
		}
		var sb strings.Builder
		if err := tt.ExecuteWriter(pongo2.Context{}, &sb); err != nil {
			t.Fatalf("render %s: %v", c.name, err)
		}
		if got := sb.String(); got != c.want {
			t.Errorf("render %s = %q, want %q", c.name, got, c.want)
		}
	}

	if _, err := tpl.FromCache("nope.p2"); err == nil {
		t.Error("expected error for missing template, got nil")
	}

	var _ fs.FS = &debugTemplateFS{}
}
