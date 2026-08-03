package tui

import (
	"time"

	"github.com/tder311/agent-tui/internal/agents"
	"github.com/tder311/agent-tui/internal/gitx"
)

// TickMsg fires the periodic auto-refresh.
type TickMsg time.Time

// ScanResultMsg carries a full background scan.
type ScanResultMsg struct {
	Data     []gitx.RepoData
	Sessions map[string][]agents.Session // historical, keyed by repo path
	Live     map[string][]agents.Agent   // running agents, keyed by repo path
	At       time.Time
}

// actionKind identifies a destructive operation.
type actionKind int

const (
	actionRemoveWorktree actionKind = iota
	actionForceRemoveWorktree
	actionDeleteBranch
	actionForceDeleteBranch
	actionPruneWorktrees
)

// actionRequest describes a pending destructive action awaiting y/n confirm.
type actionRequest struct {
	Kind     actionKind
	Label    string // human description for the confirm dialog
	RepoPath string
	WtPath   string // for worktree removal
	Branch   string // for branch deletion
}

// ActionResultMsg reports the outcome of a destructive action. NeedsForce
// means the op refused due to dirty worktree / unmerged branch and the UI
// should offer a force variant.
type ActionResultMsg struct {
	Kind       actionKind
	Label      string
	RepoPath   string
	WtPath     string
	Branch     string
	NeedsForce bool
	Err        error
}

// OpenResultMsg reports the outcome of opening a terminal tab.
type OpenResultMsg struct {
	Err error
}
