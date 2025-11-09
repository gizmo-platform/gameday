package game

import (
	"embed"
	"io/fs"
	"path"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"

	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

//go:embed ui/*
var efs embed.FS

// Field represents a field of play, and has at least one position on
// which a team can be placed.
type Config struct {
	Field ConfigField
}

type ConfigField struct {
	Positions []FieldPosition
}

// A FieldPosition has a name and a pair of colors, one of which
// should be lighter than the other to allow banding in tables.
type FieldPosition struct {
	ID     uint
	Name   string
	Color1 string
	Color2 string
}

// Field represents a single field that is available for scheduling.
type Field struct {
	ID   uint
	Name string
}

type Module struct {
	r  chi.Router
	db *db.DB
	ws *web.Server

	basePath string
}

func New(d *db.DB, w *web.Server) *Module {
	m := Module{
		r:  chi.NewRouter(),
		db: d,
		ws: w,
	}

	m.r.Route("/", func(r chi.Router) {
		r.Get("/", m.uiViewGame)
		r.Get("/setup", m.uiViewSetupForm)
		r.Post("/setup", m.uiViewSetupSubmit)

		r.Route("/fields", func(r chi.Router) {
			r.Get("/", m.uiViewFieldList)
			r.Get("/add", m.uiViewFieldForm)
			r.Post("/add", m.uiViewFieldSubmit)

			r.Get("/{id}/edit", m.uiViewFieldForm)
			r.Post("/{id}/edit", m.uiViewFieldSubmit)
		})
	})

	return &m
}

func (m *Module) Router() chi.Router {
	return m.r
}

func (m *Module) Migrate() error {
	return m.db.AutoMigrate(
		Field{},
		FieldPosition{},
	)
}

func (m *Module) TemplateLoader() pongo2.TemplateLoader {
	sub, _ := fs.Sub(efs, "ui/p2")
	return pongo2.NewFSLoader(sub)
}

func (m *Module) NavList(prefix string) []web.NavElement {
	m.basePath = prefix

	return []web.NavElement{{
		Text: "Game",
		Children: []web.NavChild{{
			Text:   "Configuration",
			Target: prefix,
		}, {
			Text:   "Setup",
			Target: path.Join(prefix, "/setup"),
		}, {
			Text:   "Fields",
			Target: path.Join(prefix, "/fields/"),
		}},
	}}
}
