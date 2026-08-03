package gitx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const gitTimeout = 5 * time.Second

// runGit executes a git command with a timeout and returns trimmed stdout.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("git %s: timed out", strings.Join(args, " "))
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			stderr := strings.TrimSpace(string(ee.Stderr))
			if stderr != "" {
				return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), stderr)
			}
		}
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// canonicalPath resolves symlinks; on any failure it returns p unchanged.
func canonicalPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// enrichWorktree gathers per-worktree git state individually (branch, upstream
// tracking, dirty, head, last commit) using probes run inside the worktree
// itself. This is deliberate: the sweep is the source of truth for existence,
// and state is read per-worktree so unknown apps' worktrees get full detail
// even if git's administrative registry is stale. Best-effort; failures leave
// fields zero-valued (the caller can fall back to the registry).
func enrichWorktree(ctx context.Context, wt *Worktree) {
	if wt.Bare {
		return
	}
	if st, err := runGit(ctx, wt.Path, "status", "--short", "--branch"); err == nil {
		parseStatusBranch(st, wt)
	}
	if wt.ShortSHA == "" {
		if sha, err := runGit(ctx, wt.Path, "rev-parse", "--short", "HEAD"); err == nil {
			wt.ShortSHA = sha
		}
	}
	if lg, err := runGit(ctx, wt.Path, "log", "-1", "--format=%s|%cr"); err == nil {
		if idx := strings.LastIndex(lg, "|"); idx >= 0 {
			wt.LastSubject = lg[:idx]
			wt.LastDate = lg[idx+1:]
		} else {
			wt.LastSubject = lg
		}
	}
}

// parseStatusBranch parses `git status --short --branch`:
//
//	## main...origin/main [ahead 2, behind 1]
//	## HEAD (no branch)
//	## feat/x
func parseStatusBranch(st string, wt *Worktree) {
	lines := strings.Split(st, "\n")
	if len(lines) == 0 {
		return
	}
	first := strings.TrimPrefix(lines[0], "## ")
	if first == "" {
		return
	}
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) != "" {
			wt.Dirty = true
			break
		}
	}
	if strings.HasPrefix(first, "HEAD (no branch)") {
		wt.Detached = true
		wt.Branch = ""
		return
	}
	rest := first
	if i := strings.Index(rest, " ["); i >= 0 {
		parseTrack(rest[i+1:], wt)
		rest = rest[:i]
	}
	if i := strings.Index(rest, "..."); i >= 0 {
		wt.Branch = rest[:i]
		wt.HasUpstream = true
		return
	}
	wt.Branch = rest
}

// parseTrack parses the bracketed "[ahead N, behind M]" tracking section.
func parseTrack(s string, wt *Worktree) {
	s = strings.Trim(s, "[]")
	for _, part := range strings.Split(s, ",") {
		f := strings.Fields(part)
		if len(f) != 2 {
			continue
		}
		switch f[0] {
		case "ahead":
			wt.Ahead, _ = strconv.Atoi(f[1])
		case "behind":
			wt.Behind, _ = strconv.Atoi(f[1])
		}
	}
}

// registryEntry is one entry of `git worktree list --porcelain`, used only as
// supplementary enrichment (locked/prunable, and branch/head fallback).
type registryEntry struct {
	locked   bool
	prunable bool
	branch   string
	head     string
}

// worktreeRegistry parses `git worktree list --porcelain` for a repo,
// keyed by canonical worktree path. Returns nil when git fails.
func worktreeRegistry(ctx context.Context, repoDir string) map[string]registryEntry {
	out, err := runGit(ctx, repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	reg := make(map[string]registryEntry)
	var cur *registryEntry
	var curPath string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case line == "":
			if cur != nil && curPath != "" {
				reg[canonicalPath(curPath)] = *cur
			}
			cur = nil
			curPath = ""
		case strings.HasPrefix(line, "worktree "):
			cur = &registryEntry{}
			curPath = strings.TrimPrefix(line, "worktree ")
		case cur == nil:
		case strings.HasPrefix(line, "branch "):
			cur.branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case strings.HasPrefix(line, "HEAD "):
			cur.head = strings.TrimPrefix(line, "HEAD ")
		case line == "locked":
			cur.locked = true
		case line == "prunable":
			cur.prunable = true
		}
	}
	if cur != nil && curPath != "" {
		reg[canonicalPath(curPath)] = *cur
	}
	return reg
}

// applyRegistry merges supplementary registry info into a worktree, falling
// back to registry branch/head when per-worktree enrichment produced nothing.
func applyRegistry(wt *Worktree, reg map[string]registryEntry) {
	if len(reg) == 0 {
		return
	}
	e, ok := reg[canonicalPath(wt.Path)]
	if !ok {
		return
	}
	wt.Locked = e.locked
	wt.Prunable = e.prunable
	if wt.Branch == "" && e.branch != "" {
		wt.Branch = e.branch
	}
	if wt.ShortSHA == "" && len(e.head) >= 8 {
		wt.ShortSHA = e.head[:8]
	} else if wt.ShortSHA == "" && e.head != "" {
		wt.ShortSHA = e.head
	}
	if wt.Branch == "" && wt.ShortSHA == "" && e.head != "" {
		wt.Detached = true
	}
}

// repoGitDir picks the directory to run repo-level git commands (branches,
// worktree registry) in: the main checkout when present, else the common dir
// (bare repos, or a main checkout that couldn't be probed).
func repoGitDir(repo RepoInfo) string {
	if repo.Path != "" {
		if _, err := os.Stat(repo.Path); err == nil {
			return repo.Path
		}
	}
	return repo.CommonDir
}
