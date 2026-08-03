package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TerminalApp identifies which macOS terminal to open sessions in.
type TerminalApp string

const (
	TerminalAuto     TerminalApp = "auto"
	TerminalTerminal TerminalApp = "terminal" // Terminal.app
	TerminalITerm    TerminalApp = "iterm2"   // iTerm2
)

// Spawner describes how to recognize and open worktrees created by a specific
// app. Pattern is a path substring: worktrees whose path contains it get the
// spawner's label/color, and the `o` action launches CLI ("" = plain shell).
// Everything here is heuristic + cosmetic; unknown worktrees are "other" and
// still fully usable.
type Spawner struct {
	Pattern string `json:"pattern"`
	CLI     string `json:"cli,omitempty"`
	Color   string `json:"color,omitempty"`
}

type Config struct {
	// Terminal is the terminal app used by the `o` (open) action:
	// "auto" (iTerm2 if installed, else Terminal.app), "terminal", or "iterm2".
	Terminal TerminalApp `json:"terminal"`
	// ScanRoots are the directories swept for .git markers (default ["~"]).
	// Discovery is app-agnostic: every worktree anywhere under these roots is
	// found regardless of which tool created it.
	ScanRoots []string `json:"scan_roots,omitempty"`
	// Skip lists directory patterns not swept. A pattern containing a path
	// separator matches that absolute path (subtree excluded once the dir
	// itself matches); a bare name matches any directory of that name.
	Skip []string `json:"skip,omitempty"`
	// Spawners maps an app name to a path-pattern matcher used to label and
	// color its worktrees and pick which CLI `o` launches. Built-in defaults
	// cover claude + conductor; the opencode orchestrator worktree root is
	// matched automatically. Users can add apps without code changes.
	Spawners map[string]Spawner `json:"spawners,omitempty"`
	// RefreshSeconds is the auto-refresh interval (default 10, min 2).
	RefreshSeconds int `json:"refresh_seconds"`
	// IncludeAgentSessions lists sessions launched by background agents
	// (detected via the Claude jobs registry: ~/.claude/jobs/<id>/state.json
	// with nameSource "auto") in the Agents section. Default false keeps the
	// historical list focused on interactive work. Included agent sessions
	// get a ⚙ badge so they stay recognizable.
	IncludeAgentSessions bool `json:"include_agent_sessions"`
	// SessionDays is the recency window for historical sessions: only
	// sessions updated within the last SessionDays days are shown. 0 =
	// unlimited. Pointer so an explicit 0 in the config is honored over the
	// default.
	SessionDays *int `json:"session_days,omitempty"`
	// SessionCap caps historical sessions per clone, most recent first.
	// 0 = unlimited.
	SessionCap *int `json:"session_cap,omitempty"`
	// PRSAuthor filters open pull requests to those authored by the given
	// GitHub login. Default "@me" (the authenticated gh user) shows only
	// your own PRs; a specific login shows another author; "" shows all.
	PRSAuthor *string `json:"prs_author,omitempty"`
	// ScanRoot is the legacy single scan root, and RepoRoots the legacy
	// repo list; both are merged into scan_roots when present. New configs
	// omit them.
	ScanRoot  string   `json:"scan_root,omitempty"`
	RepoRoots []string `json:"repo_roots,omitempty"`
}

func Default() *Config {
	return &Config{
		Terminal:  TerminalAuto,
		ScanRoots: []string{"~"},
		Skip: []string{
			"~/Library", "~/.Trash", "~/.cache", "~/.npm", "~/.yarn",
			"~/.claude", "~/Downloads", "~/Music", "~/Movies", "~/Pictures",
			"node_modules", ".git", "__pycache__", ".venv", "venv", ".tox",
			".pytest_cache", ".gradle", ".m2", ".cargo", ".rustup", ".pkg",
			"target", ".next", ".dart_tool",
			// Toolchains and app caches that never hold repos worth browsing:
			// .codex also hides the transient .codex/.tmp/* backup noise.
			".codex", ".nvm", ".cache", ".Trash",
		},
		Spawners: map[string]Spawner{
			"claude":    {Pattern: "/.claude/worktrees/", CLI: "claude", Color: "orange"},
			"conductor": {Pattern: "/conductor/workspaces/", CLI: "claude", Color: "purple"},
		},
		RefreshSeconds: 10,
		SessionDays:    intPtr(30),
		SessionCap:     intPtr(50),
		PRSAuthor:      strPtr("@me"),
	}
}

// SessionDaysValue returns the recency window for historical sessions,
// defaulting to 30 when unset. 0 means unlimited.
func (c *Config) SessionDaysValue() int {
	if c == nil || c.SessionDays == nil {
		return 30
	}
	return *c.SessionDays
}

// SessionCapValue returns the per-clone session cap, defaulting to 50 when
// unset. 0 means unlimited.
func (c *Config) SessionCapValue() int {
	if c == nil || c.SessionCap == nil {
		return 50
	}
	return *c.SessionCap
}

// PRSAuthorValue returns the PR author filter, defaulting to "@me" (the
// authenticated gh user) when unset. "" shows all PRs.
func (c *Config) PRSAuthorValue() string {
	if c == nil || c.PRSAuthor == nil {
		return "@me"
	}
	return *c.PRSAuthor
}

func DefaultPath() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "agent-tui", "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "agent-tui", "config.json")
}

// LoadOrCreate loads the config at path, creating it with defaults on first run.
func LoadOrCreate(path string) (*Config, error) {
	cfg, err := Load(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			// Corrupt config: fall back to defaults rather than failing hard.
			return Default(), nil
		}
		cfg = Default()
		if serr := Save(path, cfg); serr != nil {
			return cfg, fmt.Errorf("config: create default: %w", serr)
		}
		return cfg, nil
	}
	cfg.applyDefaults()
	return cfg, nil
}

func (c *Config) applyDefaults() {
	d := Default()
	if c.Terminal == "" {
		c.Terminal = d.Terminal
	}
	if c.RefreshSeconds < 2 {
		c.RefreshSeconds = d.RefreshSeconds
	}
	if c.SessionDays == nil {
		c.SessionDays = intPtr(*d.SessionDays)
	}
	if c.SessionCap == nil {
		c.SessionCap = intPtr(*d.SessionCap)
	}
	if c.PRSAuthor == nil {
		c.PRSAuthor = strPtr(*d.PRSAuthor)
	}
	if len(c.ScanRoots) == 0 {
		c.ScanRoots = d.ScanRoots
	}
	if len(c.Skip) == 0 {
		c.Skip = d.Skip
	}
	if len(c.Spawners) == 0 {
		c.Spawners = cloneSpawners(d.Spawners)
	}
}

func intPtr(v int) *int { return &v }

func strPtr(v string) *string { return &v }

func cloneSpawners(in map[string]Spawner) map[string]Spawner {
	out := make(map[string]Spawner, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ScanRootsExpanded returns the effective sweep roots with ~ expanded.
// Legacy repo_roots / scan_root entries are merged in (deduped) so older
// configs keep their repos even though the default already covers them.
func (c *Config) ScanRootsExpanded() []string {
	roots := c.ScanRoots
	if len(roots) == 0 {
		roots = Default().ScanRoots
	}
	if c.ScanRoot != "" {
		roots = append(roots, c.ScanRoot)
	}
	roots = append(roots, c.RepoRoots...)
	seen := make(map[string]bool, len(roots))
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		r = ExpandPath(r)
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// ExpandPath expands a leading ~ or ~/ to the user's home directory.
func ExpandPath(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}



func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: decode: %w", err)
	}
	return &cfg, nil
}

func Save(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("config: mkdir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "config*.tmp")
	if err != nil {
		return fmt.Errorf("config: tmp: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("config: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("config: close: %w", err)
	}
	if err := os.Chmod(tmpName, 0600); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("config: chmod: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("config: rename: %w", err)
	}
	return nil
}
