package gitx

import "time"

// WorktreeKind is structural: whether a worktree is the repository's main
// checkout or a linked one. App classification (claude/conductor/opencode or
// a user-configured spawner) is cosmetic and lives on Worktree.Spawner.
type WorktreeKind int

const (
	KindMain  WorktreeKind = iota // the primary checkout
	KindOther                     // any linked worktree
)

func (k WorktreeKind) String() string {
	if k == KindMain {
		return "main"
	}
	return "other"
}

// RepoInfo is a discovered git repository, grouped by its git common dir.
// The sweep finds every .git marker (worktree or main checkout); repos are
// grouped by the common dir those markers resolve to, so worktrees created
// by any app — including worktrees-of-worktrees — land under one repo node.
// RepoInfo.Origin carries the normalized remote identity so the UI can further
// group duplicate clones of the same upstream project.
type RepoInfo struct {
	Key       string // basename of the checkout path
	Path      string // main checkout path (or the common dir itself for bare repos)
	CommonDir string // grouping key: the git common dir
	Bare      bool
	Origin    Origin // normalized remote origin, used to group clones into projects
}

// Worktree is one checkout discovered by the .git-marker sweep, enriched with
// per-worktree git state (gathered individually so unknown apps' worktrees
// still get branch/dirty/ahead-behind info even without a consistent registry).
type Worktree struct {
	Path       string
	Branch     string // short branch name, "" when detached
	HEAD       string
	ShortSHA   string
	Detached   bool
	Bare       bool
	Locked     bool
	Prunable   bool
	Main       bool // is the repository's primary worktree
	Kind       WorktreeKind
	Spawner    string // matched spawner name, "" for main/other
	Tool       string // CLI `o` launches in this worktree ("" = plain shell)
	Dirty      bool
	HasUpstream bool
	Ahead      int
	Behind     int
	LastSubject string
	LastDate    string // relative, e.g. "3 days ago"
}

// Branch is a local branch from `git branch --format=...`.
type Branch struct {
	Name         string
	Upstream     string
	Track        string // e.g. "ahead 2, behind 1" (raw %(upstream:track) contents)
	Date         string // relative committerdate
	Subject      string
	WorktreePath string // non-empty when checked out in a worktree
}

// Active reports whether the branch is checked out in some worktree.
func (b Branch) Active() bool { return b.WorktreePath != "" }

// RepoData bundles everything discovered for one repo. Err holds a
// non-fatal scan problem (repo vanished, not a git repo, etc).
type RepoData struct {
	Repo      RepoInfo
	Worktrees []Worktree
	Branches  []Branch
	Err       error
}

// ScanStats reports how a discovery scan went; used for tests and diagnostics.
type ScanStats struct {
	Sweep       time.Duration
	Resolve     time.Duration
	Enrich      time.Duration
	Total       time.Duration
	DirsVisited int
	Markers     int // .git markers (files + dirs) found by the sweep
	Submodules  int // .git-file markers skipped as submodules
	Repos       int
	Worktrees   int // linked worktrees, excluding the main checkout
	Error       error
}
