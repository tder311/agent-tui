package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tder311/agent-tui/internal/agents"
	"github.com/tder311/agent-tui/internal/gitx"
	"github.com/tder311/agent-tui/internal/prs"
)

// objectViewApp builds a fully-expanded app in the object view.
func objectViewApp() appModel {
	data, sessions, live, prsByID := fakeData()
	m := appModel{
		width:    120,
		height:   40,
		ready:    true,
		nav:      newNavTreeModel(navWidth),
		data:     data,
		sessions: sessions,
		live:     live,
		prs:      prsByID,
	}
	m.layout()
	m.nav.view = viewByObject
	m.rebuildNav()
	m.nav.collapsed = map[string]bool{}
	m.rebuildNav()
	return m
}

// emptyObjectViewApp builds a fully-expanded object view with no items of any
// kind: one github.com clone with no worktrees, branches, sessions, live
// agents or PRs.
func emptyObjectViewApp() appModel {
	data := []gitx.RepoData{
		{
			Repo: gitx.RepoInfo{Key: "alpha", Path: "/repos/alpha", Origin: gitx.Origin{HasRemote: true, Identity: "github.com/org/alpha", Slug: "alpha", Host: "github.com"}},
		},
	}
	m := appModel{
		width: 120, height: 40, ready: true,
		nav:      newNavTreeModel(navWidth),
		data:     data,
		sessions: map[string][]agents.Session{},
		live:     map[string][]agents.Agent{},
		prs:      map[string][]prs.PR{},
	}
	m.layout()
	m.nav.view = viewByObject
	m.rebuildNav()
	m.nav.collapsed = map[string]bool{}
	m.rebuildNav()
	return m
}

// TestObjectViewAlwaysShowsSections: every fixed section header shows even
// when it has nothing in it, with a dim empty-state row underneath.
func TestObjectViewAlwaysShowsSections(t *testing.T) {
	m := emptyObjectViewApp()

	want := map[sectionKind]string{
		sectionWorktrees: "No worktrees",
		sectionBranches:  "No branches",
		sectionLive:      "No live agents",
		sectionAgents:    "No agent sessions",
		sectionPRs:       "No open PRs",
	}
	var gotSections []sectionKind
	for i := range m.nav.entries {
		e := m.nav.entries[i]
		if e.kind == navKindSection {
			gotSections = append(gotSections, e.section)
			continue
		}
		if e.kind != navKindEmpty {
			t.Errorf("expected only empty rows under empty sections, got %+v", e)
			continue
		}
		if want[e.section] != e.label {
			t.Errorf("section %s: empty row %q, want %q", e.section.title(), e.label, want[e.section])
		}
		if e.depth != 1 || e.repoIdx != -1 {
			t.Errorf("empty row %q: depth=%d repoIdx=%d, want depth 1 repoIdx -1", e.label, e.depth, e.repoIdx)
		}
	}
	if len(gotSections) != len(objectSectionOrder) {
		t.Fatalf("expected %d sections, got %v", len(objectSectionOrder), gotSections)
	}
	for i, sec := range objectSectionOrder {
		if gotSections[i] != sec {
			t.Errorf("sections out of order: got %v, want %v", gotSections, objectSectionOrder)
			break
		}
	}

	// Empty rows render without panicking and are not collapsible targets.
	for i := range m.nav.entries {
		m.nav.cursor = i
		m.refreshDetail(false)
		if m.View() == "" {
			t.Errorf("empty view for entry %d", i)
		}
	}
}

func TestObjectViewTreeShape(t *testing.T) {
	m := objectViewApp()

	// Walk the whole tree: sections in fixed order at depth 0, projects at
	// depth 1, leaves at depth 2, sections not tied to a clone (repoIdx -1).
	var gotSections []sectionKind
	wantLeaves := map[string]int{
		sectionWorktrees.title(): 4, // alpha: 4 worktrees
		sectionBranches.title():  3, // alpha: 3 branches
		sectionLive.title():      3, // alpha: 2 + 1 live agents
		sectionAgents.title():    3, // alpha: 2 + 1 sessions
		sectionPRs.title():       2, // alpha: 2 PRs (beta has none)
	}
	gotLeaves := map[string]int{}
	projects := map[string]bool{}
	for i := range m.nav.entries {
		e := m.nav.entries[i]
		switch e.kind {
		case navKindSection:
			if e.depth != 0 {
				t.Errorf("section %q depth=%d, want 0", e.label, e.depth)
			}
			if e.repoIdx != -1 {
				t.Errorf("section %q repoIdx=%d, want -1", e.label, e.repoIdx)
			}
			gotSections = append(gotSections, e.section)
		case navKindProject:
			if e.depth != 1 {
				t.Errorf("project %q depth=%d, want 1", e.label, e.depth)
			}
			projects[e.projID] = true
		default:
			if e.depth != 2 {
				t.Errorf("leaf %q depth=%d, want 2", e.label, e.depth)
			}
			gotLeaves[e.section.title()]++
		}
	}
	for sec, want := range wantLeaves {
		if gotLeaves[sec] != want {
			t.Errorf("section %q: got %d leaves, want %d", sec, gotLeaves[sec], want)
		}
	}
	for i, sec := range objectSectionOrder {
		if i >= len(gotSections) || gotSections[i] != sec {
			t.Errorf("sections out of order: got %v, want %v", gotSections, objectSectionOrder)
			break
		}
	}
	// Only alpha has items in the object view; beta (scan error) and the
	// local scratch repo appear nowhere.
	if len(projects) != 1 || !projects["github.com/org/alpha"] {
		t.Errorf("expected only alpha in object view, got %v", projects)
	}

	// Object-view ids must be globally unique so collapse state never collides.
	ids := map[string]bool{}
	for i := range m.nav.entries {
		id := m.nav.entries[i].id
		if ids[id] {
			t.Errorf("duplicate id %q", id)
		}
		ids[id] = true
	}
}

func TestObjectViewFilter(t *testing.T) {
	m := objectViewApp()
	m.nav.filter = "feat"
	m.rebuildNav()

	for i := range m.nav.entries {
		e := m.nav.entries[i]
		switch e.kind {
		case navKindBranch:
			if e.label != "feat/x" {
				t.Errorf("branch %q should not survive filter feat", e.label)
			}
		case navKindWorktree:
			if e.label != "feat/x" {
				t.Errorf("worktree %q should not survive filter feat", e.label)
			}
		case navKindPR:
			if e.label != "#127  wip: prs section" {
				t.Errorf("PR %q should not survive filter feat", e.label)
			}
		}
	}
}

func TestObjectViewToggle(t *testing.T) {
	data, sessions, live, prsByID := fakeData()
	m := appModel{width: 120, height: 40, ready: true, nav: newNavTreeModel(navWidth), data: data, sessions: sessions, live: live, prs: prsByID}
	m.layout()
	m.rebuildNav()

	if m.nav.view != viewByProject {
		t.Fatal("default view should be by project")
	}
	projIDs := map[string]bool{}
	for _, e := range m.nav.entries {
		projIDs[e.id] = true
	}

	// v → object view. Sections shown with collapsed project groups.
	tm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if cmd != nil {
		t.Fatalf("v should not return a cmd, got %v", cmd)
	}
	m = tm.(appModel)
	if m.nav.view != viewByObject {
		t.Fatal("v should switch to object view")
	}
	if len(m.nav.entries) < len(objectSectionOrder) {
		t.Fatalf("object view should show %d sections, got %d entries", len(objectSectionOrder), len(m.nav.entries))
	}
	for _, e := range m.nav.entries {
		if projIDs[e.id] {
			t.Errorf("object view reused a project-view id %q", e.id)
		}
	}
	// First toggle seeds per-view collapse defaults: every object-view
	// project group is collapsed.
	for _, e := range m.nav.entries {
		if e.kind == navKindProject && !m.nav.isCollapsed(e) {
			t.Errorf("object-view project %q should start collapsed", e.label)
		}
	}

	// v again → back to project view.
	tm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = tm.(appModel)
	if m.nav.view != viewByProject {
		t.Fatal("second v should return to project view")
	}
}

// TestObjectViewRenderAll walks every object-view entry and renders its detail
// pane, catching panics (notably the aggregate section pane and repoIdx=-1
// section/project rows).
func TestObjectViewRenderAll(t *testing.T) {
	m := objectViewApp()
	if len(m.nav.entries) == 0 {
		t.Fatal("no object-view entries")
	}
	for i := range m.nav.entries {
		m.nav.cursor = i
		m.refreshDetail(false)
		v := m.View()
		if v == "" {
			t.Errorf("empty view for entry %d (%+v)", i, m.nav.entries[i])
		}
	}
}

// TestObjectViewSectionActions: opening or pruning an object-view section node
// (repoIdx -1) must be a no-op, never panic.
func TestObjectViewSectionActions(t *testing.T) {
	m := objectViewApp()
	// Collapse everything so the cursor can reach section rows quickly.
	m.nav.cursor = 0
	e := m.nav.selectedEntry()
	if e == nil || e.kind != navKindSection {
		t.Fatalf("expected first entry to be a section, got %+v", e)
	}

	tm, cmd := m.openSelected()
	if cmd != nil || tm == nil {
		t.Errorf("openSelected on section should no-op, got cmd=%v model=%v", cmd, tm != nil)
	}
	m2, _ := m.requestPrune()
	if m2.(appModel).confirming != nil {
		t.Errorf("prune on section should no-op")
	}
	m3, _ := m.requestDeleteBranch()
	if m3.(appModel).confirming != nil {
		t.Errorf("delete branch on section should no-op")
	}
}
