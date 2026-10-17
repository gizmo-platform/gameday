package game

import (
	"slices"
	"testing"
)

func mustPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("%s: expected panic", name)
		}
	}()
	f()
}

func TestBuiltInPhaseRolesValid(t *testing.T) {
	for _, role := range []PhaseRole{RoleExhibition, RoleSeeding, RoleSemifinal, RoleFinal} {
		if !ValidPhaseRole(role) {
			t.Errorf("built-in role %q not valid", role)
		}
	}
	if !ValidPhaseRole("") {
		t.Error("empty role must be valid (means no role assigned)")
	}
	if ValidPhaseRole("NoSuchRole") {
		t.Error("unregistered role reported valid")
	}
}

func TestRegisterPhaseRolePanics(t *testing.T) {
	mustPanic(t, "empty role", func() { RegisterPhaseRole("") })
	mustPanic(t, "duplicate role", func() { RegisterPhaseRole(RoleSeeding) })
}

func TestRegisterPhaseRoleAdds(t *testing.T) {
	const role PhaseRole = "TestRegisteredRole"
	if ValidPhaseRole(role) {
		t.Fatalf("role %q unexpectedly already registered", role)
	}
	RegisterPhaseRole(role)
	if !ValidPhaseRole(role) {
		t.Errorf("role %q not valid after registration", role)
	}
}

func TestPhaseRolesSortedNoEmpty(t *testing.T) {
	roles := PhaseRoles()
	if !slices.IsSorted(roles) {
		t.Errorf("PhaseRoles not sorted: %v", roles)
	}
	if slices.Contains(roles, PhaseRole("")) {
		t.Error("PhaseRoles includes the empty role")
	}
	for _, role := range []PhaseRole{RoleExhibition, RoleSeeding, RoleSemifinal, RoleFinal} {
		if !slices.Contains(roles, role) {
			t.Errorf("PhaseRoles missing built-in %q: %v", role, roles)
		}
	}
}
