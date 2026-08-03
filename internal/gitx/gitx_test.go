package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tder311/agent-tui/internal/config"
)

// setupRepo creates a temp git repo with one commit on main. The returned
// path is canonical (symlinks resolved, matching git's own output).
func setupRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	// Mimic Claude Code's own exclusions so .claude/worktrees doesn't dirty main.
	if err := os.MkdirAll(filepath.Join(dir, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), []byte("**/.claude/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "init")
	return dir
}

func canonical(t *testing.T, p string) string {
	t.Helper()
	c, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// moveInto renames a setupRepo temp dir into dest. macOS os.Rename refuses to
// replace an existing (even empty) directory, so the leaf dir is not pre-made.
func moveInto(t *testing.T, src, dest string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(src, dest); err != nil {
		t.Fatalf("move %s to %s: %v", src, dest, err)
	}
}

// addWorktree creates a linked worktree of repo at wtPath on a new branch
// (or detached when branch is "").
func addWorktree(t *testing.T, repo, wtPath, branch string) {
	t.Helper()
	args := []string{"-C", repo, "worktree", "add"}
	if branch != "" {
		args = append(args, "-b", branch)
	} else {
		args = append(args, "--detach")
	}
	args = append(args, wtPath)
	cmd := exec.Command("git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worktree add %s: %v\n%s", wtPath, err, out)
	}
}

// setOrigin adds an origin remote so a repo gets a project identity.
func setOrigin(t *testing.T, repo, url string) {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "remote", "add", "origin", url)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("remote add %s: %v\n%s", url, err, out)
	}
}

// gitxOrigin builds the expected remote Origin for an assertion.
func gitxOrigin(identity, slug, host string) Origin {
	return Origin{HasRemote: true, Identity: identity, Slug: slug, Host: host}
}

// homeTree builds a fake home with one repo (monodo) whose worktrees live in
// several "app" locations plus a second repo (other) with an unknown worktree
// and a real submodule, a second clone of monodo under a different URL form,
// and a no-remote repo. Returns the root path.
func homeTree(t *testing.T) string {
	t.Helper()
	root := canonical(t, t.TempDir())

	monodo := filepath.Join(root, "repos", "monodo")
	repo := setupRepo(t)
	moveInto(t, repo, monodo)
	setOrigin(t, monodo, "git@github.com:modoenergy/monodo.git")

	addWorktree(t, monodo, filepath.Join(monodo, ".claude", "worktrees", "agent-1"), "feat/x")
	addWorktree(t, monodo, filepath.Join(root, "worktrees", "monodo", "oc-1"), "oc/branch")
	addWorktree(t, monodo, filepath.Join(root, "conductor", "workspaces", "monodo", "cape-town"), "cond/cape")

	// Second clone of the same upstream project, using a different URL form
	// (ssh://) that must normalize to the same identity.
	monodoClone := filepath.Join(root, "conductor", "repos", "monodo")
	repo4 := setupRepo(t)
	moveInto(t, repo4, monodoClone)
	setOrigin(t, monodoClone, "ssh://git@github.com/modoenergy/monodo")

	other := filepath.Join(root, "repos", "other")
	repo2 := setupRepo(t)
	moveInto(t, repo2, other)
	setOrigin(t, other, "https://github.com/modoenergy/other.git")
	addWorktree(t, other, filepath.Join(root, "mystery", "app", "wt-1"), "mystery/x")

	// A repo with no remote: becomes a local project.
	scratch := filepath.Join(root, "scratch")
	repo5 := setupRepo(t)
	moveInto(t, repo5, scratch)

	// Real submodule under "other" — must be skipped by discovery.
	subrepo := setupRepo(t)
	cmd := exec.Command("git", "-C", other, "-c", "protocol.file.allow=always", "submodule", "add", subrepo, "sub")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("submodule add: %v\n%s", err, out)
	}

	// A repo under a skipped dir (node_modules) that must NOT be found.
	skipped := filepath.Join(root, "apps", "web", "node_modules", "deep")
	repo3 := setupRepo(t)
	moveInto(t, repo3, skipped)

	return root
}

func sweepConfig(root string) *config.Config {
	return &config.Config{
		ScanRoots: []string{root},
		Skip:      []string{"node_modules"},
		Spawners: map[string]config.Spawner{
			"claude":    {Pattern: "/.claude/worktrees/", CLI: "claude", Color: "orange"},
			"conductor": {Pattern: "/conductor/workspaces/", CLI: "claude", Color: "purple"},
			"opencode":  {Pattern: "/worktrees/", CLI: "opencode", Color: "green"},
		},
	}
}

func TestSweepWorktrees(t *testing.T) {
	root := homeTree(t)
	markers, dirs := SweepWorktrees(context.Background(), []string{root}, []string{"node_modules"})
	if dirs == 0 {
		t.Fatal("expected dirs visited")
	}

	paths := map[string]bool{}
	for _, m := range markers {
		paths[m.checkout] = true
	}
	// Main checkouts (monodo + other) and all linked worktrees found.
	for _, want := range []string{
		filepath.Join(root, "repos", "monodo"),
		filepath.Join(root, "repos", "other"),
		filepath.Join(root, "repos", "monodo", ".claude", "worktrees", "agent-1"),
		filepath.Join(root, "worktrees", "monodo", "oc-1"),
		filepath.Join(root, "conductor", "workspaces", "monodo", "cape-town"),
		filepath.Join(root, "mystery", "app", "wt-1"),
	} {
		if !paths[want] {
			t.Errorf("sweep missed %s", want)
		}
	}
	// The submodule marker is a .git FILE whose gitdir points into modules/.
	// (Submodule filtering happens in resolveGroups — TestScanAllHermetic
	// asserts stats.Submodules — so here we only check the marker shape.)
	subGit := filepath.Join(root, "repos", "other", "sub", ".git")
	gd, ok := parseGitFile(subGit)
	if !ok {
		t.Fatalf("submodule .git should parse")
	}
	if !strings.Contains(gd, "/.git/modules/") {
		t.Errorf("submodule gitdir = %q, want /modules/ layout", gd)
	}
	// The node_modules repo must be skipped entirely.
	if paths[filepath.Join(root, "apps", "web", "node_modules", "deep")] {
		t.Error("node_modules repo should have been skipped")
	}
}

func TestSkipMatcher(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	m := newSkipMatcher([]string{"~/Library", "node_modules", ".git", "~/code/*"})
	if !m.skip("Library", filepath.Join(home, "Library")) {
		t.Error("~/Library should match")
	}
	if m.skip("Library", filepath.Join(home, "repos", "Library")) {
		t.Error("path pattern must not match nested Library")
	}
	if !m.skip("node_modules", filepath.Join(home, "a", "node_modules")) {
		t.Error("node_modules basename should match at any depth")
	}
	if !m.skip(".git", "/x/.git") {
		t.Error(".git basename should match")
	}
	if !m.skip("foo", filepath.Join(home, "code", "foo")) {
		t.Error("~/code/* should match children")
	}
}

func TestScanAllHermetic(t *testing.T) {
	root := homeTree(t)
	cfg := sweepConfig(root)
	cfg.Spawners = map[string]config.Spawner{
		"claude":    {Pattern: "/.claude/worktrees/", CLI: "claude", Color: "orange"},
		"conductor": {Pattern: "/conductor/workspaces/", CLI: "claude", Color: "purple"},
		"opencode":  {Pattern: filepath.Join(root, "worktrees") + "/", CLI: "opencode", Color: "green"},
	}
	data, stats := ScanAll(context.Background(), cfg)
	if len(data) != 4 {
		t.Fatalf("expected 4 repos (monodo, monodo clone, other, scratch), got %d", len(data))
	}
	if stats.Submodules != 1 {
		t.Errorf("expected 1 submodule skipped, got %d", stats.Submodules)
	}
	if stats.Markers < 8 {
		t.Errorf("expected >=8 markers, got %d", stats.Markers)
	}

	var monodo, monodoClone, other, scratch *RepoData
	for i := range data {
		switch data[i].Repo.Path {
		case filepath.Join(root, "repos", "monodo"):
			monodo = &data[i]
		case filepath.Join(root, "conductor", "repos", "monodo"):
			monodoClone = &data[i]
		case filepath.Join(root, "repos", "other"):
			other = &data[i]
		case filepath.Join(root, "scratch"):
			scratch = &data[i]
		}
	}
	if monodo == nil || monodoClone == nil || other == nil || scratch == nil {
		t.Fatalf("missing repos: %+v", data)
	}

	// Origins: the two monodo clones use different URL forms but must share
	// one normalized project identity; other gets its own; scratch has none.
	want := gitxOrigin("github.com/modoenergy/monodo", "monodo", "github.com")
	if monodo.Repo.Origin != want {
		t.Errorf("monodo origin = %+v, want %+v", monodo.Repo.Origin, want)
	}
	if monodoClone.Repo.Origin != want {
		t.Errorf("monodo clone origin = %+v, want %+v", monodoClone.Repo.Origin, want)
	}
	if other.Repo.Origin != gitxOrigin("github.com/modoenergy/other", "other", "github.com") {
		t.Errorf("other origin = %+v", other.Repo.Origin)
	}
	if scratch.Repo.Origin.HasRemote || !strings.HasPrefix(scratch.Repo.Origin.Identity, "local:") {
		t.Errorf("scratch origin = %+v, want local identity", scratch.Repo.Origin)
	}
	identities := map[string]bool{}
	for _, rd := range data {
		identities[rd.Repo.Origin.Identity] = true
	}
	if len(identities) != 3 {
		t.Errorf("expected 3 distinct project identities, got %d: %v", len(identities), identities)
	}

	// monodo: 1 main + 3 linked worktrees.
	if len(monodo.Worktrees) != 4 {
		t.Fatalf("monodo worktrees = %d, want 4: %+v", len(monodo.Worktrees), monodo.Worktrees)
	}
	byPath := map[string]Worktree{}
	for _, wt := range monodo.Worktrees {
		byPath[wt.Path] = wt
	}
	if !byPath[monodo.Repo.Path].Main {
		t.Errorf("monodo main worktree not marked Main")
	}
	claude := byPath[filepath.Join(root, "repos", "monodo", ".claude", "worktrees", "agent-1")]
	if claude.Spawner != "claude" || claude.Tool != "claude" || claude.Kind != KindOther {
		t.Errorf("claude worktree wrong: %+v", claude)
	}
	oc := byPath[filepath.Join(root, "worktrees", "monodo", "oc-1")]
	if oc.Spawner != "opencode" || oc.Tool != "opencode" {
		t.Errorf("opencode worktree wrong: %+v", oc)
	}
	cond := byPath[filepath.Join(root, "conductor", "workspaces", "monodo", "cape-town")]
	if cond.Spawner != "conductor" || cond.Tool != "claude" {
		t.Errorf("conductor worktree wrong: %+v", cond)
	}
	if claude.Branch != "feat/x" || oc.Branch != "oc/branch" || cond.Branch != "cond/cape" {
		t.Errorf("worktree branches not enriched: %+v %+v %+v", claude, oc, cond)
	}

	// other: main + 1 unknown worktree (spawner "").
	if len(other.Worktrees) != 2 {
		t.Fatalf("other worktrees = %d, want 2: %+v", len(other.Worktrees), other.Worktrees)
	}
	var unknown *Worktree
	for i := range other.Worktrees {
		if filepath.Base(other.Worktrees[i].Path) == "wt-1" {
			unknown = &other.Worktrees[i]
		}
	}
	if unknown == nil {
		t.Fatal("unknown worktree missing")
	}
	if unknown.Spawner != "" || unknown.Tool != "" || unknown.Kind != KindOther {
		t.Errorf("unknown worktree should be neutral: %+v", unknown)
	}
	if unknown.Branch != "mystery/x" {
		t.Errorf("unknown worktree branch not enriched: %+v", unknown)
	}

	// Branches grouped under their parent repo.
	if len(monodo.Branches) < 4 {
		t.Errorf("monodo should have >=4 branches, got %d", len(monodo.Branches))
	}

	// monodoClone: only the main checkout (its worktrees live elsewhere).
	if len(monodoClone.Worktrees) != 1 || !monodoClone.Worktrees[0].Main {
		t.Errorf("monodo clone worktrees = %+v, want 1 main", monodoClone.Worktrees)
	}
}

func TestScanAllDirtyAndBare(t *testing.T) {
	root := canonical(t, t.TempDir())
	repo := filepath.Join(root, "repos", "dirtyrepo")
	r := setupRepo(t)
	moveInto(t, r, repo)
	wt := filepath.Join(root, "worktrees", "dirtyrepo", "wt-1")
	addWorktree(t, repo, wt, "dirty/branch")
	if err := os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	// A bare clone with a linked worktree added via `git worktree add`.
	bare := filepath.Join(root, "bare", "shared.git")
	if err := os.MkdirAll(filepath.Dir(bare), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "clone", "--bare", repo, bare)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone --bare: %v\n%s", err, out)
	}
	addWorktree(t, bare, filepath.Join(root, "bare", "worktrees", "shared", "wt-bare"), "bare/branch")

	cfg := sweepConfig(root)
	data, _ := ScanAll(context.Background(), cfg)

	var dirty, bareRepo *RepoData
	for i := range data {
		switch filepath.Base(data[i].Repo.Path) {
		case "dirtyrepo":
			dirty = &data[i]
		case "shared.git":
			bareRepo = &data[i]
		}
	}
	if dirty == nil {
		t.Fatalf("dirtyrepo not found: %+v", data)
	}
	for _, wt := range dirty.Worktrees {
		if filepath.Base(wt.Path) == "wt-1" && !wt.Dirty {
			t.Errorf("dirty worktree not detected: %+v", wt)
		}
	}
	if bareRepo == nil {
		t.Fatalf("bare repo (shared.git) not found via its worktree markers: %+v", data)
	}
	if !bareRepo.Repo.Bare {
		t.Errorf("shared.git should be bare, got %+v", bareRepo.Repo)
	}
	if len(bareRepo.Worktrees) != 1 {
		t.Errorf("bare repo should have 1 linked worktree, got %+v", bareRepo.Worktrees)
	}
	if bareRepo.Worktrees[0].Branch != "bare/branch" {
		t.Errorf("bare worktree branch = %q, want bare/branch", bareRepo.Worktrees[0].Branch)
	}
}

func TestLocalRepoIdentities(t *testing.T) {
	root := canonical(t, t.TempDir())
	mk := func(rel string) string {
		p := filepath.Join(root, rel)
		r := setupRepo(t)
		moveInto(t, r, p)
		return p
	}
	mk("repos/monodo")
	mk("conductor/repos/monodo")

	cfg := sweepConfig(root)
	data, _ := ScanAll(context.Background(), cfg)
	if len(data) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(data))
	}
	for _, rd := range data {
		if rd.Repo.Origin.HasRemote {
			t.Errorf("no-origin repo should be local: %+v", rd.Repo.Origin)
		}
		if !strings.HasPrefix(rd.Repo.Origin.Identity, "local:") {
			t.Errorf("expected local: identity, got %q", rd.Repo.Origin.Identity)
		}
	}
	if data[0].Repo.Origin.Identity == data[1].Repo.Origin.Identity {
		t.Errorf("local identities must be unique per checkout: %v", data[0].Repo.Origin.Identity)
	}
}

func TestNormalizeOrigin(t *testing.T) {
	cases := []struct {
		raw      string
		identity string
		slug     string
		remote   bool
		ok       bool
	}{
		{"git@github.com:modoenergy/monodo.git", "github.com/modoenergy/monodo", "monodo", true, true},
		{"ssh://git@github.com/modoenergy/monodo", "github.com/modoenergy/monodo", "monodo", true, true},
		{"ssh://git@github.com:2222/modoenergy/monodo.git", "github.com/modoenergy/monodo", "monodo", true, true},
		{"https://github.com/modoenergy/monodo.git", "github.com/modoenergy/monodo", "monodo", true, true},
		{"http://github.com/modoenergy/monodo", "github.com/modoenergy/monodo", "monodo", true, true},
		{"git://github.com/modoenergy/monodo.git/", "github.com/modoenergy/monodo", "monodo", true, true},
		{"GIT@GITHUB.COM:ModoEnergy/Monodo.git", "github.com/modoenergy/monodo", "monodo", true, true},
		{"github.com:modoenergy/monodo.git", "github.com/modoenergy/monodo", "monodo", true, true},
		{"https://gitlab.com/group/sub/repo.git", "gitlab.com/group/sub/repo", "repo", true, true},
		{"/Users/tom/bare.git", "local:/Users/tom/bare.git", "bare.git", false, true},
		{"", "", "", false, false},
	}
	for _, c := range cases {
		id, host, slug, remote, ok := NormalizeOrigin(c.raw)
		if ok != c.ok || id != c.identity || slug != c.slug || remote != c.remote {
			t.Errorf("NormalizeOrigin(%q) = id %q, slug %q, remote %v, ok %v; want id %q, slug %q, remote %v, ok %v",
				c.raw, id, slug, remote, ok, c.identity, c.slug, c.remote, c.ok)
		}
		if ok && remote && host == "" {
			t.Errorf("remote origin should carry a host: %q", c.raw)
		}
	}
}

func TestActions(t *testing.T) {
	repo := setupRepo(t)
	ctx := context.Background()

	wtPath := filepath.Join(canonical(t, t.TempDir()), "wt-rm")
	addWorktree(t, repo, wtPath, "tmp/branch")

	// Dirty worktree: plain remove must report ErrDirty, force must succeed.
	if err := os.WriteFile(filepath.Join(wtPath, "dirty.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorktree(ctx, repo, wtPath, false); err != ErrDirty {
		t.Fatalf("expected ErrDirty, got %v", err)
	}
	if err := RemoveWorktree(ctx, repo, wtPath, true); err != nil {
		t.Fatalf("force remove: %v", err)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree dir should be gone")
	}

	// Unmerged branch: -d must report ErrUnmerged, -D must succeed.
	cmd := exec.Command("git", "-C", repo, "checkout", "-b", "diverge")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("git", "-C", repo, "add", ".")
	_ = cmd.Run()
	cmd = exec.Command("git", "-C", repo, "commit", "-m", "diverge")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "-C", repo, "checkout", "main")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout main: %v\n%s", err, out)
	}
	if err := DeleteBranch(ctx, repo, "diverge", false); err != ErrUnmerged {
		t.Fatalf("expected ErrUnmerged, got %v", err)
	}
	if err := DeleteBranch(ctx, repo, "diverge", true); err != nil {
		t.Fatalf("force delete: %v", err)
	}

	if err := PruneWorktrees(ctx, repo); err != nil {
		t.Fatalf("prune: %v", err)
	}
}

func TestWorktreeRegistryAndEnrichment(t *testing.T) {
	repo := setupRepo(t)
	wtPath := filepath.Join(canonical(t, t.TempDir()), "wt-reg")
	addWorktree(t, repo, wtPath, "reg/branch")

	reg := worktreeRegistry(context.Background(), repo)
	if len(reg) != 2 {
		t.Fatalf("registry entries = %d, want 2", len(reg))
	}
	if e, ok := reg[canonicalPath(wtPath)]; !ok || e.branch != "reg/branch" {
		t.Errorf("registry missing worktree entry: %+v", reg)
	}

	wt := Worktree{Path: wtPath, Kind: KindOther}
	enrichWorktree(context.Background(), &wt)
	if wt.Branch != "reg/branch" || wt.ShortSHA == "" || wt.Detached {
		t.Errorf("enrichment wrong: %+v", wt)
	}
	if !wt.Main { // not set by enrich; Main flag is caller's job
	}

	// parseStatusBranch detached form.
	wt2 := Worktree{}
	parseStatusBranch("## HEAD (no branch)\n?? x.txt\n", &wt2)
	if !wt2.Detached || wt2.Branch != "" || !wt2.Dirty {
		t.Errorf("detached parse wrong: %+v", wt2)
	}
	wt3 := Worktree{}
	parseStatusBranch("## feat/x...origin/feat/x [ahead 2, behind 1]", &wt3)
	if wt3.Branch != "feat/x" || !wt3.HasUpstream || wt3.Ahead != 2 || wt3.Behind != 1 {
		t.Errorf("track parse wrong: %+v", wt3)
	}
}

// TestScanAllSmoke sweeps the real home (~ by default) and asserts the sweep
// alone finds claude, conductor and opencode-worktreeRoot worktrees. This is
// the app-agnostic guarantee: no hardcoded app paths drive discovery.
func TestScanAllSmoke(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	cfg := config.Default()

	start := time.Now()
	data, stats := ScanAll(context.Background(), cfg)
	elapsed := time.Since(start)

	if len(data) == 0 {
		t.Fatal("expected repos from whole-home sweep")
	}
	t.Logf("sweep=%v resolve+enrich=%v total=%v dirs=%d markers=%d submodules=%d repos=%d",
		stats.Sweep, stats.Enrich, elapsed, stats.DirsVisited, stats.Markers, stats.Submodules, stats.Repos)

	claude, conductor, opencode, other := 0, 0, 0, 0
	projects := map[string]int{}
	for _, rd := range data {
		projects[rd.Repo.Origin.Identity]++
		for _, wt := range rd.Worktrees {
			switch wt.Spawner {
			case "claude":
				claude++
			case "conductor":
				conductor++
			case "opencode":
				opencode++
			default:
				if !wt.Main {
					other++
				}
			}
		}
		t.Logf("%s [%s]: %d worktrees, %d branches", rd.Repo.Path, rd.Repo.Origin.Identity, len(rd.Worktrees), len(rd.Branches))
	}
	t.Logf("projects=%d clones=%d claude=%d conductor=%d opencode=%d other=%d",
		len(projects), len(data), claude, conductor, opencode, other)

	// Assert the sweep alone finds each spawner class when the source dirs
	// exist on the machine (i.e. this is not a coverage regression).
	has := func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}
	if has(filepath.Join(home, "repos")) && claude == 0 {
		t.Error("claude worktrees should be found by the sweep")
	}
	if has(filepath.Join(home, "conductor", "workspaces")) && conductor == 0 {
		t.Error("conductor worktrees should be found by the sweep")
	}
	if has(filepath.Join(home, "worktrees")) && opencode == 0 {
		t.Error("opencode worktreeRoot worktrees should be found by the sweep")
	}
	if claude+conductor+opencode+other == 0 {
		t.Error("sweep found no linked worktrees at all")
	}
}
