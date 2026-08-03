package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tder311/agent-tui/internal/tui"
)

var (
	version = "devel"
	commit  = "none"
	date    = "unknown"
)

const usage = `agent-tui — terminal UI for local AI coding agents, git worktrees, and branches

Usage:
  agent-tui            Launch the TUI
  agent-tui run        Launch the TUI
  agent-tui version    Print version
  agent-tui help       Show this help

Data sources:
  <scan roots> (default: ~)                  app-agnostic sweep for .git markers:
  ~/repos, ~/conductor/repos, ~/worktrees    finds any app's worktrees (configurable)
  origin URLs                                 clones of the same repo collapse into
                                              one project; no-remote repos stay local
  ~/.claude/projects/*                       Claude Code session files
  claude agents --json                       live Claude agents (busy/idle/blocked),
                                             attributed to worktrees; skips if absent
  ~/.local/share/opencode/opencode.db        OpenCode sessions (read-only)
  ~/.config/opencode/orchestrator/repos.json adds the opencode worktree spawner

Config:
  ~/.config/agent-tui/config.json (created with defaults on first run)
  scan_roots, skip, spawners — see README
`

func main() {
	if len(os.Args) < 2 {
		runTUI()
		return
	}

	switch os.Args[1] {
	case "run":
		runTUI()
	case "version", "--version", "-v":
		fmt.Printf("agent-tui %s (commit %s, built %s)\n", version, commit, date)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", os.Args[1], usage)
		os.Exit(1)
	}
}

func runTUI() {
	p := tea.NewProgram(tui.NewApp(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
