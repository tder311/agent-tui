package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestAgentSessionBadge asserts that a background-agent session (AgentName
// set) carries the ⚙ gear badge in the nav and a full title + agent row in
// its detail pane. Interactive sessions never show the gear.
func TestAgentSessionBadge(t *testing.T) {
	data, sessions, live, prsByID := fakeData()
	m := appModel{
		width: 120, height: 40, ready: true,
		nav: newNavTreeModel(navWidth), data: data, sessions: sessions, live: live, prs: prsByID,
	}
	m.layout()
	m.rebuildNav()
	m.nav.collapsed = map[string]bool{} // expand all
	m.rebuildNav()

	// Select the agent session row (kind navKindSession, title = agent name).
	gearSessions, plainSessions := 0, 0
	for i, e := range m.nav.entries {
		if e.kind != navKindSession {
			continue
		}
		if e.label == "wind profile reshuffling gdm" {
			gearSessions++
			m.nav.cursor = i
		} else {
			plainSessions++
		}
	}
	if gearSessions != 1 {
		t.Fatalf("expected 1 agent session row, got %d", gearSessions)
	}
	if plainSessions != 2 {
		t.Errorf("expected 2 plain session rows, got %d", plainSessions)
	}

	// Selected agent session's detail: agent row + full title.
	m.refreshDetail(false)
	dv := m.vp.View()
	for _, want := range []string{"Agent", "wind profile reshuffling gdm", "o open/resume"} {
		if !strings.Contains(dv, want) {
			t.Errorf("agent session detail missing %q:\n%s", want, dv)
		}
	}

	// Nav renders the gear only for the agent session row. Plain session
	// detail panes must not mention an agent.
	for i, e := range m.nav.entries {
		if e.kind == navKindSession && e.label != "wind profile reshuffling gdm" {
			m.nav.cursor = i
			m.refreshDetail(false)
			if strings.Contains(m.vp.View(), "⚙") || strings.Contains(m.vp.View(), "Agent") {
				t.Errorf("plain session detail shows agent chrome:\n%s", m.vp.View())
			}
		}
	}
	navView := m.nav.View()
	if n := strings.Count(navView, "⚙"); n != 1 {
		t.Errorf("nav should render exactly 1 gear glyph, got %d\n%s", n, navView)
	}
}

func TestRenderScenarios(t *testing.T) {
	data, sessions, live, prsByID := fakeData()
	m := NewApp().(appModel)

	// Window size + data loaded.
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 32})
	m = tm.(appModel)
	tm, _ = m.Update(ScanResultMsg{Data: data, Sessions: sessions, Live: live, PRs: prsByID, At: time.Now()})
	m = tm.(appModel)

	v := m.View()
	for _, want := range []string{"agent-tui", "alpha", "beta", "NAV"} {
		if !strings.Contains(v, want) {
			t.Errorf("main view missing %q\n%s", want, v)
		}
	}
	if testing.Verbose() {
		t.Logf("\n--- main (repos collapsed) ---\n%s", v)
	}

	// Expand alpha via enter.
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(appModel)
	v = m.View()
	for _, want := range []string{"Worktrees", "Branches", "Agents (live)", "Agents"} {
		if !strings.Contains(v, want) {
			t.Errorf("expanded view missing %q\n%s", want, v)
		}
	}
	if !strings.Contains(v, "wind profile reshuffling gdm") {
		t.Errorf("expanded view missing live agent name\n%s", v)
	}
	if testing.Verbose() {
		t.Logf("\n--- alpha expanded ---\n%s", v)
	}

	// Help modal.
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = tm.(appModel)
	v = m.View()
	if !strings.Contains(v, "Keyboard Shortcuts") {
		t.Errorf("help view missing title\n%s", v)
	}
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = tm.(appModel)

	// Filter flow.
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = tm.(appModel)
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("feat")})
	m = tm.(appModel)
	v = m.View()
	if !strings.Contains(v, "feat/x") {
		t.Errorf("filtered view missing feat/x\n%s", v)
	}
	if testing.Verbose() {
		t.Logf("\n--- filter=feat ---\n%s", v)
	}
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = tm.(appModel)
}
