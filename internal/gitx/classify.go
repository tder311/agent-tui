package gitx

import (
	"sort"
	"strings"

	"github.com/tder311/agent-tui/internal/config"
)

// EffectiveSpawners returns the spawner matchers used for classification:
// user-configured spawners (or the built-in defaults) plus, when the opencode
// orchestrator registry exists, a runtime "opencode" matcher keyed on its
// worktree root — unless the user already defined "opencode".
func EffectiveSpawners(cfg *config.Config, reg *OrchestratorRegistry) map[string]config.Spawner {
	spawners := map[string]config.Spawner{}
	if cfg != nil {
		for k, v := range cfg.Spawners {
			spawners[k] = v
		}
	}
	if len(spawners) == 0 {
		for k, v := range config.Default().Spawners {
			spawners[k] = v
		}
	}
	if reg != nil {
		if root := reg.WorktreeRootExpanded(); root != "" {
			if _, ok := spawners["opencode"]; !ok {
				spawners["opencode"] = config.Spawner{Pattern: root, CLI: "opencode", Color: "green"}
			}
		}
	}
	return spawners
}

// classifySpawner matches a worktree path against spawner patterns (sorted for
// determinism); the first match wins. Returns the spawner name and the CLI to
// launch ("" = plain shell). Main checkouts are never classified.
func classifySpawner(path string, isMain bool, spawners map[string]config.Spawner) (name, tool string) {
	if isMain || path == "" {
		return "", ""
	}
	keys := make([]string, 0, len(spawners))
	for k := range spawners {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sp := spawners[k]
		if sp.Pattern != "" && strings.Contains(path, sp.Pattern) {
			return k, sp.CLI
		}
	}
	return "", ""
}
