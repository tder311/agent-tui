package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScanRootsDefaults(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}

	// Fresh config: whole home is the default sweep root.
	c := Default()
	want := []string{home}
	if got := c.ScanRootsExpanded(); !reflect.DeepEqual(got, want) {
		t.Errorf("defaults = %v, want %v", got, want)
	}
}

func TestScanRootsBackwardCompat(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}

	// Legacy config with only scan_root: merged into the default whole-home
	// sweep (deduped), so existing repos keep being found.
	legacy := &Config{ScanRoot: "~/repos"}
	want := []string{
		home,
		filepath.Join(home, "repos"),
	}
	if got := legacy.ScanRootsExpanded(); !reflect.DeepEqual(got, want) {
		t.Errorf("legacy = %v, want %v", got, want)
	}

	// Legacy repo_roots: appended, deduped.
	legacy2 := &Config{RepoRoots: []string{"~/repos", "~/repos", "~/conductor/repos"}}
	want = []string{
		home,
		filepath.Join(home, "repos"),
		filepath.Join(home, "conductor", "repos"),
	}
	if got := legacy2.ScanRootsExpanded(); !reflect.DeepEqual(got, want) {
		t.Errorf("legacy repo_roots = %v, want %v", got, want)
	}

	// Explicit scan_roots wins; duplicates deduped.
	explicit := &Config{ScanRoots: []string{"~/repos", "~/repos", "~/other"}}
	want = []string{
		filepath.Join(home, "repos"),
		filepath.Join(home, "other"),
	}
	if got := explicit.ScanRootsExpanded(); !reflect.DeepEqual(got, want) {
		t.Errorf("explicit = %v, want %v", got, want)
	}
}

func TestSkipDefaults(t *testing.T) {
	c := Default()
	want := []string{".codex", ".nvm", ".cache", ".Trash", "node_modules", ".git"}
	for _, w := range want {
		found := false
		for _, s := range c.Skip {
			if s == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("default skip missing %q (skip=%v)", w, c.Skip)
		}
	}
}

func TestDefaultsApplied(t *testing.T) {
	cfg := &Config{}
	cfg.applyDefaults()
	if len(cfg.ScanRoots) != 1 || cfg.ScanRoots[0] != "~" {
		t.Errorf("scan_roots default missing: %v", cfg.ScanRoots)
	}
	if len(cfg.Skip) == 0 {
		t.Errorf("skip defaults missing")
	}
	if len(cfg.Spawners) == 0 {
		t.Errorf("spawner defaults missing")
	}
	if sp, ok := cfg.Spawners["claude"]; !ok || sp.CLI != "claude" || !strings.Contains(sp.Pattern, ".claude") {
		t.Errorf("claude spawner default wrong: %+v", sp)
	}
	if sp, ok := cfg.Spawners["conductor"]; !ok || sp.CLI != "claude" {
		t.Errorf("conductor spawner default wrong: %+v", sp)
	}
	if cfg.IncludeAgentSessions {
		t.Errorf("include_agent_sessions should default false")
	}
	if got := cfg.SessionDaysValue(); got != 30 {
		t.Errorf("session_days default = %d, want 30", got)
	}
	if got := cfg.SessionCapValue(); got != 50 {
		t.Errorf("session_cap default = %d, want 50", got)
	}
	if got := cfg.PRSAuthorValue(); got != "@me" {
		t.Errorf("prs_author default = %q, want @me", got)
	}
	// Explicit 0 must stay 0 (unlimited), not be overwritten by defaults.
	zero := 0
	cfg3 := &Config{SessionDays: &zero, SessionCap: &zero}
	cfg3.applyDefaults()
	if got := cfg3.SessionDaysValue(); got != 0 {
		t.Errorf("explicit session_days=0 should stay 0, got %d", got)
	}
	if got := cfg3.SessionCapValue(); got != 0 {
		t.Errorf("explicit session_cap=0 should stay 0, got %d", got)
	}
	// Explicit prs_author is preserved; "" means all PRs.
	me := "@me"
	all := ""
	cfg4 := &Config{PRSAuthor: &me}
	cfg4.applyDefaults()
	if got := cfg4.PRSAuthorValue(); got != "@me" {
		t.Errorf("prs_author @me should be preserved, got %q", got)
	}
	cfg5 := &Config{PRSAuthor: &all}
	cfg5.applyDefaults()
	if got := cfg5.PRSAuthorValue(); got != "" {
		t.Errorf("prs_author \"\" (all) should be preserved, got %q", got)
	}
	// Custom spawners are preserved, not replaced by defaults.
	cfg2 := &Config{Spawners: map[string]Spawner{"myapp": {Pattern: "/apps/myapp/", CLI: "myapp", Color: "green"}}}
	cfg2.applyDefaults()
	if len(cfg2.Spawners) != 1 {
		t.Errorf("custom spawners should not be replaced: %+v", cfg2.Spawners)
	}
}
