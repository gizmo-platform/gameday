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

	// Register the tie-breaker exactly once, dispatching through the
	// package-level indirection that New() updates with the current
	// database handle.
	modules.RegisterTieBreaker(TieBreakerBESTUnified, func(nums []int, mc int) []int {
		return unifiedTieBreaker(nums, mc)
	})
}
