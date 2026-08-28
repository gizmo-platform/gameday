package best

import (
	"context"
	"embed"
	"io/fs"
	"path"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

const (
	ModuleName = "BEST"

	PermissionAdmin = "ADMIN"
)

//go:embed ui/*
var efs embed.FS

type Module struct {
	r  chi.Router
	db *db.DB
	ws *web.Server

	basePath string
}

func New(db *db.DB, ws *web.Server, _ modules.ModuleDeps) *Module {
	m := &Module{
		r:  chi.NewRouter(),
		db: db,
		ws: ws,
	}

	if m.ws != nil {
		for _, p := range []string{PermissionAdmin} {
			if err := m.ws.InstallPermission(context.Background(), ModuleName, p); err != nil {
				return nil
			}
		}
	}
	return m
}

func (m *Module) Router() chi.Router {
	return m.r
}

func (m *Module) Migrate() error {
	return m.db.AutoMigrate()
}

func (m *Module) TemplateLoader() pongo2.TemplateLoader {
	sub, _ := fs.Sub(efs, "ui/p2")
	return pongo2.NewFSLoader(sub)
}

func (m *Module) NavList(prefix string) []web.NavElement {
	m.basePath = prefix

	return []web.NavElement{{
		Text: "BEST",
		Children: []web.NavChild{{
			Text:       "Setup",
			Target:     path.Join(prefix, "/setup"),
			Permission: web.Permission{Module: ModuleName, Grant: PermissionAdmin},
		}},
	}}
}
