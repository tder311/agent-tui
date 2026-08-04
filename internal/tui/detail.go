package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/tder311/agent-tui/internal/agents"
	"github.com/tder311/agent-tui/internal/config"
	"github.com/tder311/agent-tui/internal/gitx"
	"github.com/tder311/agent-tui/internal/prs"
)

// renderTable lays out rows under headers, distributing width across columns
// proportionally to weights. Cells are truncated to fit.
func renderTable(width int, headers []string, weights []float64, rows [][]string) string {
	if width <= 0 || len(headers) == 0 {
		return ""
	}
	n := len(headers)
	gap := 2
	if len(weights) != n {
		weights = make([]float64, n)
		for i := range weights {
			weights[i] = 1
		}
	}
	totalW := 0.0
	for _, w := range weights {
		totalW += w
	}
	avail := width - gap*(n-1)
	colWs := make([]int, n)
	used := 0
	for i := range colWs {
		colWs[i] = int(float64(avail) * weights[i] / totalW)
		if colWs[i] < 4 {
			colWs[i] = 4
		}
		used += colWs[i]
	}
	// Shrink the widest columns until we fit.
	for used > avail {
		widest := 0
		for i := range colWs {
			if colWs[i] > colWs[widest] {
				widest = i
			}
		}
		if colWs[widest] <= 4 {
			break
		}
		colWs[widest]--
		used--
	}

	var b strings.Builder
	for i, h := range headers {
		if i > 0 {
			b.WriteString(strings.Repeat(" ", gap))
		}
		b.WriteString(tableHeaderStyle.Render(padCell(h, colWs[i])))
	}
	for _, row := range rows {
		b.WriteString("\n")
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(strings.Repeat(" ", gap))
			}
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			b.WriteString(padCell(cell, colWs[i]))
		}
	}
	return b.String()
}

func padCell(s string, w int) string {
	s = truncate(s, w)
	if cw := lipgloss.Width(s); cw < w {
		s += strings.Repeat(" ", w-cw)
	}
	return s
}

func kv(key, val string) string {
	return dimStyle.Render(fmt.Sprintf("%-14s", key)) + val
}

// renderDetail produces the right-pane content for the current selection.
func renderDetail(nav *navTreeModel, data []gitx.RepoData, sessions map[string][]agents.Session, live map[string][]agents.Agent, prsMap map[string][]prs.PR, width int, cfg *config.Config) string {
	e := nav.selectedEntry()
	if e == nil {
		return dimStyle.Render("No repos discovered.\n\nagent-tui sweeps your configured scan roots\nfor .git markers (default: ~), so any app's\nworktrees are found. Press r to rescan.")
	}
	w := width - 4
	if w < 20 {
		w = 20
	}
	// Object-view section nodes are not tied to a single clone (repoIdx -1);
	// render an aggregate of every project that has items of this kind.
	if e.kind == navKindSection && e.repoIdx < 0 {
		return sectionAggregateDetail(e, nav, data, sessions, live, prsMap, w)
	}
	if e.repoIdx < 0 || e.repoIdx >= len(data) {
		return ""
	}
	rd := data[e.repoIdx]

	switch e.kind {
	case navKindProject:
		return projectDetail(e, nav, data, sessions, live, prsMap, w, cfg)
	case navKindClone:
		return repoDetail(rd, sessions[rd.Repo.Path], live[rd.Repo.Path], w, cfg)
	case navKindSection:
		switch e.section {
		case sectionWorktrees:
			return worktreesTable(rd, w)
		case sectionBranches:
			return branchesTable(rd, w)
		case sectionLive:
			return liveAgentsTable(live[rd.Repo.Path], w)
		case sectionPRs:
			return prsTable(prsMap[e.projID], w)
		default:
			return sessionsTable(sessions[rd.Repo.Path], w)
		}
	case navKindWorktree:
		if e.itemIdx < len(rd.Worktrees) {
			return worktreeDetail(rd.Worktrees[e.itemIdx], w, cfg)
		}
	case navKindBranch:
		if e.itemIdx < len(rd.Branches) {
			return branchDetail(rd.Branches[e.itemIdx], w)
		}
	case navKindSession:
		ss := sessions[rd.Repo.Path]
		if e.itemIdx < len(ss) {
			return sessionDetail(ss[e.itemIdx], w)
		}
	case navKindLiveAgent:
		la := live[rd.Repo.Path]
		if e.itemIdx < len(la) {
			return liveAgentDetail(la[e.itemIdx], w)
		}
	case navKindPR:
		p := prsMap[e.projID]
		if e.itemIdx < 0 {
			return dimStyle.Render("No open pull requests for this project.")
		}
		if e.itemIdx < len(p) {
			return prDetail(p[e.itemIdx], w)
		}
	}
	return ""
}

// projectDetail renders a project node: its origin, aggregate counts, a
// compact per-clone summary, its open PRs, and any live agents running across
// the project.
func projectDetail(e *navEntry, nav *navTreeModel, data []gitx.RepoData, sessions map[string][]agents.Session, live map[string][]agents.Agent, prsMap map[string][]prs.PR, w int, cfg *config.Config) string {
	var p *projectNode
	for i := range nav.projects {
		if nav.projects[i].origin.Identity == e.projID {
			p = &nav.projects[i]
			break
		}
	}
	if p == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render(" " + p.name + " ") + "\n\n")
	if p.local {
		b.WriteString(kv("Origin", localTag.Render("local (no remote)")) + "\n")
	} else {
		b.WriteString(kv("Origin", p.origin.Identity) + "\n")
	}
	clones := len(p.clones)
	worktrees, branches, sess, liveN := 0, 0, 0, 0
	for _, ci := range p.clones {
		rd := data[ci]
		worktrees += len(rd.Worktrees)
		branches += len(rd.Branches)
		sess += len(sessions[rd.Repo.Path])
		liveN += len(live[rd.Repo.Path])
	}
	prsN := 0
	if _, eligible := nav.projectPRs(p); eligible {
		prsN = len(prsMap[p.origin.Identity])
	}
	b.WriteString(kv("Clones", fmt.Sprintf("%d", clones)) + "   " +
		kv("Worktrees", fmt.Sprintf("%d", worktrees)) + "   " +
		kv("Branches", fmt.Sprintf("%d", branches)) + "   " +
		kv("Sessions", fmt.Sprintf("%d", sess)) + "   " +
		kv("Live", fmt.Sprintf("%d", liveN)) + "   " +
		kv("PRs", fmt.Sprintf("%d", prsN)) + "\n\n")

	b.WriteString(tableHeaderStyle.Render("CLONES") + "\n")
	for i, ci := range p.clones {
		rd := data[ci]
		label := p.cloneLabel[i]
		if label == "" {
			label = rd.Repo.Key
		}
		line := "  " + label + "   " + dimStyle.Render(fmt.Sprintf("%d worktrees · %d branches · %d sessions · %d live",
			len(rd.Worktrees), len(rd.Branches), len(sessions[rd.Repo.Path]), len(live[rd.Repo.Path])))
		b.WriteString(line + "\n")
		if rd.Err != nil {
			b.WriteString(errorStyle.Render("    scan error: "+rd.Err.Error()) + "\n")
		}
	}

	if prsN > 0 {
		b.WriteString("\n" + tableHeaderStyle.Render("PULL REQUESTS") + "\n")
		b.WriteString(prsTable(prsMap[p.origin.Identity], w))
	}

	if liveN > 0 {
		b.WriteString("\n" + tableHeaderStyle.Render("LIVE AGENTS") + "\n")
		for i, ci := range p.clones {
			rd := data[ci]
			label := p.cloneLabel[i]
			if label == "" {
				label = rd.Repo.Key
			}
			for _, a := range live[rd.Repo.Path] {
				name := a.Name
				if name == "" {
					name = a.ID
				}
				line := "  " + liveDot(a) + " " + liveKindTag(a) + "  " +
					dimStyle.Render(label) + "  " + liveStatusWord(a) + "  " +
					truncate(name, w-20)
				b.WriteString(line + "\n")
			}
		}
	}
	b.WriteString("\n" + dimStyle.Render("enter/return to expand a clone   r refresh"))
	return b.String()
}

// sectionAggregateDetail renders an object-view section node (not tied to any
// single clone): a per-project breakdown of every project that has items of
// this kind.
func sectionAggregateDetail(e *navEntry, nav *navTreeModel, data []gitx.RepoData, sessions map[string][]agents.Session, live map[string][]agents.Agent, prsMap map[string][]prs.PR, w int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render(" "+e.section.title()+" ") + "\n")
	b.WriteString(dimStyle.Render(strings.ToLower(e.section.title())+" across all projects") + "\n")

	shown := 0
	for pi := range nav.projects {
		p := &nav.projects[pi]
		blocks := sectionProjectBlocks(p, e.section, data, sessions, live, prsMap, w)
		if len(blocks) == 0 {
			continue
		}
		shown++
		b.WriteString("\n" + tableHeaderStyle.Render(" "+p.name+" ") + "\n")
		for _, blk := range blocks {
			b.WriteString(blk + "\n")
		}
	}
	if shown == 0 {
		b.WriteString(dimStyle.Render("Nothing here yet.") + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("v toggle view   enter/return to expand a project"))
	return b.String()
}

// sectionProjectBlocks renders one project's contribution to an object-view
// section: the relevant table per clone (or a single PR table, which is
// project-scoped).
func sectionProjectBlocks(p *projectNode, sec sectionKind, data []gitx.RepoData, sessions map[string][]agents.Session, live map[string][]agents.Agent, prsMap map[string][]prs.PR, w int) []string {
	var out []string
	if sec == sectionPRs {
		prsFor := prsMap[p.origin.Identity]
		if len(prsFor) > 0 {
			out = append(out, prsTable(prsFor, w))
		}
		return out
	}
	for i, ci := range p.clones {
		rd := data[ci]
		label := p.cloneLabel[i]
		if label == "" {
			label = rd.Repo.Key
		}
		var body string
		switch sec {
		case sectionWorktrees:
			if len(rd.Worktrees) == 0 {
				continue
			}
			body = worktreesTable(rd, w)
		case sectionBranches:
			if len(rd.Branches) == 0 {
				continue
			}
			body = branchesTable(rd, w)
		case sectionLive:
			if len(live[rd.Repo.Path]) == 0 {
				continue
			}
			body = liveAgentsTable(live[rd.Repo.Path], w)
		default:
			if len(sessions[rd.Repo.Path]) == 0 {
				continue
			}
			body = sessionsTable(sessions[rd.Repo.Path], w)
		}
		out = append(out, dimStyle.Render("· "+label)+"\n"+body)
	}
	return out
}

// liveAgentsTable lists the agents currently running in a clone.
func liveAgentsTable(live []agents.Agent, w int) string {
	if len(live) == 0 {
		return dimStyle.Render("No live agents right now.")
	}
	rows := make([][]string, 0, len(live))
	for _, a := range live {
		name := a.Name
		if name == "" {
			name = a.ID
		}
		status := a.Status
		if status == "" {
			status = a.State
		}
		rows = append(rows, []string{a.Kind, status, name, shortPath(a.Cwd), relTime(a.StartedAt)})
	}
	return renderTable(w, []string{"KIND", "STATUS", "NAME", "CWD", "STARTED"},
		[]float64{1.2, 1.4, 2.6, 3, 1}, rows)
}

// liveAgentDetail renders one running agent.
func liveAgentDetail(a agents.Agent, w int) string {
	var b strings.Builder
	name := a.Name
	if name == "" {
		name = a.ID
	}
	b.WriteString(headerStyle.Render(" "+name+" ") + "\n\n")
	b.WriteString(kv("Status", liveStatusWord(a)) + "\n")
	b.WriteString(kv("Kind", a.Kind) + "\n")
	b.WriteString(kv("Tool", statusOrange.Render("claude")) + "\n")
	b.WriteString(kv("Session ID", a.ID) + "\n")
	b.WriteString(kv("Directory", shortPath(a.Cwd)) + "\n")
	if a.PID != 0 {
		b.WriteString(kv("PID", fmt.Sprintf("%d", a.PID)) + "\n")
	}
	if !a.StartedAt.IsZero() {
		b.WriteString(kv("Started", a.StartedAt.Format("2006-01-02 15:04:05")+" ("+relTime(a.StartedAt)+")") + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("o resume in terminal (claude --resume)   r refresh"))
	return b.String()
}

func repoDetail(rd gitx.RepoData, sess []agents.Session, live []agents.Agent, w int, cfg *config.Config) string {
	var b strings.Builder
	title := headerStyle.Render(" " + rd.Repo.Key + " ")
	b.WriteString(title + "\n\n")
	if rd.Repo.Origin.HasRemote {
		b.WriteString(kv("Project", rd.Repo.Origin.Identity) + "\n")
	} else {
		b.WriteString(kv("Project", localTag.Render("local (no remote)")) + "\n")
	}
	b.WriteString(kv("Path", rd.Repo.Path) + "\n")
	b.WriteString(kv("Git dir", shortPath(rd.Repo.CommonDir)) + "\n")
	if rd.Repo.Bare {
		b.WriteString(kv("Repo", statusYellow.Render("bare")) + "\n")
	}
	if rd.Err != nil {
		b.WriteString("\n" + errorStyle.Render("scan error: "+rd.Err.Error()) + "\n")
	}
	b.WriteString("\n" + kv("Worktrees", fmt.Sprintf("%d", len(rd.Worktrees))) +
		"   " + kv("Branches", fmt.Sprintf("%d", len(rd.Branches))) +
		"   " + kv("Sessions", fmt.Sprintf("%d", len(sess))) +
		"   " + kv("Live", fmt.Sprintf("%d", len(live))) + "\n\n")

	if len(rd.Worktrees) > 0 {
		b.WriteString(tableHeaderStyle.Render("WORKTREES") + "\n")
		b.WriteString(worktreesTable(rd, w))
	}
	return b.String()
}

func worktreesTable(rd gitx.RepoData, w int) string {
	if len(rd.Worktrees) == 0 {
		return dimStyle.Render("No worktrees.")
	}
	rows := make([][]string, 0, len(rd.Worktrees))
	for _, wt := range rd.Worktrees {
		branch := wt.Branch
		if branch == "" {
			branch = "(detached " + wt.ShortSHA + ")"
		}
		state := "clean"
		if wt.Dirty {
			state = "dirty"
		}
		if wt.Locked {
			state += ",locked"
		}
		ab := ""
		if wt.HasUpstream {
			ab = fmt.Sprintf("↑%d ↓%d", wt.Ahead, wt.Behind)
		}
		last := wt.LastSubject
		if wt.LastDate != "" {
			last = wt.LastDate + " · " + last
		}
		rows = append(rows, []string{branch, shortPath(wt.Path), worktreeLabel(wt), state, ab, last})
	}
	return renderTable(w, []string{"BRANCH", "PATH", "KIND", "STATE", "A/B", "LAST COMMIT"},
		[]float64{2, 3, 1.2, 1.4, 1, 3}, rows)
}

func branchesTable(rd gitx.RepoData, w int) string {
	if len(rd.Branches) == 0 {
		return dimStyle.Render("No local branches.")
	}
	rows := make([][]string, 0, len(rd.Branches))
	for _, br := range rd.Branches {
		checked := ""
		if br.Active() {
			checked = shortPath(br.WorktreePath)
		}
		last := br.Date
		if br.Subject != "" {
			last += " · " + br.Subject
		}
		rows = append(rows, []string{br.Name, br.Upstream, br.Track, checked, last})
	}
	return renderTable(w, []string{"NAME", "UPSTREAM", "TRACK", "CHECKED OUT", "LAST COMMIT"},
		[]float64{2, 2, 1.4, 2.4, 3}, rows)
}

func sessionsTable(sess []agents.Session, w int) string {
	if len(sess) == 0 {
		return dimStyle.Render("No agent sessions found for this repo.")
	}
	rows := make([][]string, 0, len(sess))
	for _, s := range sess {
		title := s.Title
		if s.AgentName != "" {
			title = gearStyle.Render("⚙") + " " + title
		}
		rows = append(rows, []string{
			s.Tool,
			title,
			relTime(s.Updated),
			fmt.Sprintf("%d", s.Messages),
			shortPath(s.Dir),
		})
	}
	return renderTable(w, []string{"TOOL", "TITLE", "UPDATED", "MSGS", "DIR"},
		[]float64{1.2, 3, 1.2, 0.8, 3}, rows)
}

// prsTable lists a project's open pull requests.
func prsTable(prsList []prs.PR, w int) string {
	if len(prsList) == 0 {
		return dimStyle.Render("No open pull requests.")
	}
	rows := make([][]string, 0, len(prsList))
	for _, p := range prsList {
		delta := ""
		if p.Additions > 0 || p.Deletions > 0 {
			delta = fmt.Sprintf("+%d −%d", p.Additions, p.Deletions)
		}
		rows = append(rows, []string{
			prStateBadge(p),
			"#" + strconv.Itoa(p.Number) + "  " + p.Title,
			p.HeadRef + " → " + p.BaseRef,
			delta,
			relTime(p.UpdatedAt),
		})
	}
	return renderTable(w, []string{"STATE", "PR", "BRANCHES", "DIFF", "UPDATED"},
		[]float64{1.2, 3.2, 2.2, 1, 1.2}, rows)
}

// prDetail renders one pull request.
func prDetail(p prs.PR, w int) string {
	var b strings.Builder
	title := fmt.Sprintf("#%d  %s", p.Number, p.Title)
	b.WriteString(headerStyle.Render(" "+truncate(title, w-4)+" ") + "\n\n")
	b.WriteString(kv("State", prStateBadge(p)) + "\n")
	if p.IsDraft {
		b.WriteString(kv("Draft", dimStyle.Render("yes")) + "\n")
	}
	b.WriteString(kv("Branches", p.HeadRef+" → "+p.BaseRef) + "\n")
	b.WriteString(kv("Author", p.Author) + "\n")
	b.WriteString(kv("URL", truncate(p.URL, w-16)) + "\n")
	if !p.CreatedAt.IsZero() {
		b.WriteString(kv("Created", p.CreatedAt.Format("2006-01-02 15:04:05")) + "\n")
	}
	if !p.UpdatedAt.IsZero() {
		b.WriteString(kv("Updated", p.UpdatedAt.Format("2006-01-02 15:04:05")+" ("+relTime(p.UpdatedAt)+")") + "\n")
	}
	b.WriteString(kv("Diff", fmt.Sprintf("+%d additions  −%d deletions", p.Additions, p.Deletions)) + "\n")
	b.WriteString("\n" + dimStyle.Render("o open PR in browser   r refresh"))
	return b.String()
}

func worktreeDetail(wt gitx.Worktree, w int, cfg *config.Config) string {
	var b strings.Builder
	branch := wt.Branch
	if branch == "" {
		branch = "(detached HEAD)"
	}
	b.WriteString(headerStyle.Render(" "+branch+" ") + "\n\n")
	b.WriteString(kv("Path", wt.Path) + "\n")
	b.WriteString(kv("Kind", spawnerStyle(wt.Spawner, spawnerColor(cfg, wt.Spawner)).Render(worktreeLabel(wt))) + "\n")
	b.WriteString(kv("HEAD", wt.ShortSHA) + "\n")
	b.WriteString(kv("State", dirtyMark(wt.Dirty)) + "\n")
	if wt.Locked {
		b.WriteString(kv("Locked", statusYellow.Render("yes")) + "\n")
	}
	if wt.Prunable {
		b.WriteString(kv("Prunable", statusYellow.Render("yes")) + "\n")
	}
	if wt.HasUpstream {
		b.WriteString(kv("Ahead/Behind", fmt.Sprintf("↑%d ↓%d", wt.Ahead, wt.Behind)) + "\n")
	}
	if wt.LastSubject != "" {
		b.WriteString(kv("Last commit", truncate(wt.LastSubject, w-16)) + "\n")
		b.WriteString(kv("When", wt.LastDate) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("o open in terminal   d remove worktree   r refresh"))
	return b.String()
}

func branchDetail(br gitx.Branch, w int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render(" "+br.Name+" ") + "\n\n")
	if br.Upstream != "" {
		b.WriteString(kv("Upstream", br.Upstream) + "\n")
	} else {
		b.WriteString(kv("Upstream", dimStyle.Render("(none)")) + "\n")
	}
	if br.Track != "" {
		b.WriteString(kv("Track", br.Track) + "\n")
	}
	if br.Active() {
		b.WriteString(kv("Checked out", statusCyan.Render("●")+" "+shortPath(br.WorktreePath)) + "\n")
	} else {
		b.WriteString(kv("Checked out", dimStyle.Render("no")) + "\n")
	}
	if br.Subject != "" {
		b.WriteString(kv("Last commit", truncate(br.Subject, w-16)) + "\n")
		b.WriteString(kv("When", br.Date) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("D delete branch   r refresh"))
	return b.String()
}

func sessionDetail(s agents.Session, w int) string {
	var b strings.Builder
	tool := s.Tool
	if tool == "claude" {
		tool = statusOrange.Render("claude")
	} else {
		tool = statusGreen.Render("opencode")
	}
	b.WriteString(headerStyle.Render(" "+s.Tool+" session ") + "\n\n")
	b.WriteString(kv("Tool", tool) + "\n")
	if s.AgentName != "" {
		b.WriteString(kv("Agent", gearStyle.Render("⚙")+" "+s.AgentName) + "\n")
	}
	// Full, wrapped title — never truncated.
	b.WriteString(dimStyle.Render(fmt.Sprintf("%-14s", "Title")) + "\n")
	b.WriteString(lipgloss.NewStyle().PaddingLeft(14).MaxWidth(w-14).Render(s.Title) + "\n")
	b.WriteString(kv("ID", s.ID) + "\n")
	b.WriteString(kv("Directory", s.Dir) + "\n")
	b.WriteString(kv("Updated", s.Updated.Format("2006-01-02 15:04:05")+" ("+relTime(s.Updated)+")") + "\n")
	b.WriteString(kv("Messages", fmt.Sprintf("%d", s.Messages)) + "\n")
	b.WriteString("\n" + dimStyle.Render("o open/resume in terminal   r refresh"))
	return b.String()
}

// shortPath replaces $HOME with ~ for compact display.
func shortPath(p string) string {
	if p == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

// relTime renders a compact relative time.
func relTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
