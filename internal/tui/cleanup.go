package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tder311/agent-tui/internal/agents"
	"github.com/tder311/agent-tui/internal/cleanup"
)

const cleanupTimeout = 5 * time.Minute

// cleanupModel is the state of the cleanup screen (c). Candidates are scanned
// on open; nothing is removed until the user confirms a selection.
type cleanupModel struct {
	loading    bool
	running    bool
	confirming bool
	items      []cleanup.Candidate
	selected   []bool
	cursor     int
	opts       cleanup.Options
	summary    string
	failures   []string
}

// CleanupScanMsg carries a finished cleanup candidate scan.
type CleanupScanMsg struct {
	Items []cleanup.Candidate
	Opts  cleanup.Options
}

// CleanupDoneMsg carries the outcome of applying a cleanup selection.
type CleanupDoneMsg struct {
	Results []cleanup.Result
}

func cleanupScanCmd(days int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		defer cancel()
		opts := cleanup.DefaultOptions(days, agents.LiveAgents(ctx))
		return CleanupScanMsg{Items: cleanup.Scan(opts), Opts: opts}
	}
}

func cleanupApplyCmd(items []cleanup.Candidate, opts cleanup.Options) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		return CleanupDoneMsg{Results: cleanup.ApplyAll(ctx, items, opts, nil)}
	}
}

func (c *cleanupModel) setItems(items []cleanup.Candidate, opts cleanup.Options) {
	c.loading = false
	c.items = items
	c.opts = opts
	c.selected = make([]bool, len(items))
	for i, it := range items {
		c.selected[i] = it.Default
	}
	if c.cursor >= len(items) {
		c.cursor = max(len(items)-1, 0)
	}
}

func (c *cleanupModel) chosen() []cleanup.Candidate {
	var out []cleanup.Candidate
	for i, it := range c.items {
		if c.selected[i] {
			out = append(out, it)
		}
	}
	return out
}

func (c *cleanupModel) totals() (selN int, selBytes, allBytes int64) {
	for i, it := range c.items {
		allBytes += it.Bytes
		if c.selected[i] {
			selN++
			selBytes += it.Bytes
		}
	}
	return
}

func (m appModel) openCleanup() (tea.Model, tea.Cmd) {
	m.cleanup = &cleanupModel{loading: true}
	return m, cleanupScanCmd(m.cfg.CleanupDaysValue())
}

func (m appModel) handleCleanupKey(key string) (tea.Model, tea.Cmd) {
	c := m.cleanup
	if c.running {
		return m, nil
	}
	if c.confirming {
		switch key {
		case "y":
			c.confirming = false
			c.running = true
			c.summary = ""
			c.failures = nil
			return m, cleanupApplyCmd(c.chosen(), c.opts)
		case "n", "esc":
			c.confirming = false
		}
		return m, nil
	}
	switch key {
	case "esc", "q", "c":
		m.cleanup = nil
		return m, nil
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	}
	if c.loading {
		return m, nil
	}
	switch key {
	case "up", "k":
		if c.cursor > 0 {
			c.cursor--
		}
	case "down", "j":
		if c.cursor < len(c.items)-1 {
			c.cursor++
		}
	case "g", "home":
		c.cursor = 0
	case "G", "end":
		c.cursor = max(len(c.items)-1, 0)
	case " ", "space", "x":
		if c.cursor < len(c.selected) {
			c.selected[c.cursor] = !c.selected[c.cursor]
		}
	case "a":
		all := true
		for _, s := range c.selected {
			all = all && s
		}
		for i := range c.selected {
			c.selected[i] = !all
		}
	case "r":
		c.loading = true
		return m, cleanupScanCmd(m.cfg.CleanupDaysValue())
	case "enter", "return", "ctrl+m", "d":
		if n, _, _ := c.totals(); n > 0 {
			c.confirming = true
		}
	}
	return m, nil
}

func (m appModel) handleCleanupDone(msg CleanupDoneMsg) (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{}
	if !m.scanning {
		m.scanning = true
		cmds = append(cmds, scanCmd(m.cfg))
	}
	if m.cleanup == nil {
		m.setStatus("cleanup: " + cleanup.Summarize(msg.Results))
		return m, tea.Batch(cmds...)
	}
	c := m.cleanup
	c.running = false
	c.summary = cleanup.Summarize(msg.Results)
	c.failures = nil
	for _, r := range msg.Results {
		if r.Err != nil {
			c.failures = append(c.failures, r.Err.Error())
		}
	}
	c.loading = true
	cmds = append(cmds, cleanupScanCmd(m.cfg.CleanupDaysValue()))
	return m, tea.Batch(cmds...)
}

func cleanupView(width, height int, c *cleanupModel, days int) string {
	innerW := max(width-8, 40)
	var b strings.Builder
	b.WriteString(helpTitleStyle.Render("Cleanup — stale agents & local data") + "\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("  inactive ≥ %dd (cleanup_days) · busy agents and memory dirs are never touched", days)) + "\n\n")

	if c.summary != "" {
		b.WriteString("  " + statusGreen.Render("✓ "+c.summary) + "\n")
		for _, f := range c.failures {
			b.WriteString("  " + errorStyle.Render(truncate("✗ "+f, innerW-4)) + "\n")
		}
		b.WriteString("\n")
	}

	listH := max(height-14-len(c.failures), 3)
	switch {
	case c.running:
		b.WriteString("  " + statusCyan.Render("cleaning up…") + "\n")
	case c.loading && len(c.items) == 0:
		b.WriteString("  " + statusCyan.Render("scanning ~/.claude…") + "\n")
	case len(c.items) == 0:
		b.WriteString("  " + statusGreen.Render("Nothing to clean up.") + "\n")
	default:
		b.WriteString(cleanupList(c, innerW, listH))
	}

	selN, selBytes, allBytes := c.totals()
	b.WriteString("\n")
	if c.confirming {
		b.WriteString("  " + statusYellow.Render(fmt.Sprintf("⚠  Delete %d item(s), freeing %s? This cannot be undone.", selN, cleanup.HumanBytes(selBytes))) + "\n")
		b.WriteString("  " + statusGreen.Render("y") + dimStyle.Render("  yes  ") + statusRed.Render("n") + dimStyle.Render("  no"))
	} else {
		b.WriteString("  " + fmt.Sprintf("%d of %d selected · %s of %s", selN, len(c.items), cleanup.HumanBytes(selBytes), cleanup.HumanBytes(allBytes)) + "\n")
		b.WriteString("  " + dimStyle.Render("[space] toggle [a] all/none [enter] clean selected [r] rescan [esc] close"))
	}

	dialog := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(clrActive).
		Padding(1, 2).
		Width(innerW + 4).
		Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, dialog)
}

// cleanupList renders candidates grouped by kind, scrolled so the cursor
// stays within h lines.
func cleanupList(c *cleanupModel, w, h int) string {
	var lines []string
	cursorLine := 0
	var last cleanup.Kind = -1
	for i, it := range c.items {
		if it.Kind != last {
			last = it.Kind
			lines = append(lines, "  "+statusOrange.Render(it.Kind.String())+dimStyle.Render("  — "+it.Kind.Action()))
		}
		box := dimStyle.Render("[ ]")
		if c.selected[i] {
			box = statusGreen.Render("[x]")
		}
		meta := relTime(it.Updated)
		if it.State != "" {
			meta = it.State + " · " + meta
		}
		title := it.Title
		if title == "" {
			title = it.ID
		}
		idW := 9
		if it.Kind == cleanup.KindProjectStub {
			idW = 0
		}
		size := fmt.Sprintf("%9s", cleanup.HumanBytes(it.Bytes))
		titleW := max(w-4-4-10-idW-len(meta)-3, 10)
		row := fmt.Sprintf("%s %s  ", box, size)
		if idW > 0 {
			row += statusCyan.Render(fmt.Sprintf("%-8s", it.ID)) + " "
		}
		row += truncate(title, titleW) + "  " + dimStyle.Render(meta)
		prefix := "   "
		if i == c.cursor {
			prefix = " " + statusCyan.Render("›") + " "
			cursorLine = len(lines)
		}
		lines = append(lines, prefix+row)
	}
	start := 0
	if len(lines) > h {
		start = min(max(cursorLine-h/2, 0), len(lines)-h)
	}
	end := min(start+h, len(lines))
	return strings.Join(lines[start:end], "\n") + "\n"
}
