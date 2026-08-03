package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/tder311/agent-tui/internal/config"
)

// gitMarker is one .git entry found by the sweep.
type gitMarker struct {
	checkout string // directory containing the .git entry
	gitFile  bool   // .git is a regular file (linked worktree or submodule)
	gitDir   string // parsed/resolved git dir for .git-file markers
}

// SweepWorktrees walks the scan roots looking for .git entries (the marker
// git uses for both a main checkout — a .git directory — and every linked
// worktree — a .git file whose contents are `gitdir: <path>`). This is the
// source of truth for worktree existence, regardless of which app created a
// worktree or whether git's administrative registry is consistent. Directories
// matched by a skip pattern are never descended into. Symlink loops and
// duplicate visits are prevented via a canonical-path visited set. Never
// fails: unreadable dirs are skipped. Returns the markers found and the number
// of directories visited.
func SweepWorktrees(ctx context.Context, scanRoots []string, skipPatterns []string) ([]gitMarker, int) {
	matcher := newSkipMatcher(skipPatterns)
	visited := make(map[string]bool)
	dirs := 0

	var out []gitMarker
	for _, root := range scanRoots {
		root = config.ExpandPath(root)
		if root == "" {
			continue
		}
		if _, err := os.Stat(root); err != nil {
			continue
		}
		canonical, err := filepath.EvalSymlinks(root)
		if err != nil {
			canonical = filepath.Clean(root)
		}
		if visited[canonical] {
			continue
		}
		visited[canonical] = true
		dirs++
		sweepDir(ctx, root, matcher, visited, &out, &dirs)
	}
	return out, dirs
}

func sweepDir(ctx context.Context, dir string, matcher *skipMatcher, visited map[string]bool, out *[]gitMarker, dirs *int) {
	select {
	case <-ctx.Done():
		return
	default:
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if name == ".git" {
			sub := filepath.Join(dir, name)
			if e.IsDir() {
				*out = append(*out, gitMarker{checkout: dir})
				continue
			}
			if e.Type().IsRegular() {
				if gd, ok := parseGitFile(sub); ok {
					*out = append(*out, gitMarker{checkout: dir, gitFile: true, gitDir: gd})
				}
				continue
			}
			continue
		}
		if !e.IsDir() || matcher.skip(name, filepath.Join(dir, name)) {
			continue
		}
		sub := filepath.Join(dir, name)
		// Only symlinked dirs need resolution to detect loops/duplicates;
		// regular dirs are keyed by their literal path (already canonical
		// because the root was canonicalized and we never follow symlinks
		// without resolving them here).
		canonical := sub
		if e.Type()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(sub)
			if err != nil {
				continue
			}
			canonical = resolved
		}
		if visited[canonical] {
			continue
		}
		visited[canonical] = true
		*dirs++
		sweepDir(ctx, sub, matcher, visited, out, dirs)
	}
}

// parseGitFile reads a .git marker file (a linked-worktree or submodule
// marker) and returns the resolved git dir it points to.
func parseGitFile(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(data))
	if !strings.HasPrefix(s, "gitdir:") {
		return "", false
	}
	p := strings.TrimSpace(strings.TrimPrefix(s, "gitdir:"))
	if p == "" {
		return "", false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(filepath.Dir(path), p)
	}
	return filepath.Clean(p), true
}

// skipMatcher matches a directory against configured skip patterns. A pattern
// containing a path separator matches that absolute path (the dir itself is
// never descended, so its whole subtree is excluded); a bare name matches any
// directory of that name at any depth. Patterns support filepath.Match globs.
type skipMatcher struct {
	paths []string // absolute-path patterns (exact dir match)
	names []string // basename patterns
}

func newSkipMatcher(patterns []string) *skipMatcher {
	m := &skipMatcher{}
	for _, p := range patterns {
		p = strings.TrimSpace(config.ExpandPath(strings.TrimSpace(p)))
		if p == "" {
			continue
		}
		if strings.ContainsRune(p, filepath.Separator) {
			m.paths = append(m.paths, filepath.Clean(p))
		} else {
			m.names = append(m.names, p)
		}
	}
	return m
}

func (m *skipMatcher) skip(name, path string) bool {
	for _, n := range m.names {
		if ok, _ := filepath.Match(n, name); ok {
			return true
		}
	}
	for _, p := range m.paths {
		if ok, _ := filepath.Match(p, path); ok {
			return true
		}
	}
	return false
}
