package game

import (
	"fmt"
	"slices"
)

// PhaseRole names the competitive purpose of a game phase. It is the
// stable cross-module handle for asking about phases without knowing
// the author-assigned phase ID: a phase declares its role in the game
// setup file, and any module can then query the phases carrying a
// given role.
type PhaseRole string

// Roles registered by the game module itself. These cover the standard
// tournament structure; other modules register their own roles from
// their init() functions (the BEST module registers "Wildcard").
const (
	RoleExhibition PhaseRole = "Exhibition"
	RoleSeeding    PhaseRole = "Seeding"
	RoleSemifinal  PhaseRole = "Semifinal"
	RoleFinal      PhaseRole = "Final"
)

// phaseRoles is the global role registry. The empty role is
// deliberately absent: it means "no role assigned" and is accepted by
// ValidPhaseRole, but it is not a registerable role.
var phaseRoles = map[PhaseRole]struct{}{
	RoleExhibition: {},
	RoleSeeding:    {},
	RoleSemifinal:  {},
	RoleFinal:      {},
}

// RegisterPhaseRole adds a role to the global registry. Modules call
// this from their init() function. Panics if the role is empty or
// already registered: roles are a fixed vocabulary, so a collision is
// always a bug. This is deliberately stricter than
// RegisterAdvancementFilter, which only logs and overwrites.
func RegisterPhaseRole(role PhaseRole) {
	if role == "" {
		panic("phase role must not be empty")
	}
	if _, dup := phaseRoles[role]; dup {
		panic(fmt.Sprintf("phase role %q already registered", role))
	}
	phaseRoles[role] = struct{}{}
}

// ValidPhaseRole reports whether role is registered. The empty role is
// always valid and means "no role assigned"; an unregistered non-empty
// role is invalid and is rejected by LintConfig.
func ValidPhaseRole(role PhaseRole) bool {
	if role == "" {
		return true
	}
	_, ok := phaseRoles[role]
	return ok
}

// PhaseRoles returns the registered roles in sorted order for
// deterministic display and lint output. The empty role is not
// included.
func PhaseRoles() []PhaseRole {
	out := make([]PhaseRole, 0, len(phaseRoles))
	for role := range phaseRoles {
		out = append(out, role)
	}
	slices.Sort(out)
	return out
}
