package tui

import (
	"testing"
	"time"

	"github.com/tder311/agent-tui/internal/agents"
	"github.com/tder311/agent-tui/internal/gitx"
	"github.com/tder311/agent-tui/internal/prs"
)

func fakeData() ([]gitx.RepoData, map[string][]agents.Session, map[string][]agents.Agent, map[string][]prs.PR) {
	alphaOrigin := gitx.Origin{HasRemote: true, Identity: "github.com/org/alpha", Slug: "alpha", Host: "github.com"}
	data := []gitx.RepoData{
		{
			Repo: gitx.RepoInfo{Key: "alpha", Path: "/repos/alpha", Origin: alphaOrigin},
			Worktrees: []gitx.Worktree{
				{Path: "/repos/alpha", Branch: "main", ShortSHA: "abc12345", Kind: gitx.KindMain, Main: true, LastSubject: "init", LastDate: "2 days ago"},
				{Path: "/repos/alpha/.claude/worktrees/agent-1", Branch: "feat/x", ShortSHA: "def67890", Spawner: "claude", Tool: "claude", Dirty: true, HasUpstream: true, Ahead: 2, Behind: 1, LastSubject: "wip", LastDate: "3 hours ago"},
				{Path: "/worktrees/alpha/oc-1", ShortSHA: "99999999", Detached: true, Spawner: "opencode", Tool: "opencode", Locked: true},
				{Path: "/conductor/workspaces/alpha/cape-town", Branch: "cond/cape", ShortSHA: "abcd4321", Spawner: "conductor", Tool: "claude", LastSubject: "exp", LastDate: "1 day ago"},
			},
			Branches: []gitx.Branch{
				{Name: "main", Upstream: "origin/main", Track: "ahead 1", Date: "2 days ago", Subject: "init", WorktreePath: "/repos/alpha"},
				{Name: "feat/x", Upstream: "", Date: "3 hours ago", Subject: "wip", WorktreePath: "/repos/alpha/.claude/worktrees/agent-1"},
				{Name: "stale", Date: "4 weeks ago", Subject: "old stuff"},
			},
		},
		{
			// Second clone of the same upstream project.
			Repo: gitx.RepoInfo{Key: "alpha", Path: "/conductor/repos/alpha", Origin: alphaOrigin},
		},
		{
			Repo: gitx.RepoInfo{
				Key:    "beta",
				Path:   "/repos/beta",
				Origin: gitx.Origin{HasRemote: true, Identity: "github.com/org/beta", Slug: "beta", Host: "github.com"},
			},
			Err: errFake,
		},
		{
			// No remote: local project, flagged local.
			Repo: gitx.RepoInfo{Key: "scratch", Path: "/local/scratch", Origin: gitx.Origin{Identity: "local:/local/scratch", Slug: "scratch"}},
		},
	}
	sessions := map[string][]agents.Session{
		"/repos/alpha": {
			{Tool: "claude", ID: "uuid-1", Title: "uuid-1", Dir: "/repos/alpha", Updated: time.Now().Add(-time.Hour), Messages: 42},
			{Tool: "opencode", ID: "ses_1", Title: "fix stuff", Dir: "/worktrees/alpha/oc-1", Updated: time.Now(), Messages: 7},
		},
		"/conductor/repos/alpha": {
			{Tool: "claude", ID: "uuid-bg-1", Title: "wind profile reshuffling gdm", AgentName: "wind profile reshuffling gdm", Dir: "/conductor/repos/alpha", Updated: time.Now().Add(-30 * time.Minute), Messages: 12},
		},
	}
	live := map[string][]agents.Agent{
		"/repos/alpha": {
			{Tool: "claude", ID: "sess-bg-1", Name: "wind profile reshuffling gdm", Cwd: "/repos/alpha/.claude/worktrees/wt-1", Kind: "background", Status: "busy", StartedAt: time.Now().Add(-2 * time.Hour)},
			{Tool: "claude", ID: "sess-bg-2", Name: "postgres ram optimization", Cwd: "/repos/alpha/.claude/worktrees", Kind: "background", State: "blocked"},
		},
		"/conductor/repos/alpha": {
			{Tool: "claude", ID: "sess-fg-1", Name: "worktrees-9f", Cwd: "/conductor/repos/alpha", Kind: "interactive", Status: "idle"},
		},
	}
	prsByID := map[string][]prs.PR{
		"github.com/org/alpha": {
			{
				Number: 123, Title: "fix scan timeout", State: prs.StateOpen, IsDraft: false,
				HeadRef: "fix/scan-timeout", BaseRef: "main", Author: "tder311",
				CreatedAt: time.Now().Add(-48 * time.Hour), UpdatedAt: time.Now().Add(-2 * time.Hour),
				Additions: 120, Deletions: 45, URL: "https://github.com/org/alpha/pull/123",
			},
			{
				Number: 127, Title: "wip: prs section", State: prs.StateDraft, IsDraft: true,
				HeadRef: "feat/pull-requests", BaseRef: "main", Author: "tder311",
				CreatedAt: time.Now().Add(-24 * time.Hour), UpdatedAt: time.Now().Add(-time.Hour),
				Additions: 900, Deletions: 10, URL: "https://github.com/org/alpha/pull/127",
			},
		},
		// beta is github.com but has zero open PRs → empty state row.
		"github.com/org/beta": {},
	}
	return data, sessions, live, prsByID
}

type fakeErr string

func (e fakeErr) Error() string { return string(e) }

var errFake = fakeErr("repo vanished")

// TestRenderAllSelections walks every nav entry and renders its detail pane,
// catching panics in the rendering paths.
func TestRenderAllSelections(t *testing.T) {
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
	m.rebuildNav()
	m.nav.collapsed = map[string]bool{} // expand all
	m.rebuildNav()

	if len(m.nav.entries) == 0 {
		t.Fatal("no nav entries")
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

func TestCollapseAndFilter(t *testing.T) {
	data, sessions, live, prsByID := fakeData()
	m := appModel{nav: newNavTreeModel(navWidth), data: data, sessions: sessions, live: live, prs: prsByID}
	m.layout()
	m.rebuildNav()

	// Projects start collapsed on first rebuild.
	collapsedCount := len(m.nav.entries)

	// Expand alpha.
	m.nav.collapsed["proj:github.com/org/alpha"] = false
	m.rebuildNav()
	total := len(m.nav.entries)
	if total <= collapsedCount {
		t.Errorf("expanding should grow entries: %d vs %d", total, collapsedCount)
	}

	// Collapse it again.
	m.nav.collapsed["proj:github.com/org/alpha"] = true
	m.rebuildNav()
	if len(m.nav.entries) >= total {
		t.Errorf("collapse should shrink entries: %d vs %d", len(m.nav.entries), total)
	}

	// Filter to a branch name (expands everything).
	m.nav.filter = "feat"
	m.rebuildNav()
	found := false
	for _, e := range m.nav.entries {
		if e.kind == navKindBranch && e.label == "feat/x" {
			found = true
		}
		if e.kind == navKindProject && e.label == "beta" {
			t.Errorf("beta should be filtered out")
		}
	}
	if !found {
		t.Errorf("feat/x should survive filter")
	}
}

func TestSelectionPreservedAcrossRebuild(t *testing.T) {
	data, sessions, live, prsByID := fakeData()
	m := appModel{nav: newNavTreeModel(navWidth), data: data, sessions: sessions, live: live, prs: prsByID}
	m.rebuildNav()
	m.nav.collapsed = map[string]bool{} // expand all
	m.rebuildNav()

	// Put cursor on the claude worktree.
	for i, e := range m.nav.entries {
		if e.kind == navKindWorktree && e.itemIdx == 1 {
			m.nav.cursor = i
		}
	}
	m.rebuildNav()
	e := m.nav.selectedEntry()
	if e == nil || e.kind != navKindWorktree || e.itemIdx != 1 {
		t.Errorf("selection not preserved: %+v", e)
	}
}

func TestActionRequests(t *testing.T) {
	data, sessions, live, prsByID := fakeData()
	m := appModel{nav: newNavTreeModel(navWidth), data: data, sessions: sessions, live: live, prs: prsByID}
	m.rebuildNav()
	m.nav.collapsed = map[string]bool{} // expand all
	m.rebuildNav()

	// Main worktree: refuse removal.
	for i, e := range m.nav.entries {
		if e.kind == navKindWorktree && e.itemIdx == 0 {
			m.nav.cursor = i
		}
	}
	m2, _ := m.requestRemoveWorktree()
	mm := m2.(appModel)
	if mm.confirming != nil || mm.err == nil {
		t.Errorf("removing main worktree should error, got confirm=%v err=%v", mm.confirming, mm.err)
	}

	// Linked worktree: confirm requested.
	for i, e := range m.nav.entries {
		if e.kind == navKindWorktree && e.itemIdx == 1 {
			m.nav.cursor = i
		}
	}
	m2, _ = m.requestRemoveWorktree()
	mm = m2.(appModel)
	if mm.confirming == nil || mm.confirming.Kind != actionRemoveWorktree {
		t.Errorf("expected remove confirm, got %+v", mm.confirming)
	}

	// Active branch: refuse delete. Stale branch: confirm.
	m.confirming = nil
	for i, e := range m.nav.entries {
		if e.kind == navKindBranch && e.label == "main" {
			m.nav.cursor = i
		}
	}
	m2, _ = m.requestDeleteBranch()
	mm = m2.(appModel)
	if mm.confirming != nil || mm.err == nil {
		t.Errorf("deleting checked-out branch should error")
	}
	m.err = nil
	for i, e := range m.nav.entries {
		if e.kind == navKindBranch && e.label == "stale" {
			m.nav.cursor = i
		}
	}
	m2, _ = m.requestDeleteBranch()
	mm = m2.(appModel)
	if mm.confirming == nil || mm.confirming.Branch != "stale" {
		t.Errorf("expected delete confirm for stale, got %+v", mm.confirming)
	}
}

func TestForceOfferFlow(t *testing.T) {
	m := appModel{nav: newNavTreeModel(navWidth)}
	tm, cmd := m.updateMain(ActionResultMsg{
		Kind: actionRemoveWorktree, RepoPath: "/repos/alpha", WtPath: "/repos/alpha/wt", NeedsForce: true,
	})
	if cmd != nil {
		t.Errorf("NeedsForce should not return a cmd")
	}
	mm := tm.(appModel)
	if mm.confirming == nil || mm.confirming.Kind != actionForceRemoveWorktree {
		t.Errorf("expected force confirm, got %+v", mm.confirming)
	}
}
