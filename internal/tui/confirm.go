package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var confirmBox = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(clrYellow).
	Padding(1, 3).
	Width(56)

func confirmView(width, height int, req *actionRequest) string {
	var b strings.Builder
	b.WriteString(statusYellow.Render("⚠  Confirm Action"))
	b.WriteString("\n\n")
	b.WriteString("Are you sure you want to\n" + req.Label + "?\n")
	if req.WtPath != "" {
		b.WriteString(dimStyle.Render("Worktree: "+shortPath(req.WtPath)) + "\n")
	}
	if req.Branch != "" {
		b.WriteString(dimStyle.Render("Branch: "+req.Branch) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(statusGreen.Render("y") + dimStyle.Render("  yes  "))
	b.WriteString(statusRed.Render("n") + dimStyle.Render("  no   "))
	b.WriteString(dimStyle.Render("  esc  cancel"))

	dialog := confirmBox.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, dialog)
}
