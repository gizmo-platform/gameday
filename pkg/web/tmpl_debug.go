package web

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/flosch/pongo2/v6"
)

// debugTemplateFS is an fs.FS rooted at a module's on-disk template
// directory that falls back to the module's embedded templates for
// files that do not yet exist on disk. It lets the standard
// pongo2.NewFSLoader read templates straight from the source tree in
// template debug mode while still serving any template that has not
// been created on disk.
type debugTemplateFS struct {
	root     string
	embedded fs.FS
}

func (f *debugTemplateFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrInvalid
	}
	if info, err := os.Stat(filepath.Join(f.root, name)); err == nil && !info.IsDir() {
		return os.Open(filepath.Join(f.root, name))
	}
	return f.embedded.Open(name)
}

// DebugTemplateLoader returns a standard pongo2.NewFSLoader reading
// templates from the on-disk directory dir (relative to the process
// working directory), falling back to the module's embedded templates
// (under "ui/p2") for files not present on disk. Combined with a
// TemplateSet built in Debug mode, template edits on disk are picked
// up on every render.
func DebugTemplateLoader(dir string, embedded fs.FS) pongo2.TemplateLoader {
	sub, _ := fs.Sub(embedded, "ui/p2")
	return pongo2.NewFSLoader(&debugTemplateFS{root: dir, embedded: sub})
}
