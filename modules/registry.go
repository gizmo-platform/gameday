package modules

import (
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

// ModuleDeps is a map of already-created modules, passed to factory
// functions after topological sort ensures dependency ordering.
type ModuleDeps = map[string]Web

// Factory creates a new module given the database, webserver, and
// already-created dependencies.
type Factory func(db *db.DB, server *web.Server, deps ModuleDeps) Web

// ModuleInfo declares metadata for a single module registration.
type ModuleInfo struct {
	// Name is the unique identifier for the module (e.g. "team").
	Name string

	// Scopes lists the entrypoints this module supports (e.g. "onsite",
	// "cloud"). The module is only eligible when the entrypoint's scope
	// matches.
	Scopes []string

	// Dependencies lists module names that must be created before this
	// one. An empty list means no ordering constraints.
	Dependencies []string

	// Required modules are always loaded for any matching scope,
	// regardless of the GAMEDAY_MODULES env var. A required module
	// may not depend on a non-required module.
	Required bool

	// Factory creates the module instance.
	Factory Factory
}

var registry = make(map[string]ModuleInfo)

// Register adds a module to the global registry. Modules call this
// from their init() function. Panics if the name is already taken
// or if a required module depends on a non-required one.
func Register(info ModuleInfo) {
	if _, dup := registry[info.Name]; dup {
		panic(fmt.Sprintf("module %q already registered", info.Name))
	}

	if info.Required {
		for _, dep := range info.Dependencies {
			if other, ok := registry[dep]; ok && !other.Required {
				panic(fmt.Sprintf("required module %q depends on non-required module %q", info.Name, dep))
			}
		}
	}

	registry[info.Name] = info
}

// ResolveModules filters registered modules by the given scope,
// applies the GAMEDAY_MODULES env var (comma-separated whitelist),
// validates the dependency graph, and returns modules in
// topological order plus a ready-to-use map keyed by name.
//
// When GAMEDAY_MODULES is empty, only required modules for the
// given scope are loaded. When set, listed modules are loaded in
// addition to any required ones.
func ResolveModules(scope string, db *db.DB, server *web.Server) ([]string, ModuleDeps) {
	// 1. Filter by scope
	candidates := make(map[string]ModuleInfo)
	for name, info := range registry {
		if slices.Contains(info.Scopes, scope) {
			candidates[name] = info
		}
	}

	// 2. Build the final selected set
	selected := make(map[string]ModuleInfo)
	for name, info := range candidates {
		if info.Required {
			selected[name] = info
		}
	}

	// 3. Apply GAMEDAY_MODULES env var
	if env := os.Getenv("GAMEDAY_MODULES"); env != "" {
		for name := range strings.SplitSeq(env, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if info, ok := candidates[name]; ok {
				selected[name] = info
			} else {
				slog.Warn("Module in GAMEDAY_MODULES not found or not in scope", "module", name, "scope", scope)
			}
		}
	}

	// 4. Validate all dependencies are satisfied
	for name, info := range selected {
		for _, dep := range info.Dependencies {
			if _, ok := selected[dep]; !ok {
				slog.Error("Module dependency not satisfied", "module", name, "dependency", dep)
				return nil, nil
			}
		}
	}

	// 5. Topological sort
	sorted, err := topologicalSort(selected)
	if err != nil {
		slog.Error("Module dependency cycle detected", "error", err)
		return nil, nil
	}

	// 6. Instantiate in order
	deps := make(ModuleDeps)
	for _, name := range sorted {
		info := selected[name]
		mod := info.Factory(db, server, deps)
		deps[name] = mod
		slog.Info("Module resolved", "module", name)
	}

	return sorted, deps
}

func topologicalSort(mods map[string]ModuleInfo) ([]string, error) {
	indegree := make(map[string]int)
	adjacency := make(map[string][]string)
	for name := range mods {
		indegree[name] = 0
	}
	for name, info := range mods {
		for _, dep := range info.Dependencies {
			adjacency[dep] = append(adjacency[dep], name)
			indegree[name]++
		}
	}

	queue := []string{}
	for name, deg := range indegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}
	sort.Strings(queue)

	result := []string{}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		result = append(result, node)

		for _, adj := range adjacency[node] {
			indegree[adj]--
			if indegree[adj] == 0 {
				queue = append(queue, adj)
				sort.Strings(queue)
			}
		}
	}

	if len(result) != len(mods) {
		return nil, fmt.Errorf("cycle detected in module dependencies")
	}
	return result, nil
}

// TieBreaker takes a slice of tied team numbers and the number of
// matches played as context, and returns a strict ordering of those
// teams.
type TieBreaker func(teamNumbers []int, matchCount int) []int

var tieBreakers = make(map[string]TieBreaker)

// RegisterTieBreaker adds a tie-breaker to the global registry.
// Tie-breakers are called from init() functions in any module.
// Panics if the name is already taken.
func RegisterTieBreaker(name string, tb TieBreaker) {
	if _, dup := tieBreakers[name]; dup {
		panic(fmt.Sprintf("tie-breaker %q already registered", name))
	}
	tieBreakers[name] = tb
}

// GetTieBreaker returns a registered tie-breaker by name.
func GetTieBreaker(name string) (TieBreaker, bool) {
	tb, ok := tieBreakers[name]
	return tb, ok
}
