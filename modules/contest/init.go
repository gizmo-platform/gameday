package contest

import (
	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

func init() {
	modules.Register(modules.ModuleInfo{
		Name:         "contest",
		Scopes:       []string{"cloud", "onsite"},
		Dependencies: []string{"team", "game"},
		Required:     true,
		Factory: func(scope string, db *db.DB, server *web.Server, deps modules.ModuleDeps) modules.Web {
			if scope == "cloud" {
				return New(scope, db, server, deps)
			}
			return NewOnsite(scope, db, server, deps)
		},
	})
}
