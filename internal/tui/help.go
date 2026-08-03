package tui

import "github.com/charmbracelet/lipgloss"

var (
	helpTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(clrCyan).
			Padding(0, 1)

	helpKeyStyle = lipgloss.NewStyle().
			Foreground(clrYellow).
			Bold(true)

	helpDescStyle = lipgloss.NewStyle().
			Foreground(clrWhite)
)

type helpEntry struct {
	key  string
	desc string
}

var helpEntries = []helpEntry{
	{"↑/↓ j/k", "Navigate tree"},
	{"g / G", "Jump to top / bottom"},
	{"enter / return / space", "Expand/collapse repo or section"},
	{"tab", "Cycle focus (nav ↔ content)"},
	{"/", "Filter tree (esc clears)"},
	{"o", "Open worktree / session / live agent in a new terminal tab"},
	{"d", "Remove worktree (confirms; force-offer if dirty)"},
	{"D", "Delete branch (confirms; -D offer if unmerged)"},
	{"p", "Prune stale worktrees in repo"},
	{"r", "Refresh now"},
	{"y / n", "Confirm / dismiss destructive action"},
	{"?", "Toggle this help"},
	{"q / ctrl+c", "Quit"},
}

var helpContent string

func init() {
	s := helpTitleStyle.Render("agent-tui — Keyboard Shortcuts") + "\n\n"
	for _, e := range helpEntries {
		s += "  " + helpKeyStyle.Render(e.key)
		s += "  " + helpDescStyle.Render(e.desc) + "\n"
	}
	s += "\n" + dimStyle.Render("Press ? or esc to close")
	helpContent = s
}

func helpView(width, height int) string {
	dialog := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(clrActive).
		Padding(1, 2).
		Render(helpContent)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, dialog)
}
