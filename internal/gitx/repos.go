package gitx

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/tder311/agent-tui/internal/config"
)

// OrchestratorRegistry mirrors ~/.config/opencode/orchestrator/repos.json.
// Discovery no longer depends on it — the sweep finds every worktree on disk.
// It is loaded only to classify worktrees under the opencode worktree root.
type OrchestratorRegistry struct {
	Repos map[string]struct {
		Path string `json:"path"`
	} `json:"repos"`
	WorktreeRoot string `json:"worktreeRoot"`
}

// LoadOrchestratorRegistry reads the opencode orchestrator registry.
// Returns nil (no error) when the file does not exist.
func LoadOrchestratorRegistry() (*OrchestratorRegistry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}
	path := filepath.Join(home, ".config", "opencode", "orchestrator", "repos.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var reg OrchestratorRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, err
	}
	return &reg, nil
}

// WorktreeRootExpanded returns the orchestrator worktree root with ~ expanded,
// or "" when unknown.
func (r *OrchestratorRegistry) WorktreeRootExpanded() string {
	if r == nil || r.WorktreeRoot == "" {
		return ""
	}
	return config.ExpandPath(r.WorktreeRoot)
}
