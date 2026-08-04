package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tder311/agent-tui/internal/agents"
	"github.com/tder311/agent-tui/internal/config"
	"github.com/tder311/agent-tui/internal/gitx"
	"github.com/tder311/agent-tui/internal/prs"
)

const navWidth = 36

type appModel struct {
	width    int
	height   int
	ready    bool
	quitting bool

	cfg        *config.Config
	configPath string

	nav       navTreeModel
	vp        viewport.Model
	filter    textinput.Model
	filtering bool

	data     []gitx.RepoData
	sessions map[string][]agents.Session // historical, keyed by repo path
	live     map[string][]agents.Agent   // running agents, keyed by repo path
	prs      map[string][]prs.PR         // open PRs, keyed by project identity

	scanning bool
	lastScan time.Time

	focusIndex int // 0 = nav, 1 = content
	showHelp   bool
	confirming *actionRequest

	statusMsg string
	statusAt  time.Time
	err       error
	errSince  time.Time
}

// NewApp builds the root model, loading (or creating) the config.
func NewApp() tea.Model {
	cfgPath := config.DefaultPath()
	cfg, err := config.LoadOrCreate(cfgPath)
	if err != nil {
		cfg = config.Default()
	}

	ti := textinput.New()
	ti.Placeholder = "filter…"
	ti.Prompt = "/"

	return appModel{
		cfg:        cfg,
		configPath: cfgPath,
		nav:        newNavTreeModel(navWidth),
		filter:     ti,
		sessions:   make(map[string][]agents.Session),
		live:       make(map[string][]agents.Agent),
		prs:        make(map[string][]prs.PR),
	}
}

func (m appModel) Init() tea.Cmd {
	m.nav.loading = true
	return tea.Batch(
		scanCmd(m.cfg),
		tickCmd(m.refreshInterval()),
	)
}

func (m appModel) refreshInterval() time.Duration {
	return time.Duration(m.cfg.RefreshSeconds) * time.Second
}

func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.layout()
		return m, nil

	case tea.KeyMsg:
		if m.quitting {
			return m, tea.Quit
		}
	}

	return m.updateMain(msg)
}

func (m *appModel) layout() {
	m.nav.width = navWidth
	// nav.View wraps content in a border (+2 lines): outer = m.height - 2,
	// matching the content box so header+body+footer fill the screen.
	m.nav.height = m.height - 4
	if m.nav.height < 3 {
		m.nav.height = 3
	}
	contentW := m.width - navWidth - 6
	if contentW < 20 {
		contentW = 20
	}
	m.vp.Width = contentW
	m.vp.Height = m.height - 6
	if m.vp.Height < 3 {
		m.vp.Height = 3
	}
	m.refreshDetail(false)
}

func (m appModel) updateMain(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Expire transient messages.
	if m.err != nil && time.Since(m.errSince) > 12*time.Second {
		m.err = nil
	}
	if m.statusMsg != "" && time.Since(m.statusAt) > 8*time.Second {
		m.statusMsg = ""
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case TickMsg:
		if m.scanning {
			return m, tickCmd(m.refreshInterval())
		}
		m.scanning = true
		return m, tea.Batch(scanCmd(m.cfg), tickCmd(m.refreshInterval()))

	case ScanResultMsg:
		m.scanning = false
		m.lastScan = msg.At
		m.data = msg.Data
		m.sessions = msg.Sessions
		m.live = msg.Live
		m.prs = msg.PRs
		m.nav.loading = false
		m.rebuildNav()
		return m, nil

	case ActionResultMsg:
		m.confirming = nil
		if msg.NeedsForce {
			// Second confirm: offer the force variant.
			switch msg.Kind {
			case actionRemoveWorktree:
				m.confirming = &actionRequest{
					Kind:     actionForceRemoveWorktree,
					Label:    "FORCE remove the DIRTY worktree (uncommitted changes will be lost)",
					RepoPath: msg.RepoPath,
					WtPath:   msg.WtPath,
				}
			case actionDeleteBranch:
				m.confirming = &actionRequest{
					Kind:     actionForceDeleteBranch,
					Label:    "FORCE delete the UNMERGED branch " + msg.Branch,
					RepoPath: msg.RepoPath,
					Branch:   msg.Branch,
				}
			}
			return m, nil
		}
		if msg.Err != nil {
			m.setErr(msg.Err)
		} else {
			m.setStatus(msg.Label + " — done")
		}
		if !m.scanning {
			m.scanning = true
			return m, scanCmd(m.cfg)
		}
		return m, nil

	case OpenResultMsg:
		if msg.Err != nil {
			if msg.Kind == openBrowser {
				m.setErr(fmt.Errorf("open browser: %w", msg.Err))
			} else {
				m.setErr(fmt.Errorf("open terminal: %w", msg.Err))
			}
		} else if msg.Kind == openBrowser {
			m.setStatus("opened in browser")
		} else {
			m.setStatus("opened in terminal")
		}
		return m, nil
	}
	return m, nil
}

func (m appModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Filter input captures all keys while active.
	if m.filtering {
		switch key {
		case "esc":
			m.filtering = false
			m.filter.SetValue("")
			m.nav.filter = ""
			m.rebuildNav()
			return m, nil
		case "enter", "return", "ctrl+m", "kpenter":
			m.filtering = false
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.nav.filter = m.filter.Value()
		m.rebuildNav()
		return m, cmd
	}

	// Confirm dialog captures y/n/esc.
	if m.confirming != nil {
		switch key {
		case "y":
			req := m.confirming
			m.confirming = nil
			return m, performActionCmd(*req)
		case "n", "esc":
			m.confirming = nil
		}
		return m, nil
	}

	// Help modal.
	if key == "?" {
		m.showHelp = !m.showHelp
		return m, nil
	}
	if m.showHelp {
		if key == "esc" || key == "q" {
			m.showHelp = false
		}
		return m, nil
	}

	switch key {
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit
	case "tab", "shift+tab":
		m.focusIndex = (m.focusIndex + 1) % 2
		return m, nil
	case "/":
		m.filtering = true
		m.filter.Focus()
		return m, textinput.Blink
	case "r":
		if !m.scanning {
			m.scanning = true
			return m, scanCmd(m.cfg)
		}
		return m, nil
	case "o":
		return m.openSelected()
	case "v":
		if m.nav.view == viewByProject {
			m.nav.view = viewByObject
		} else {
			m.nav.view = viewByProject
		}
		m.rebuildNav()
		return m, nil
	case "d":
		return m.requestRemoveWorktree()
	case "D":
		return m.requestDeleteBranch()
	case "p":
		return m.requestPrune()
	}

	// Focus-routed navigation keys.
	if m.focusIndex == 0 {
		prevID := m.selectedID()
		var cmd tea.Cmd
		m.nav, cmd = m.nav.Update(msg)
		// Collapse state or cursor may have changed: rebuild and re-detail.
		m.rebuildNav()
		if m.selectedID() != prevID {
			m.refreshDetail(true)
		}
		return m, cmd
	}

	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// selectedID returns the stable id of the nav cursor entry.
func (m appModel) selectedID() string {
	if e := m.nav.selectedEntry(); e != nil {
		return e.id + fmt.Sprintf("|%d|%d", e.kind, e.section)
	}
	return ""
}

// rebuildNav rebuilds the tree preserving selection, then refreshes detail.
func (m *appModel) rebuildNav() {
	m.nav.rememberSelection()
	m.nav.focused = m.focusIndex == 0
	m.nav.cfg = m.cfg
	m.nav = m.nav.rebuild(m.data, m.sessions, m.live, m.prs)
	m.refreshDetail(false)
}

// refreshDetail recomputes the right-pane content for the current selection.
func (m *appModel) refreshDetail(gotoTop bool) {
	content := renderDetail(&m.nav, m.data, m.sessions, m.live, m.prs, m.vp.Width, m.cfg)
	m.vp.SetContent(content)
	if gotoTop {
		m.vp.GotoTop()
	}
}

// openSelected implements the `o` key.
func (m appModel) openSelected() (tea.Model, tea.Cmd) {
	e := m.nav.selectedEntry()
	if e == nil || e.repoIdx < 0 || e.repoIdx >= len(m.data) {
		return m, nil
	}
	rd := m.data[e.repoIdx]

	switch e.kind {
	case navKindWorktree:
		if e.itemIdx >= len(rd.Worktrees) {
			return m, nil
		}
		wt := rd.Worktrees[e.itemIdx]
		return m, openTerminalCmd(string(m.cfg.Terminal), wt.Path, wt.Tool)
	case navKindSession:
		ss := m.sessions[rd.Repo.Path]
		if e.itemIdx >= len(ss) {
			return m, nil
		}
		s := ss[e.itemIdx]
		var tool string
		if s.Tool == "claude" {
			tool = "claude --resume " + s.ID
		} else {
			tool = "opencode --session " + s.ID
		}
		return m, openTerminalCmd(string(m.cfg.Terminal), s.Dir, tool)
	case navKindLiveAgent:
		la := m.live[rd.Repo.Path]
		if e.itemIdx >= len(la) {
			return m, nil
		}
		a := la[e.itemIdx]
		return m, openTerminalCmd(string(m.cfg.Terminal), a.Cwd, "claude --resume "+a.ID)
	case navKindPR:
		p := m.prs[e.projID]
		if e.itemIdx < 0 || e.itemIdx >= len(p) {
			m.setStatus("select a pull request to open")
			return m, nil
		}
		pr := p[e.itemIdx]
		if pr.URL == "" {
			m.setStatus("this PR has no URL")
			return m, nil
		}
		return m, openBrowserCmd(pr.URL)
	default:
		m.setStatus("select a worktree, session or pull request to open")
		return m, nil
	}
}

// requestRemoveWorktree implements the `d` key.
func (m appModel) requestRemoveWorktree() (tea.Model, tea.Cmd) {
	e := m.nav.selectedEntry()
	if e == nil || e.kind != navKindWorktree || e.repoIdx >= len(m.data) {
		m.setStatus("select a worktree to remove")
		return m, nil
	}
	rd := m.data[e.repoIdx]
	if e.itemIdx >= len(rd.Worktrees) {
		return m, nil
	}
	wt := rd.Worktrees[e.itemIdx]
	if wt.Main {
		m.setErr(fmt.Errorf("cannot remove the main worktree"))
		return m, nil
	}
	label := "remove worktree " + wt.Branch
	if wt.Branch == "" {
		label = "remove worktree (detached " + wt.ShortSHA + ")"
	}
	m.confirming = &actionRequest{
		Kind:     actionRemoveWorktree,
		Label:    label,
		RepoPath: rd.Repo.Path,
		WtPath:   wt.Path,
	}
	return m, nil
}

// requestDeleteBranch implements the `D` key.
func (m appModel) requestDeleteBranch() (tea.Model, tea.Cmd) {
	e := m.nav.selectedEntry()
	if e == nil || e.kind != navKindBranch || e.repoIdx >= len(m.data) {
		m.setStatus("select a branch to delete")
		return m, nil
	}
	rd := m.data[e.repoIdx]
	if e.itemIdx >= len(rd.Branches) {
		return m, nil
	}
	br := rd.Branches[e.itemIdx]
	if br.Active() {
		m.setErr(fmt.Errorf("branch %s is checked out in a worktree", br.Name))
		return m, nil
	}
	m.confirming = &actionRequest{
		Kind:     actionDeleteBranch,
		Label:    "delete branch " + br.Name,
		RepoPath: rd.Repo.Path,
		Branch:   br.Name,
	}
	return m, nil
}

// requestPrune implements the `p` key (repo level).
func (m appModel) requestPrune() (tea.Model, tea.Cmd) {
	e := m.nav.selectedEntry()
	if e == nil || e.repoIdx < 0 || e.repoIdx >= len(m.data) {
		return m, nil
	}
	rd := m.data[e.repoIdx]
	m.confirming = &actionRequest{
		Kind:     actionPruneWorktrees,
		Label:    "prune stale worktrees in " + rd.Repo.Key,
		RepoPath: rd.Repo.Path,
	}
	return m, nil
}

func (m *appModel) setErr(err error) {
	m.err = err
	m.errSince = time.Now()
	m.statusMsg = ""
}

func (m *appModel) setStatus(s string) {
	m.statusMsg = s
	m.statusAt = time.Now()
	m.err = nil
}

func (m appModel) View() string {
	if !m.ready {
		return "Loading agent-tui…"
	}
	if m.quitting {
		return ""
	}
	if m.showHelp {
		return helpView(m.width, m.height)
	}
	if m.confirming != nil {
		return confirmView(m.width, m.height, m.confirming)
	}

	header := m.headerView()
	nav := m.nav.View()
	content := m.contentView()
	footer := m.footerView()

	body := lipgloss.JoinHorizontal(lipgloss.Top, nav, content)
	return lipgloss.JoinVertical(lipgloss.Top, header, body, footer)
}

func (m appModel) headerView() string {
	nProjects := 0
	seen := make(map[string]bool)
	for _, rd := range m.data {
		if !seen[rd.Repo.Origin.Identity] {
			seen[rd.Repo.Origin.Identity] = true
			nProjects++
		}
	}
	nSessions := 0
	for _, ss := range m.sessions {
		nSessions += len(ss)
	}
	nLive := 0
	for _, la := range m.live {
		nLive += len(la)
	}
	nPRs := 0
	for _, p := range m.prs {
		nPRs += len(p)
	}
	scanState := ""
	if m.scanning {
		scanState = "  " + statusCyan.Render("scanning…")
	}
	left := fmt.Sprintf(" agent-tui — %d projects · %d clones · %d sessions · %d live · %d PRs · %s", nProjects, len(m.data), nSessions, nLive, nPRs, m.nav.view)
	right := ""
	if !m.lastScan.IsZero() {
		right = dimStyle.Render("updated " + m.lastScan.Format("15:04:05")) + " "
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - lipgloss.Width(scanState)
	if gap < 0 {
		gap = 0
	}
	return headerStyle.Width(m.width).Render(left + scanState + strings.Repeat(" ", gap) + right)
}

func (m appModel) contentView() string {
	style := unfocusedBorder
	if m.focusIndex == 1 {
		style = focusedBorder
	}
	return style.
		Width(m.vp.Width + 2).
		Height(m.vp.Height + 2).
		Render(m.vp.View())
}

func (m appModel) footerView() string {
	var focusTag string
	if m.focusIndex == 0 {
		focusTag = statusCyan.Render("NAV")
	} else {
		focusTag = statusCyan.Render("CONTENT")
	}

	var extra string
	if m.err != nil {
		extra = "  " + errorStyle.Render(truncate(m.err.Error(), m.width/2))
	} else if m.statusMsg != "" {
		extra = "  " + statusGreen.Render(m.statusMsg)
	}

	var help string
	if m.filtering {
		help = focusTag + "  " + statusYellow.Render("/"+m.filter.Value()) + "  " + dimStyle.Render("[enter] apply [esc] clear")
	} else {
		help = focusTag + "  " + dimStyle.Render("[enter] collapse [tab] focus [/] filter [o] open [v] view [d/D] delete [p] prune [?] help [q] quit")
	}
	spare := m.width - lipgloss.Width(help) - lipgloss.Width(extra) - 2
	if spare < 0 {
		spare = 0
	}
	return helpStyle.Render(help + strings.Repeat(" ", spare) + extra)
}
