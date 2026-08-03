package agents

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestClaudeSlug(t *testing.T) {
	cases := map[string]string{
		"/Users/dev/repos/widget":                           "-Users-dev-repos-widget",
		"/Users/dev/repos/widget/.claude/worktrees/abc":     "-Users-dev-repos-widget--claude-worktrees-abc",
		"/Users/dev/worktrees/railway-tui/railway-tui-plan": "-Users-dev-worktrees-railway-tui-railway-tui-plan",
		"/Users/dev/conductor/repos/inventory_service_api":  "-Users-dev-conductor-repos-inventory-service-api",
		"/Users/dev/repos/widget/.claude/worktrees/fix+api+auth-x": "-Users-dev-repos-widget--claude-worktrees-fix-api-auth-x",
	}
	for in, want := range cases {
		if got := claudeSlug(in); got != want {
			t.Errorf("claudeSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchRepo(t *testing.T) {
	repoDirs := map[string][]string{
		"/repos/widget": {
			"/repos/widget",
			"/repos/widget/.claude/worktrees/agent-1",
		},
		"/repos/other": {"/repos/other"},
	}
	cases := map[string]string{
		"/repos/widget":                              "/repos/widget",
		"/repos/widget/.claude/worktrees/agent-1":    "/repos/widget",
		"/repos/widget/sub/dir":                      "/repos/widget",
		"/repos/widget2":                             "", // prefix but not within
		"/repos/other":                               "/repos/other",
		"/elsewhere":                                 "",
		"":                                           "",
	}
	for dir, want := range cases {
		if got := matchRepo(dir, repoDirs); got != want {
			t.Errorf("matchRepo(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestClaudeSessions(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	root := filepath.Join(home, ".claude", "projects")
	if _, err := os.Stat(root); err != nil {
		t.Skip("no claude projects dir")
	}

	// Point discovery at a fake repo whose dir slug we create by symlink-free
	// means: just verify the scan of a known-real dir works if any exist.
	dirs, err := os.ReadDir(root)
	if err != nil || len(dirs) == 0 {
		t.Skip("no claude projects")
	}

	// Use the temp-dir trick: create a fake project dir for a fake repo.
	fakeRepo := t.TempDir()
	projDir := filepath.Join(root, claudeSlug(fakeRepo))
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(projDir)
	if err := os.WriteFile(filepath.Join(projDir, "sess-1.jsonl"), []byte("{}\n{}\n{}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	repoDirs := map[string][]string{fakeRepo: {fakeRepo}}
	sessions := claudeSessions(repoDirs, Options{})
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.Tool != "claude" || s.ID != "sess-1" || s.Messages != 3 || s.Dir != fakeRepo {
		t.Errorf("session wrong: %+v", s)
	}
}

// TestConductorWorkspaceSessions verifies that Claude sessions whose cwd was
// a Conductor workspace attribute to the conductor repo, both at the slug
// resolution level and through the full Discover pipeline.
func TestConductorWorkspaceSessions(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	projectsRoot := filepath.Join(home, ".claude", "projects")
	if _, err := os.Stat(projectsRoot); err != nil {
		t.Skip("no claude projects dir")
	}

	repo := t.TempDir() + "/conductor/repos/widget"
	workspace := t.TempDir() + "/conductor/workspaces/widget/cape-town"

	// Slug for the workspace must match the real-world pattern, e.g.
	// -Users-jappleseed-conductor-workspaces-widget-cape-town.
	if got := claudeSlug("/Users/jappleseed/conductor/workspaces/widget/cape-town"); got != "-Users-jappleseed-conductor-workspaces-widget-cape-town" {
		t.Fatalf("conductor slug mismatch: %q", got)
	}

	// Create a fake project dir + session file for the fake workspace.
	slug := claudeSlug(workspace)
	projDir := filepath.Join(projectsRoot, slug)
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(projDir)
	if err := os.WriteFile(filepath.Join(projDir, "cond-sess.jsonl"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// A stale workspace (deleted dir) whose slug still matches the real-world
	// -Users-jappleseed-conductor-workspaces-widget-abuja pattern.
	staleWorkspace := "/Users/jappleseed/conductor/workspaces/widget/abuja"
	staleSlug := claudeSlug(staleWorkspace)
	staleProj := filepath.Join(projectsRoot, staleSlug)
	if err := os.MkdirAll(staleProj, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(staleProj)
	if err := os.WriteFile(filepath.Join(staleProj, "stale-sess.jsonl"), []byte("{}\n{}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Slug resolution: workspace dir must be the exact match target.
	known := []knownDir{
		{slug: claudeSlug(repo), dir: repo},
		{slug: claudeSlug(workspace), dir: workspace},
	}
	sort.Slice(known, func(i, j int) bool { return len(known[i].slug) > len(known[j].slug) })
	if got := resolveSlug(slug, known); got != workspace {
		t.Errorf("resolveSlug = %q, want %q", got, workspace)
	}

	// Full pipeline: sessions attribute to the conductor repo.
	repoDirs := map[string][]string{
		repo: {repo, workspace},
	}
	sessions := claudeSessions(repoDirs, Options{})
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d: %+v", len(sessions), sessions)
	}
	if sessions[0].Dir != workspace || sessions[0].ID != "cond-sess" {
		t.Errorf("session wrong: %+v", sessions[0])
	}

	byRepo := Discover(repoDirs)
	if len(byRepo[repo]) != 1 {
		t.Errorf("Discover should attribute to conductor repo, got %+v", byRepo)
	}
}

// TestConductorStaleWorkspaceFallback verifies that sessions for a deleted
// Conductor workspace fall back to the virtual workspace-root dir and
// attribute to the owning conductor clone.
func TestConductorStaleWorkspaceFallback(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	projectsRoot := filepath.Join(home, ".claude", "projects")
	if _, err := os.Stat(projectsRoot); err != nil {
		t.Skip("no claude projects dir")
	}

	repo := "/Users/jappleseed/conductor/repos/widget"
	// The virtual workspace-root dir withConductorVirtualDirs would add.
	virtual := "/Users/jappleseed/conductor/workspaces/widget"
	staleSlug := "-Users-jappleseed-conductor-workspaces-widget-abuja"

	staleProj := filepath.Join(projectsRoot, staleSlug)
	if err := os.MkdirAll(staleProj, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(staleProj)
	if err := os.WriteFile(filepath.Join(staleProj, "stale-sess.jsonl"), []byte("{}\n{}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// The virtual dir participates in slug resolution: longest prefix wins.
	known := []knownDir{
		{slug: claudeSlug(repo), dir: repo},
		{slug: claudeSlug(virtual), dir: virtual},
	}
	sort.Slice(known, func(i, j int) bool { return len(known[i].slug) > len(known[j].slug) })
	if got := resolveSlug(staleSlug, known); got != virtual {
		t.Errorf("resolveSlug(stale) = %q, want %q", got, virtual)
	}

	// Full pipeline attributes the stale session to the conductor clone.
	repoDirs := map[string][]string{repo: {repo, virtual}}
	sessions := claudeSessions(repoDirs, Options{})
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d: %+v", len(sessions), sessions)
	}
	if sessions[0].Dir != virtual || sessions[0].ID != "stale-sess" {
		t.Errorf("session wrong: %+v", sessions[0])
	}
	byRepo := Discover(repoDirs)
	if len(byRepo[repo]) != 1 || byRepo[repo][0].ID != "stale-sess" {
		t.Errorf("Discover should attribute stale session to conductor clone, got %+v", byRepo)
	}
}

// TestWithConductorVirtualDirs verifies the real ~/conductor layout augments
// conductor clones with a virtual workspace-root dir.
func TestWithConductorVirtualDirs(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	wsRoot := filepath.Join(home, "conductor", "workspaces")
	if _, err := os.Stat(wsRoot); err != nil {
		t.Skip("no conductor workspaces dir")
	}

	repo := filepath.Join(home, "conductor", "repos", "widget")
	repoDirs := map[string][]string{
		repo:                                    {repo},
		filepath.Join(home, "repos", "widget"):  {filepath.Join(home, "repos", "widget")},
	}
	aug := withConductorVirtualDirs(repoDirs)

	wantVirtual := filepath.Join(home, "conductor", "workspaces", "widget")
	found := false
	for _, d := range aug[repo] {
		if d == wantVirtual {
			found = true
		}
	}
	if !found {
		t.Errorf("conductor clone %q missing virtual dir %q; got %v", repo, wantVirtual, aug[repo])
	}
	// Non-conductor repos must be untouched.
	if len(aug[filepath.Join(home, "repos", "widget")]) != 1 {
		t.Errorf("non-conductor repo was augmented: %v", aug[filepath.Join(home, "repos", "widget")])
	}
}

// TestOpenCodeSessionsSmoke reads the real opencode db when present.
func TestOpenCodeSessionsSmoke(t *testing.T) {
	if _, err := os.Stat(opencodeDBPath()); err != nil {
		t.Skip("no opencode db")
	}
	sessions := opencodeSessions()
	t.Logf("found %d opencode sessions", len(sessions))
	for _, s := range sessions[:min(5, len(sessions))] {
		t.Logf("  %s %q dir=%s msgs=%d updated=%s", s.ID, s.Title, s.Dir, s.Messages, s.Updated)
	}
	if len(sessions) == 0 {
		t.Error("expected sessions from real db")
	}
	for _, s := range sessions {
		if s.ID == "" || s.Dir == "" {
			t.Errorf("session missing fields: %+v", s)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
