package agents

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tder311/agent-tui/internal/config"
	"github.com/tder311/agent-tui/internal/gitx"
)

// TestDiscoverSmoke runs the full pipeline against the real machine:
// ScanAll → AllDirs → Discover, and logs per-repo session counts.
func TestDiscoverSmoke(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	root := filepath.Join(home, "repos")
	if _, err := os.Stat(root); err != nil {
		t.Skip("~/repos not present")
	}

	cfg := config.Default()
	cfg.RepoRoots = nil
	for _, r := range []string{"~/repos", "~/conductor/repos"} {
		p := filepath.Join(home, r[2:])
		if _, err := os.Stat(p); err == nil {
			cfg.RepoRoots = append(cfg.RepoRoots, r)
		}
	}
	if len(cfg.RepoRoots) == 0 {
		t.Skip("no scan roots present")
	}
	data, _ := gitx.ScanAll(context.Background(), cfg)
	byRepo := Discover(gitx.AllDirs(data))

	total := 0
	for repo, ss := range byRepo {
		total += len(ss)
		claude, oc := 0, 0
		for _, s := range ss {
			if s.Tool == "claude" {
				claude++
			} else {
				oc++
			}
		}
		t.Logf("%s: %d claude, %d opencode", repo, claude, oc)
	}
	t.Logf("total matched sessions: %d", total)
}
