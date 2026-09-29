package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tder311/agent-tui/internal/agents"
	"github.com/tder311/agent-tui/internal/cleanup"
	"github.com/tder311/agent-tui/internal/config"
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
  agent-tui clean      List reclaimable agent data (dry run)
                         --apply   remove the preselected candidates
                         --all     with --apply, remove every candidate
                         --days N  inactivity threshold (default: cleanup_days)
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
	case "clean":
		os.Exit(runClean(os.Args[2:]))
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

func runClean(args []string) int {
	fs := flag.NewFlagSet("clean", flag.ExitOnError)
	apply := fs.Bool("apply", false, "remove the preselected candidates")
	all := fs.Bool("all", false, "with --apply, remove every candidate")
	cfg, err := config.LoadOrCreate(config.DefaultPath())
	if err != nil {
		cfg = config.Default()
	}
	days := fs.Int("days", cfg.CleanupDaysValue(), "inactivity threshold in days")
	_ = fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	opts := cleanup.DefaultOptions(*days, agents.LiveAgents(ctx))
	items := cleanup.Scan(opts)
	if len(items) == 0 {
		fmt.Println("Nothing to clean up.")
		return 0
	}

	var chosen []cleanup.Candidate
	var total int64
	const maxPerKind = 8
	last := cleanup.Kind(-1)
	shown, hidden := 0, 0
	flushHidden := func() {
		if hidden > 0 {
			fmt.Printf("  … and %d more\n", hidden)
		}
	}
	for _, it := range items {
		if it.Kind != last {
			flushHidden()
			last, shown, hidden = it.Kind, 0, 0
			fmt.Printf("\n%s — %s\n", it.Kind, it.Kind.Action())
		}
		mark := "   "
		if it.Default || *all {
			mark = "[x]"
			chosen = append(chosen, it)
			total += it.Bytes
		}
		if shown >= maxPerKind {
			hidden++
			continue
		}
		shown++
		title := it.Title
		if it.State != "" {
			title += " (" + it.State + ")"
		}
		fmt.Printf("  %s %9s  %-8s  %s  %s\n", mark, cleanup.HumanBytes(it.Bytes), it.ID, it.Updated.Format("2006-01-02"), title)
	}
	flushHidden()
	fmt.Printf("\n%d candidate(s); %d marked [x], %s\n", len(items), len(chosen), cleanup.HumanBytes(total))
	if !*apply {
		fmt.Println("Dry run. Re-run with --apply to remove the [x] items (add --all for every item), or press c in the TUI to pick.")
		return 0
	}

	results := cleanup.ApplyAll(ctx, chosen, opts, nil)
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "failed: %v\n", r.Err)
		}
	}
	fmt.Println(cleanup.Summarize(results))
	if failed > 0 {
		return 1
	}
	return 0
}
