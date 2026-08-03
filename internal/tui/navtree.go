package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tder311/agent-tui/internal/agents"
	"github.com/tder311/agent-tui/internal/config"
	"github.com/tder311/agent-tui/internal/gitx"
)

type navEntryKind int

const (
	navKindProject navEntryKind = iota // a project (normalized origin identity)
	navKindClone                       // one clone/checkout of a project
	navKindSection
	navKindWorktree
	navKindBranch
	navKindSession
	navKindLiveAgent // a running agent (`claude agents --json`)
)

type sectionKind int

const (
	sectionWorktrees sectionKind = iota
	sectionBranches
	sectionLive
	sectionAgents
)

func (s sectionKind) title() string {
	switch s {
	case sectionWorktrees:
		return "Worktrees"
	case sectionBranches:
		return "Branches"
	case sectionLive:
		return "Agents (live)"
	default:
		return "Agents"
	}
}

// projectNode groups the clone indices (into the flat RepoData slice) that
// share one normalized origin, with a disambiguated display name and per-clone
// labels (parent-dir shorthand, e.g. "~/repos").
type projectNode struct {
	origin     gitx.Origin
	name       string // display name (slug, disambiguated on collisions)
	local      bool   // no real remote origin
	clones     []int  // indices into the RepoData slice
	cloneLabel []string
}

// buildProjects groups RepoData by normalized origin identity and computes
// display names and clone labels. Local (no-remote) repos become their own
// project flagged local, so they stay visible and skippable but never merge.
func buildProjects(data []gitx.RepoData) []projectNode {
	byID := make(map[string][]int)
	var order []string
	for i, rd := range data {
		id := rd.Repo.Origin.Identity
		if id == "" {
			id = "local:" + rd.Repo.Path
		}
		if _, ok := byID[id]; !ok {
			order = append(order, id)
		}
		byID[id] = append(byID[id], i)
	}

	projects := make([]projectNode, 0, len(order))
	for _, id := range order {
		clones := byID[id]
		first := data[clones[0]].Repo.Origin
		name := first.Slug
		if name == "" {
			name = filepath.Base(data[clones[0]].Repo.Path)
		}
		projects = append(projects, projectNode{
			origin:     first,
			name:       name,
			local:      !first.HasRemote,
			clones:     clones,
			cloneLabel: cloneLabelsFor(clones, data),
		})
	}

	// Disambiguate colliding display names: remote projects by host, local
	// projects by their (single) clone's parent dir.
	counts := make(map[string]int, len(projects))
	for _, p := range projects {
		counts[p.name]++
	}
	for i := range projects {
		if counts[projects[i].name] < 2 {
			continue
		}
		p := &projects[i]
		if p.local && len(p.clones) == 1 {
			p.name += " (" + shortPath(filepath.Dir(data[p.clones[0]].Repo.Path)) + ")"
		} else if p.origin.Host != "" {
			p.name += " (" + p.origin.Host + ")"
		}
	}

	sort.Slice(projects, func(i, j int) bool {
		return strings.ToLower(projects[i].name) < strings.ToLower(projects[j].name)
	})
	return projects
}

// cloneLabelsFor returns a display label per clone: the parent-dir shorthand
// ("~/repos"). When two clones of a project share a parent dir, the full path
// is used instead so the clones stay distinguishable.
func cloneLabelsFor(clones []int, data []gitx.RepoData) []string {
	labels := make([]string, len(clones))
	for i, ci := range clones {
		labels[i] = shortPath(filepath.Dir(data[ci].Repo.Path))
	}
	seen := make(map[string]bool, len(labels))
	dup := false
	for _, l := range labels {
		if seen[l] {
			dup = true
		}
		seen[l] = true
	}
	if dup {
		for i, ci := range clones {
			labels[i] = shortPath(data[ci].Repo.Path)
		}
	}
	return labels
}

type navEntry struct {
	kind    navEntryKind
	depth   int
	repoIdx int // index into the flat RepoData slice (clone/section/leaf rows)
	projID  string
	section sectionKind
	itemIdx int
	label   string
	badge   string // pre-rendered, e.g. spawner / dirty / tool
	isWT    bool   // worktree row: render the WT tag prefix
	id      string // stable identity across rebuilds
}

// secEntries is the pre-built child list for one section of a clone.
type secEntries struct {
	sec     sectionKind
	entries []navEntry
}

type navTreeModel struct {
	entries     []navEntry
	projects    []projectNode
	cloneLabels map[string]string // repo path -> display label
	cursor      int
	offset      int
	width       int
	height      int
	focused     bool
	loading     bool
	collapsed   map[string]bool
	initialized bool // projects start collapsed on first rebuild
	filter      string
	selID       string
	cfg         *config.Config
}

func newNavTreeModel(width int) navTreeModel {
	return navTreeModel{
		width:     width,
		collapsed: make(map[string]bool),
	}
}

// collapseKey identifies a collapsible node (project, clone, or section).
func (m navTreeModel) collapseKey(e navEntry) string {
	switch e.kind {
	case navKindProject:
		return "proj:" + e.id
	case navKindClone:
		return "clon:" + e.id
	case navKindSection:
		return "sec:" + e.id
	}
	return ""
}

func (m navTreeModel) isCollapsed(e navEntry) bool {
	// While filtering, everything is expanded so matches stay visible.
	if m.filter != "" {
		return false
	}
	k := m.collapseKey(e)
	return k != "" && m.collapsed[k]
}

// rebuild reconstructs the flat entry list from scan data, honoring collapse
// state and filter, and restores the cursor to the previously selected entry.
func (m navTreeModel) rebuild(data []gitx.RepoData, sessions map[string][]agents.Session, live map[string][]agents.Agent) navTreeModel {
	m.entries = nil
	filter := strings.ToLower(strings.TrimSpace(m.filter))

	projects := buildProjects(data)
	m.projects = projects
	m.cloneLabels = make(map[string]string, len(data))
	for _, p := range projects {
		for i, ci := range p.clones {
			m.cloneLabels[data[ci].Repo.Path] = p.cloneLabel[i]
		}
	}

	// First build: projects start collapsed so a machine with many clones
	// stays navigable. Clones and sections expand within an expanded project.
	if !m.initialized {
		for _, p := range projects {
			m.collapsed["proj:"+p.origin.Identity] = true
		}
		m.initialized = true
	}

	match := func(s string) bool {
		return filter == "" || strings.Contains(strings.ToLower(s), filter)
	}

	type cloneEntry struct {
		navEntry
		matches  bool // clone or one of its children matches the filter
		sections []secEntries
	}

	for _, p := range projects {
		var cloneEntries []cloneEntry
		for _, ci := range p.clones {
			rd := data[ci]
			repoID := rd.Repo.Path
			var secs []secEntries

			// Worktrees
			var wtEntries []navEntry
			for wi, wt := range rd.Worktrees {
				label := wt.Branch
				if label == "" {
					label = "(detached " + wt.ShortSHA + ")"
				}
				if wt.Main {
					label += " [main]"
				}
				if !match(label) && !match(wt.Path) {
					continue
				}
				badge := spawnerStyle(wt.Spawner, spawnerColor(m.cfg, wt.Spawner)).Render(worktreeLabel(wt))
				if wt.Dirty {
					badge += " " + statusYellow.Render("✱")
				}
				if wt.Locked {
					badge += " " + statusGray.Render("🔒")
				}
				wtEntries = append(wtEntries, navEntry{
					kind: navKindWorktree, depth: 3, repoIdx: ci,
					section: sectionWorktrees, itemIdx: wi,
					label: label, badge: badge, isWT: true,
					id: repoID + "|wt:" + wt.Path,
				})
			}
			secs = append(secs, secEntries{sectionWorktrees, wtEntries})

			// Branches
			var brEntries []navEntry
			for bi, br := range rd.Branches {
				if !match(br.Name) {
					continue
				}
				badge := ""
				if br.Active() {
					badge = statusCyan.Render("●")
				}
				brEntries = append(brEntries, navEntry{
					kind: navKindBranch, depth: 3, repoIdx: ci,
					section: sectionBranches, itemIdx: bi,
					label: br.Name, badge: badge,
					id: repoID + "|br:" + br.Name,
				})
			}
			secs = append(secs, secEntries{sectionBranches, brEntries})

			// Live agents (running now) — distinct from historical sessions.
			var liveEntries []navEntry
			for li, a := range live[repoID] {
				if !match(a.Name) && !match(a.Cwd) && !match(a.Status) && !match(a.State) {
					continue
				}
				label := a.Name
				if label == "" {
					label = a.ID
				}
				liveEntries = append(liveEntries, navEntry{
					kind: navKindLiveAgent, depth: 3, repoIdx: ci,
					section: sectionLive, itemIdx: li,
					label: label, badge: liveBadge(a),
					id: repoID + "|live:" + a.ID,
				})
			}
			if len(liveEntries) > 0 {
				secs = append(secs, secEntries{sectionLive, liveEntries})
			}

			// Agent sessions
			var agEntries []navEntry
			for si, s := range sessions[repoID] {
				if !match(s.Title) && !match(s.Dir) && !match(s.Tool) {
					continue
				}
				var badge string
				if s.AgentName != "" {
					badge = gearStyle.Render("⚙") + " "
				}
				if s.Tool == "claude" {
					badge += statusOrange.Render("claude")
				} else {
					badge += statusGreen.Render("opencode")
				}
				agEntries = append(agEntries, navEntry{
					kind: navKindSession, depth: 3, repoIdx: ci,
					section: sectionAgents, itemIdx: si,
					label: s.Title, badge: badge,
					id: repoID + "|ag:" + s.Tool + ":" + s.ID,
				})
			}
			secs = append(secs, secEntries{sectionAgents, agEntries})

			anyChild := false
			for _, se := range secs {
				if len(se.entries) > 0 {
					anyChild = true
					break
				}
			}
			cloneLabel := m.cloneLabels[repoID]
			cloneMatches := match(cloneLabel) || match(repoID) || anyChild
			if filter != "" && !cloneMatches {
				continue
			}

			ce := cloneEntry{
				navEntry: navEntry{
					kind: navKindClone, depth: 1, repoIdx: ci,
					projID: p.origin.Identity,
					label:  cloneLabel, id: repoID,
				},
				matches:  cloneMatches,
				sections: secs,
			}
			cloneEntries = append(cloneEntries, ce)
		}

		anyClone := false
		for _, ce := range cloneEntries {
			if ce.matches {
				anyClone = true
				break
			}
		}
		if filter != "" && !match(p.name) && !anyClone {
			continue
		}

		projEntry := navEntry{
			kind:   navKindProject,
			depth:  0,
			projID: p.origin.Identity,
			label:  p.name,
			id:     p.origin.Identity,
		}
		if p.local {
			projEntry.badge = localTag.Render("local")
		}
		m.entries = append(m.entries, projEntry)
		if m.isCollapsed(projEntry) {
			continue
		}

		for _, ce := range cloneEntries {
			m.entries = append(m.entries, ce.navEntry)
			if m.isCollapsed(ce.navEntry) {
				continue
			}
			for _, se := range ce.sections {
				secEntry := navEntry{
					kind: navKindSection, depth: 2, repoIdx: ce.repoIdx,
					projID:  p.origin.Identity,
					section: se.sec, label: se.sec.title(),
					id: ce.navEntry.id + "|sec:" + strconv.Itoa(int(se.sec)),
				}
				m.entries = append(m.entries, secEntry)
				if m.isCollapsed(secEntry) {
					continue
				}
				m.entries = append(m.entries, se.entries...)
			}
		}
	}

	// Restore selection by identity.
	if m.selID != "" {
		for i, e := range m.entries {
			if e.id == m.selID {
				m.cursor = i
				break
			}
		}
		m.selID = ""
	}
	if m.cursor >= len(m.entries) {
		m.cursor = len(m.entries) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	return m
}

func (m *navTreeModel) selectedEntry() *navEntry {
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return nil
	}
	return &m.entries[m.cursor]
}

// rememberSelection stores the cursor entry's stable id for rebuilds.
func (m *navTreeModel) rememberSelection() {
	if e := m.selectedEntry(); e != nil {
		m.selID = e.id
	}
}

func (m navTreeModel) Update(msg tea.Msg) (navTreeModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.entries)-1 {
				m.cursor++
			}
		case "home", "g":
			m.cursor = 0
		case "end", "G":
			if len(m.entries) > 0 {
				m.cursor = len(m.entries) - 1
			}
		case "enter", "return", "ctrl+m", "kpenter", " ":
			if e := m.selectedEntry(); e != nil {
				switch e.kind {
				case navKindProject, navKindClone, navKindSection:
					k := m.collapseKey(*e)
					m.collapsed[k] = !m.collapsed[k]
				}
			}
		}
	}
	m.ensureVisible()
	return m, nil
}

// ensureVisible scrolls so the cursor row is within the viewport.
func (m *navTreeModel) ensureVisible() {
	if m.height <= 0 {
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.height {
		m.offset = m.cursor - m.height + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m navTreeModel) View() string {
	style := unfocusedBorder
	if m.focused {
		style = focusedBorder
	}

	if m.loading && len(m.entries) == 0 {
		return style.Width(m.width).Height(m.height).Render("  Scanning…")
	}
	if len(m.entries) == 0 {
		msg := "  No projects found"
		if m.filter != "" {
			msg = "  No matches for /" + m.filter
		}
		return style.Width(m.width).Height(m.height).Render(msg)
	}

	end := m.offset + m.height
	if end > len(m.entries) || m.height <= 0 {
		end = len(m.entries)
	}
	start := m.offset
	if start < 0 {
		start = 0
	}

	innerW := m.width - 2 // border padding
	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		e := m.entries[i]
		indent := strings.Repeat("  ", e.depth)
		// Single leading marker: collapse triangle for parents, cursor
		// triangle for leaf rows; selection is shown via row highlight.
		marker := " "
		switch e.kind {
		case navKindProject, navKindClone, navKindSection:
			if m.isCollapsed(e) {
				marker = "▸"
			} else {
				marker = "▼"
			}
		default:
			if i == m.cursor {
				marker = "▸"
			}
		}

		prefix := ""
		if e.isWT {
			prefix = wtTag.Render("WT") + " "
		}

		label := truncate(e.label, innerW-lipgloss.Width(indent)-lipgloss.Width(prefix)-lipgloss.Width(e.badge)-4)
		line := fmt.Sprintf("%s%s %s%s", marker, indent, prefix, label)
		if e.badge != "" {
			line += " " + e.badge
		}

		rowStyle := lipgloss.NewStyle()
		if i == m.cursor {
			rowStyle = rowStyle.Background(clrCursor)
		}
		switch e.kind {
		case navKindProject:
			rowStyle = rowStyle.Bold(true)
		case navKindSection:
			rowStyle = rowStyle.Foreground(clrGray)
		}
		lines = append(lines, rowStyle.Render(line))
	}

	return style.Width(m.width).Height(m.height).Render(strings.Join(lines, "\n"))
}
