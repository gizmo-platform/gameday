package game

import (
	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

func init() {
	modules.Register(modules.ModuleInfo{
		Name:         "game",
		Scopes:       []string{"onsite", "cloud"},
		Dependencies: []string{"team"},
		Required:     true,
		Factory: func(db *db.DB, server *web.Server, deps modules.ModuleDeps) modules.Web {
			return New(db, server, deps)
		},
	})
}
