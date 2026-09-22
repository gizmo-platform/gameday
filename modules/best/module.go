package best

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"path"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

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

	pAdmin := web.Permission{Module: ModuleName, Grant: PermissionAdmin}
	m.r.Route("/", func(r chi.Router) {
		r.Use(m.ws.RequirePermission(pAdmin))

		r.Route("/scores", func(r chi.Router) {
			r.Get("/", m.ws.GuardRoute(pAdmin, m.uiViewScores))
			r.Post("/{id}/{field}", m.ws.GuardRoute(pAdmin, m.uiViewScoreSet))
			r.Get("/import", m.ws.GuardRoute(pAdmin, m.uiViewImportScores))
			r.Post("/import", m.ws.GuardRoute(pAdmin, m.uiViewImportScoresSubmit))
		})
	})

	return m
}

func (m *Module) Router() chi.Router {
	return m.r
}

func (m *Module) Migrate() error {
	if err := m.db.AutoMigrate(
		ScoreType{},
		TeamScoreValue{},
	); err != nil {
		return err
	}

	defaults := []ScoreType{
		{Key: "notebook", Name: "Notebook", Max: 300, Order: 1},
		{Key: "marketing", Name: "Marketing", Max: 250, Order: 2},
		{Key: "poster", Name: "Poster", Max: 100, Order: 3},
		{Key: "video", Name: "Video", Max: 100, Order: 4},
	}

	for _, dt := range defaults {
		existing, err := gorm.G[ScoreType](m.db.DB).Where(&ScoreType{Key: dt.Key}).First(context.Background())
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if existing.ID == 0 {
			if err := m.db.Create(&dt).Error; err != nil {
				return err
			}
		} else if existing.Name != dt.Name || existing.Max != dt.Max || existing.Order != dt.Order {
			if err := m.db.Model(&existing).Updates(map[string]any{"name": dt.Name, "max": dt.Max, "order": dt.Order}).Error; err != nil {
				return err
			}
		}
	}

	return nil
}

func (m *Module) TemplateLoader() pongo2.TemplateLoader {
	if m.ws != nil && m.ws.TemplateDebug() {
		return web.DebugTemplateLoader("modules/best/ui/p2", efs)
	}
	sub, _ := fs.Sub(efs, "ui/p2")
	return pongo2.NewFSLoader(sub)
}

func (m *Module) NavList(prefix string) []web.NavElement {
	m.basePath = prefix

	return []web.NavElement{{
		Text: "BEST",
		Children: []web.NavChild{{
			Text:       "Scores",
			Target:     path.Join(prefix, "/scores"),
			Permission: web.Permission{Module: ModuleName, Grant: PermissionAdmin},
		}, {
			Text:       "Bulk Import",
			Target:     path.Join(prefix, "/scores/import"),
			Permission: web.Permission{Module: ModuleName, Grant: PermissionAdmin},
		}},
	}}
}
