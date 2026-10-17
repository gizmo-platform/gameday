package best

import (
	"testing"

	"github.com/gizmo-platform/gameday/modules/game"
)

// TestWildcardRoleRegistered verifies linking the BEST module
// registers its Wildcard role with the game module's role registry, so
// setup files may tag a phase with Role: Wildcard.
func TestWildcardRoleRegistered(t *testing.T) {
	if !game.ValidPhaseRole(RoleWildcard) {
		t.Errorf("role %q not registered with the game module", RoleWildcard)
	}
}
