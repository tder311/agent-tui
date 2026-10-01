package tui

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/tder311/agent-tui/internal/agents"
	"github.com/tder311/agent-tui/internal/config"
	"github.com/tder311/agent-tui/internal/gitx"
)

func TestBuildProjectsGrouping(t *testing.T) {
	mk := func(path, id, slug, host string, remote bool) gitx.RepoData {
		return gitx.RepoData{Repo: gitx.RepoInfo{
			Path:   path,
			Origin: gitx.Origin{HasRemote: remote, Identity: id, Slug: slug, Host: host},
		}}
	}
	data := []gitx.RepoData{
		mk("/x/a/widget", "github.com/exampleorg/widget", "widget", "github.com", true),
		mk("/x/b/widget", "gitlab.com/exampleorg/widget", "widget", "gitlab.com", true),
		mk("/x/codex/a/repo", "local:/x/codex/a/repo", "repo", "", false),
		mk("/x/codex/b/repo", "local:/x/codex/b/repo", "repo", "", false),
	}
	projs := buildProjects(data)
	if len(projs) != 4 {
		t.Fatalf("expected 4 projects, got %d", len(projs))
	}
	names := map[string]bool{}
	for _, p := range projs {
		names[p.name] = true
	}
	for _, want := range []string{"widget (github.com)", "widget (gitlab.com)", "repo (/x/codex/a)", "repo (/x/codex/b)"} {
		if !names[want] {
			t.Errorf("missing project name %q; got %v", want, names)
		}
	}
}

func TestBuildProjectsLocalFlag(t *testing.T) {
	data := []gitx.RepoData{
		{Repo: gitx.RepoInfo{Path: "/r/one", Origin: gitx.Origin{HasRemote: true, Identity: "h/x", Slug: "x", Host: "h"}}},
		{Repo: gitx.RepoInfo{Path: "/r/two", Origin: gitx.Origin{Identity: "local:/r/two"}}},
	}
	projs := buildProjects(data)
	var local, remote *projectNode
	for i := range projs {
		switch projs[i].name {
		case "two":
			local = &projs[i]
		case "x":
			remote = &projs[i]
		}
	}
	if local == nil || remote == nil {
		t.Fatalf("missing projects: %+v", projs)
	}
	if !local.local || remote.local {
		t.Errorf("local flags wrong: local=%+v remote=%+v", *local, *remote)
	}
}

func TestCloneLabelsSharedParent(t *testing.T) {
	data := []gitx.RepoData{
		{Repo: gitx.RepoInfo{Path: "/repos/widget", Origin: gitx.Origin{HasRemote: true, Identity: "github.com/x/widget", Slug: "widget", Host: "github.com"}}},
		{Repo: gitx.RepoInfo{Path: "/repos/widget-backup", Origin: gitx.Origin{HasRemote: true, Identity: "github.com/x/widget", Slug: "widget", Host: "github.com"}}},
	}
	projs := buildProjects(data)
	if len(projs) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projs))
	}
	p := projs[0]
	if p.cloneLabel[0] != "/repos/widget" || p.cloneLabel[1] != "/repos/widget-backup" {
		t.Errorf("clone labels not disambiguated to full paths: %v", p.cloneLabel)
	}
}

func TestProjectTreeShape(t *testing.T) {
	data, sessions, live, prsByID := fakeData()
	m := appModel{nav: newNavTreeModel(navWidth), data: data, sessions: sessions, live: live, prs: prsByID}
	m.rebuildNav()
	m.nav.collapsed = map[string]bool{}
	m.rebuildNav()

	projects, clones, wts, branches, local := 0, 0, 0, 0, 0
	liveAgents, liveSections := 0, 0
	cloneLabels := map[string]bool{}
	for _, e := range m.nav.entries {
		switch e.kind {
		case navKindProject:
			projects++
			if e.badge != "" {
				local++
			}
		case navKindClone:
			clones++
			cloneLabels[e.label] = true
		case navKindWorktree:
			wts++
			if !e.isWT {
				t.Errorf("worktree row missing WT flag: %+v", e)
			}
		case navKindBranch:
			branches++
			if e.isWT {
				t.Errorf("branch row must not be WT: %+v", e)
			}
		case navKindSection:
			if e.section == sectionLive {
				liveSections++
			}
		case navKindLiveAgent:
			liveAgents++
			if e.badge == "" {
				t.Errorf("live agent row missing badge: %+v", e)
			}
		}
	}
	if projects != 3 {
		t.Errorf("projects = %d, want 3", projects)
	}
	if clones != 4 {
		t.Errorf("clones = %d, want 4", clones)
	}
	if !cloneLabels["/repos"] || !cloneLabels["/conductor/repos"] || !cloneLabels["/local"] {
		t.Errorf("clone labels wrong: %v", cloneLabels)
	}
	if wts != 4 {
		t.Errorf("worktrees = %d, want 4", wts)
	}
	if branches != 3 {
		t.Errorf("branches = %d, want 3", branches)
	}
	if local != 1 {
		t.Errorf("local-badged projects = %d, want 1 (scratch)", local)
	}
	if liveAgents != 3 {
		t.Errorf("live agent rows = %d, want 3", liveAgents)
	}
	if liveSections != 2 {
		t.Errorf("live sections = %d, want 2 (alpha clone1 + clone2)", liveSections)
	}

	v := m.nav.View()
	if !strings.Contains(v, "WT") {
		t.Errorf("nav view should render WT tag:\n%s", v)
	}
	if !strings.Contains(v, "Agents (live)") {
		t.Errorf("nav view should render the live-agents section:\n%s", v)
	}
}

// TestProjectDetail asserts the project right-pane shows the origin, aggregate
// and per-clone counts, and the live agents running anywhere in the project.
func TestProjectDetail(t *testing.T) {
	data, sessions, live, prsByID := fakeData()
	m := appModel{
		width: 120, height: 40, ready: true,
		nav: newNavTreeModel(navWidth), data: data, sessions: sessions, live: live, prs: prsByID,
	}
	m.layout()
	m.rebuildNav()
	m.nav.collapsed = map[string]bool{}
	m.rebuildNav()

	for i, e := range m.nav.entries {
		if e.kind == navKindProject && e.projID == "github.com/org/alpha" {
			m.nav.cursor = i
		}
	}
	m.refreshDetail(false)
	v := m.vp.View()

	for _, want := range []string{
		"github.com/org/alpha", // origin
		"Clones",               // aggregate row
		"Worktrees",
		"Branches",
		"Sessions",
		"Live",
		"CLONES",
		"/repos",
		"/conductor/repos",
		"4 worktrees · 3 branches · 2 sessions · 2 live", // clone1 summary
		"LIVE AGENTS",
		"refactor config loader tests", // live agent across the project
		"worktrees-9f",                 // live agent in clone2
	} {
		if !strings.Contains(v, want) {
			t.Errorf("project detail missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "beta") {
		t.Errorf("project detail should not mention beta:\n%s", v)
	}
}

// TestNavRealHome sweeps the real home and asserts the nav tree groups clones
// into projects, WT rows carry the WT tag, and widget collapses to one project
// when both ~/repos and ~/conductor/repos exist.
func TestNavRealHome(t *testing.T) {
	if _, err := os.UserHomeDir(); err != nil {
		t.Skip("no home")
	}
	cfg := config.Default()
	data, _ := gitx.ScanAll(context.Background(), cfg)
	if len(data) == 0 {
		t.Skip("no repos from whole-home sweep")
	}
	repoDirs := gitx.AllDirs(data)
	m := appModel{
		cfg: cfg, nav: newNavTreeModel(navWidth), data: data,
		sessions: map[string][]agents.Session{},
		live:     agents.AttributeLive(agents.LiveAgents(context.Background()), repoDirs),
	}
	m.rebuildNav()
	m.nav.collapsed = map[string]bool{}
	m.rebuildNav()

	projects, clones, wtRows, liveRows := 0, 0, 0, 0
	for _, e := range m.nav.entries {
		switch e.kind {
		case navKindProject:
			projects++
		case navKindClone:
			clones++
		case navKindWorktree:
			wtRows++
			if !e.isWT {
				t.Errorf("worktree row missing WT flag: %+v", e)
			}
		case navKindLiveAgent:
			liveRows++
		}
	}
	if projects == 0 {
		t.Fatal("expected projects from whole-home sweep")
	}
	if wtRows > 0 && !strings.Contains(m.nav.View(), "WT") {
		t.Error("nav view should render WT tag for worktree rows")
	}
	t.Logf("projects=%d clones=%d worktreeRows=%d liveAgentRows=%d", projects, clones, wtRows, liveRows)
}
