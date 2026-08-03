package gitx

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tder311/agent-tui/internal/config"
)

// maxConcurrentRepos bounds parallel per-repo scanning.
const maxConcurrentRepos = 8

// ScanAll is the whole discovery pipeline. It is app-agnostic:
//  1. sweep scan roots for .git markers (source of truth for existence),
//  2. resolve each marker to its repo's common dir (grouping key), skipping
//     submodules,
//  3. group by common dir into repos (main checkout + linked worktrees),
//  4. enrich each worktree individually (per-worktree git probes, with
//     `git worktree list` only as supplementary locked/prunable info),
//  5. list branches per repo.
//
// Never fails: unreadable roots and broken repos contribute nothing (their
// RepoData.Err is set); scan statistics are returned for diagnostics.
func ScanAll(ctx context.Context, cfg *config.Config) ([]RepoData, ScanStats) {
	stats := ScanStats{}
	start := time.Now()

	reg, _ := LoadOrchestratorRegistry()
	spawners := EffectiveSpawners(cfg, reg)
	roots := cfg.ScanRootsExpanded()

	markers, dirs := SweepWorktrees(ctx, roots, cfg.Skip)
	stats.Markers = len(markers)
	stats.DirsVisited = dirs
	stats.Sweep = time.Since(start)

	t0 := time.Now()
	groups := resolveGroups(ctx, markers, &stats)
	stats.Resolve = time.Since(t0)

	repos := scanRepos(ctx, groups, spawners)
	sort.Slice(repos, func(i, j int) bool {
		return repos[i].Repo.Path < repos[j].Repo.Path
	})
	stats.Repos = len(repos)
	for _, rd := range repos {
		stats.Worktrees += max(0, len(rd.Worktrees)-1)
		if rd.Err != nil && stats.Error == nil {
			stats.Error = rd.Err
		}
	}
	stats.Enrich = time.Since(t0)
	stats.Total = time.Since(start)
	return repos, stats
}

// group is one repo, keyed by its canonical git common dir.
type group struct {
	common        string   // canonical git common dir (grouping key)
	main          string   // main checkout path (== common dir for bare repos)
	bare          bool     // no working tree (e.g. a bare clone)
	linked        []string // linked worktree paths
	hasMainMarker bool     // the main checkout's .git dir was swept
}

// resolveGroups turns raw sweep markers into per-repo groups, skipping
// submodules and resolving each marker to its repo's common dir.
func resolveGroups(ctx context.Context, markers []gitMarker, stats *ScanStats) []group {
	byCommon := make(map[string]*group)
	var order []string

	ensure := func(common string) *group {
		g, ok := byCommon[common]
		if !ok {
			g = &group{common: common}
			byCommon[common] = g
			order = append(order, common)
		}
		return g
	}

	for _, m := range markers {
		common, submodule := resolveCommonDir(ctx, m)
		if submodule {
			stats.Submodules++
			continue
		}
		if common == "" {
			continue
		}
		common = canonicalPath(common)
		g := ensure(common)
		if !m.gitFile {
			g.hasMainMarker = true
			g.main = m.checkout
			continue
		}
		g.linked = append(g.linked, m.checkout)
	}

	// Derive the main checkout / bare-ness from the common dir when the main
	// checkout wasn't swept (excluded by skip list, or the repo is bare).
	for _, common := range order {
		g := byCommon[common]
		if g.hasMainMarker {
			continue
		}
		if filepath.Base(common) == ".git" {
			g.main = filepath.Dir(common)
		} else {
			g.bare = true
			g.main = common
		}
	}

	groups := make([]group, 0, len(order))
	for _, common := range order {
		groups = append(groups, *byCommon[common])
	}
	return groups
}

// resolveCommonDir maps a marker to its repo's common dir. Returns
// (common, true) for submodules (which also carry a .git file) so the caller
// can skip them. The cheap path resolves the .git-file gitdir directly;
// unusual layouts fall back to `git rev-parse`.
func resolveCommonDir(ctx context.Context, m gitMarker) (string, bool) {
	if !m.gitFile {
		// A .git directory marks a real main checkout — never a submodule.
		return filepath.Join(m.checkout, ".git"), false
	}
	if strings.Contains(m.gitDir, "/.git/modules/") {
		return "", true // submodule marker
	}
	if i := strings.Index(m.gitDir, "/.git/worktrees/"); i >= 0 {
		return m.gitDir[:i+len("/.git")], false
	}
	// Unusual layout: ask git.
	if super, err := runGit(ctx, m.checkout, "rev-parse", "--show-superproject-working-tree"); err == nil && strings.TrimSpace(super) != "" {
		return "", true // submodule
	}
	common, err := runGit(ctx, m.checkout, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", false
	}
	return common, false
}

// scanRepos builds RepoData for every group in parallel.
func scanRepos(ctx context.Context, groups []group, spawners map[string]config.Spawner) []RepoData {
	data := make([]RepoData, len(groups))
	sem := make(chan struct{}, maxConcurrentRepos)
	var wg sync.WaitGroup
	for i, g := range groups {
		wg.Add(1)
		go func(i int, g group) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			data[i] = scanRepo(ctx, g, spawners)
		}(i, g)
	}
	wg.Wait()
	return data
}

func scanRepo(ctx context.Context, g group, spawners map[string]config.Spawner) RepoData {
	rd := RepoData{}
	rd.Repo = RepoInfo{
		Key:       filepath.Base(g.main),
		Path:      g.main,
		CommonDir: g.common,
		Bare:      g.bare,
	}

	gitDir := repoGitDir(rd.Repo)
	rd.Repo.Origin = fetchOrigin(ctx, gitDir, rd.Repo.Path)

	if !g.bare {
		rd.Worktrees = append(rd.Worktrees, Worktree{Path: g.main, Main: true, Kind: KindMain})
	}
	for _, p := range g.linked {
		name, tool := classifySpawner(p, false, spawners)
		rd.Worktrees = append(rd.Worktrees, Worktree{Path: p, Kind: KindOther, Spawner: name, Tool: tool})
	}
	sort.Slice(rd.Worktrees, func(i, j int) bool { return rd.Worktrees[i].Path < rd.Worktrees[j].Path })

	reg := worktreeRegistry(ctx, gitDir)

	for i := range rd.Worktrees {
		enrichWorktree(ctx, &rd.Worktrees[i])
		applyRegistry(&rd.Worktrees[i], reg)
	}

	if branches, err := ListBranches(ctx, gitDir); err != nil {
		rd.Err = err
	} else {
		rd.Branches = branches
	}
	return rd
}

// fetchOrigin reads remote.origin.url and normalizes it into the repo's project
// identity. Repos without a (parseable) origin fall back to a unique local
// identity so they still appear, flagged as local.
func fetchOrigin(ctx context.Context, gitDir, repoPath string) Origin {
	if url, err := runGit(ctx, gitDir, "remote", "get-url", "origin"); err == nil && url != "" {
		if id, host, slug, remote, ok := NormalizeOrigin(url); ok {
			return Origin{HasRemote: remote, Identity: id, Slug: slug, Host: host}
		}
	}
	return localIdentity(repoPath)
}

// AllDirs returns every directory associated with the repos (main checkouts
// plus all worktree paths) — used to match agent sessions to repos.
func AllDirs(data []RepoData) map[string][]string {
	out := make(map[string][]string, len(data))
	for _, rd := range data {
		dirs := []string{rd.Repo.Path}
		for _, wt := range rd.Worktrees {
			if wt.Path != rd.Repo.Path {
				dirs = append(dirs, wt.Path)
			}
		}
		out[rd.Repo.Path] = dirs
	}
	return out
}
