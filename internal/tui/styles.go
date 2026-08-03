package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/tder311/agent-tui/internal/agents"
	"github.com/tder311/agent-tui/internal/config"
	"github.com/tder311/agent-tui/internal/gitx"
)

var (
	clrWhite  = lipgloss.Color("#ffffff")
	clrGreen  = lipgloss.Color("#00ff00")
	clrRed    = lipgloss.Color("#ff0000")
	clrYellow = lipgloss.Color("#ffff00")
	clrGray   = lipgloss.Color("#888888")
	clrCyan   = lipgloss.Color("#00ffff")
	clrOrange = lipgloss.Color("#ff8800")
	clrDim    = lipgloss.Color("#555555")
	clrPurple = lipgloss.Color("#c792ea")
	clrHeader = lipgloss.Color("#16213e")
	clrBorder = lipgloss.Color("#3a3a5c")
	clrActive = lipgloss.Color("#00aaff")
	clrCursor = lipgloss.Color("#2a2a4a")
)

var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(clrWhite).
			Background(clrHeader).
			Padding(0, 1)

	statusGreen = lipgloss.NewStyle().
			Foreground(clrGreen).
			Bold(true)

	statusRed = lipgloss.NewStyle().
			Foreground(clrRed).
			Bold(true)

	statusYellow = lipgloss.NewStyle().
			Foreground(clrYellow).
			Bold(true)

	statusGray = lipgloss.NewStyle().
			Foreground(clrGray)

	statusOrange = lipgloss.NewStyle().
			Foreground(clrOrange).
			Bold(true)

	statusCyan = lipgloss.NewStyle().
			Foreground(clrCyan)

	focusedBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(clrActive)

	unfocusedBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(clrBorder)

	contentStyle = lipgloss.NewStyle().
			Padding(0, 1)

	dimStyle = lipgloss.NewStyle().
			Foreground(clrDim)

	errorStyle = lipgloss.NewStyle().
			Foreground(clrRed).
			Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(clrDim).
			Padding(0, 1)

	tableHeaderStyle = lipgloss.NewStyle().
				Foreground(clrGray).
				Bold(true)

	// wtTag marks worktree rows so they read as worktrees, not branches.
	wtTag = lipgloss.NewStyle().
		Foreground(clrDim).
		Bold(true)

	// localTag flags repositories with no real remote origin.
	localTag = lipgloss.NewStyle().
			Foreground(clrGray)

	// gearStyle marks a historical session launched by a background agent
	// (only shown when include_agent_sessions is on).
	gearStyle = lipgloss.NewStyle().
			Foreground(clrPurple).
			Bold(true)
)

// worktreeLabel returns the classification badge for a worktree: "main" for
// the primary checkout, the matched spawner name otherwise, "other" as the
// neutral fallback for worktrees no known app spawned.
func worktreeLabel(wt gitx.Worktree) string {
	if wt.Main {
		return "main"
	}
	if wt.Spawner != "" {
		return wt.Spawner
	}
	return "other"
}

// spawnerColor returns the configured color for a spawner, "" when none.
func spawnerColor(cfg *config.Config, name string) string {
	if cfg != nil {
		if sp, ok := cfg.Spawners[name]; ok {
			return sp.Color
		}
	}
	return ""
}

// spawnerStyle colors a worktree badge. An explicit config color wins;
// otherwise well-known spawners get a default color and unknown ones are gray.
func spawnerStyle(name, color string) lipgloss.Style {
	switch strings.ToLower(color) {
	case "cyan":
		return statusCyan
	case "green":
		return statusGreen
	case "orange":
		return statusOrange
	case "purple":
		return lipgloss.NewStyle().Foreground(clrPurple).Bold(true)
	case "red":
		return statusRed
	case "yellow":
		return statusYellow
	case "gray", "grey":
		return statusGray
	}
	switch name {
	case "claude":
		return statusOrange
	case "conductor":
		return lipgloss.NewStyle().Foreground(clrPurple).Bold(true)
	case "opencode":
		return statusGreen
	default:
		return statusGray
	}
}

// liveDot colors the status dot for a running agent: busy/working = green,
// idle = gray, blocked = yellow, anything else dim. Status is preferred over
// state (the CLI sometimes reports a stale "done" state alongside "busy").
func liveDot(a agents.Agent) string {
	s := a.Status
	if s == "" {
		s = a.State
	}
	switch s {
	case "busy", "working", "running", "active":
		return statusGreen.Render("●")
	case "idle", "waiting", "paused":
		return statusGray.Render("●")
	case "blocked", "error", "failed":
		return statusYellow.Render("●")
	default:
		return dimStyle.Render("●")
	}
}

// liveKindTag renders the interactive/background marker for a live agent.
func liveKindTag(a agents.Agent) string {
	if a.Kind == "interactive" {
		return statusCyan.Render("fg")
	}
	return dimStyle.Render("bg")
}

// liveBadge is the compact nav badge for a running agent: a status dot plus
// the interactive/background tag.
func liveBadge(a agents.Agent) string {
	return liveDot(a) + " " + liveKindTag(a)
}

// liveStatusWord renders the full status word for detail panes.
func liveStatusWord(a agents.Agent) string {
	s := a.Status
	if s == "" {
		s = a.State
	}
	switch s {
	case "busy", "working", "running", "active":
		return statusGreen.Render(s)
	case "blocked", "error", "failed":
		return statusYellow.Render(s)
	default:
		return statusGray.Render(s)
	}
}

func dirtyMark(dirty bool) string {
	if dirty {
		return statusYellow.Render("✱ dirty")
	}
	return statusGreen.Render("✓ clean")
}

// truncate shortens s to fit width w (in cells), adding an ellipsis.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	runes := []rune(s)
	out := ""
	for _, r := range runes {
		if lipgloss.Width(out+string(r))+1 > w {
			break
		}
		out += string(r)
	}
	return out + "…"
}
