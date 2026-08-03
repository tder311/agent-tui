// Package agents discovers local AI coding agent sessions (Claude Code,
// OpenCode) and maps them to repo/worktree directories. Everything here is
// read-only and best-effort: a missing or broken source yields zero sessions,
// never an app-fatal error.
package agents

import (
	"sort"
	"time"
)

// Session is one agent session tied to a working directory.
type Session struct {
	Tool     string // "claude" or "opencode"
	ID       string
	Title    string // session title (opencode) or best-effort title (claude)
	Dir      string // working directory the session ran in
	Updated  time.Time
	Messages int
	// AgentName is set for Claude sessions launched by a background agent
	// (from the jobs registry) when such sessions are included; "" for
	// ordinary interactive sessions and OpenCode sessions.
	AgentName string
}

// Options tunes discovery of historical sessions.
type Options struct {
	// IncludeAgents lists background-agent sessions (default false).
	IncludeAgents bool
	// MaxAgeDays is the recency window for historical sessions; 0 = unlimited.
	MaxAgeDays int
	// MaxPerClone caps sessions per clone, most recent first; 0 = unlimited.
	MaxPerClone int
}

// Option mutates Options; Discover takes zero or more of these.
type Option func(*Options)

// WithIncludeAgentSessions controls whether background-agent sessions appear.
func WithIncludeAgentSessions(include bool) Option {
	return func(o *Options) { o.IncludeAgents = include }
}

// WithMaxAgeDays sets the recency window in days (0 = unlimited).
func WithMaxAgeDays(days int) Option {
	return func(o *Options) { o.MaxAgeDays = days }
}

// WithMaxPerClone sets the per-clone session cap (0 = unlimited).
func WithMaxPerClone(n int) Option {
	return func(o *Options) { o.MaxPerClone = n }
}

func applyOptions(opts []Option) Options {
	var o Options
	for _, f := range opts {
		if f != nil {
			f(&o)
		}
	}
	return o
}

// Discover gathers sessions from all sources and buckets them by repo path.
// repoDirs maps repoPath → all dirs belonging to that repo (main + worktrees).
// It never fails; broken sources contribute nothing.
func Discover(repoDirs map[string][]string, opts ...Option) map[string][]Session {
	o := applyOptions(opts)
	out := make(map[string][]Session)
	dirs := withConductorVirtualDirs(repoDirs)

	var all []Session
	all = append(all, claudeSessions(dirs, o)...)
	all = append(all, opencodeSessions()...)

	for _, s := range all {
		repo := matchRepo(s.Dir, dirs)
		if repo == "" {
			continue
		}
		out[repo] = append(out[repo], s)
	}
	for repo := range out {
		ss := out[repo]
		sort.Slice(ss, func(i, j int) bool { return ss[i].Updated.After(ss[j].Updated) })
		ss = applyRecencyCap(ss, o.MaxAgeDays, o.MaxPerClone)
		out[repo] = ss
	}
	return out
}

// applyRecencyCap drops sessions older than maxAgeDays (when > 0) and keeps
// only the newest maxPerClone (when > 0). ss must already be sorted by
// Updated descending; the slice returned is a prefix of the caller's.
func applyRecencyCap(ss []Session, maxAgeDays, maxPerClone int) []Session {
	if maxAgeDays > 0 {
		cutoff := time.Now().Add(-time.Duration(maxAgeDays) * 24 * time.Hour)
		out := ss[:0]
		for _, s := range ss {
			if s.Updated.IsZero() || s.Updated.After(cutoff) {
				out = append(out, s)
			}
		}
		ss = out
	}
	if maxPerClone > 0 && len(ss) > maxPerClone {
		ss = ss[:maxPerClone]
	}
	return ss
}

// matchRepo finds the repo whose dir set contains dir (exact or nested).
// Longest matching dir wins so a worktree nested under another checkout
// resolves to its own repo.
func matchRepo(dir string, repoDirs map[string][]string) string {
	if dir == "" {
		return ""
	}
	best := ""
	bestLen := -1
	for repo, dirs := range repoDirs {
		for _, d := range dirs {
			if dirWithin(dir, d) && len(d) > bestLen {
				best = repo
				bestLen = len(d)
			}
		}
	}
	return best
}

func dirWithin(dir, root string) bool {
	if dir == root {
		return true
	}
	if len(dir) > len(root) && dir[:len(root)] == root && dir[len(root)] == '/' {
		return true
	}
	return false
}
