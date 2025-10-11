package modules

import (
	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"

	"github.com/gizmo-platform/gameday/pkg/web"
)

type Web interface {
	Router() chi.Router
	Migrate() error
	NavList(string) []web.NavElement
	TemplateLoader() pongo2.TemplateLoader
}
