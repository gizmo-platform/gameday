package best

import (
	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

func init() {
	modules.Register(modules.ModuleInfo{
		Name:         "best",
		Scopes:       []string{"onsite"},
		Dependencies: []string{},
		Required:     false,
		Factory: func(db *db.DB, server *web.Server, deps modules.ModuleDeps) modules.Web {
			return New(db, server, deps)
		},
	})
}
