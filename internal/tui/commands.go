package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tder311/agent-tui/internal/agents"
	"github.com/tder311/agent-tui/internal/config"
	"github.com/tder311/agent-tui/internal/gitx"
	"github.com/tder311/agent-tui/internal/prs"
)

const scanTimeout = 60 * time.Second

func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

// scanCmd runs the entire discovery in the background: sweep for .git markers,
// group into repos, enrich worktrees/branches, discover agent sessions, live
// agents, and open pull requests for each github.com project. Never blocks the
// UI; never mutates anything. PR fetches are best-effort and cached once per
// distinct project identity.
func scanCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		defer cancel()
		data, _ := gitx.ScanAll(ctx, cfg)
		repoDirs := gitx.AllDirs(data)
		sessions := agents.Discover(repoDirs,
			agents.WithIncludeAgentSessions(cfg.IncludeAgentSessions),
			agents.WithMaxAgeDays(cfg.SessionDaysValue()),
			agents.WithMaxPerClone(cfg.SessionCapValue()),
		)
		live := agents.AttributeLive(agents.LiveAgents(ctx), repoDirs)
		prsByID := prs.FetchAll(ctx, distinctIdentities(data), nil)
		return ScanResultMsg{Data: data, Sessions: sessions, Live: live, PRs: prsByID, At: time.Now()}
	}
}

// distinctIdentities returns the distinct project identities of a scan, so PRs
// are fetched once per project and reused across its clones.
func distinctIdentities(data []gitx.RepoData) []string {
	seen := make(map[string]bool, len(data))
	var out []string
	for _, rd := range data {
		id := rd.Repo.Origin.Identity
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// performActionCmd executes a confirmed destructive action.
func performActionCmd(req actionRequest) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		res := ActionResultMsg{
			Kind:     req.Kind,
			Label:    req.Label,
			RepoPath: req.RepoPath,
			WtPath:   req.WtPath,
			Branch:   req.Branch,
		}
		switch req.Kind {
		case actionRemoveWorktree:
			err := gitx.RemoveWorktree(ctx, req.RepoPath, req.WtPath, false)
			if err == gitx.ErrDirty {
				res.NeedsForce = true
			} else {
				res.Err = err
			}
		case actionForceRemoveWorktree:
			res.Err = gitx.RemoveWorktree(ctx, req.RepoPath, req.WtPath, true)
		case actionDeleteBranch:
			err := gitx.DeleteBranch(ctx, req.RepoPath, req.Branch, false)
			if err == gitx.ErrUnmerged {
				res.NeedsForce = true
			} else {
				res.Err = err
			}
		case actionForceDeleteBranch:
			res.Err = gitx.DeleteBranch(ctx, req.RepoPath, req.Branch, true)
		case actionPruneWorktrees:
			res.Err = gitx.PruneWorktrees(ctx, req.RepoPath)
		}
		return res
	}
}

// openTerminalCmd opens a new terminal tab in dir, optionally launching a tool.
func openTerminalCmd(termApp, dir, toolCmd string) tea.Cmd {
	return func() tea.Msg {
		return OpenResultMsg{Kind: openTerminal, Err: openInTerminal(termApp, dir, toolCmd)}
	}
}

// openBrowserCmd opens a URL in the default browser.
func openBrowserCmd(url string) tea.Cmd {
	return func() tea.Msg {
		return OpenResultMsg{Kind: openBrowser, Err: openInBrowser(url)}
	}
}
