package agents

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"
)

// Agent is one LIVE agent process/session reported by the official Claude
// local API (`claude agents --json`). Unlike Session (a historical log read
// from disk), an Agent represents something running right now.
type Agent struct {
	Tool      string // always "claude"
	ID        string // full sessionId (used for `claude --resume`)
	Name      string
	Cwd       string
	Kind      string // "background" | "interactive"
	Status    string // "busy" | "idle" | "blocked" (may be absent)
	State     string // "done" etc. (may be absent)
	PID       int
	StartedAt time.Time
}

// liveAgentTimeout bounds the `claude agents --json` probe so a hung CLI never
// stalls a UI refresh.
const liveAgentTimeout = 5 * time.Second

// rawAgent mirrors the `claude agents --json` entry shape so we can decode
// startedAt (epoch millis) into a time.Time without a custom Unmarshaler.
type rawAgent struct {
	ID        string `json:"id"`
	PID       int    `json:"pid,omitempty"`
	Cwd       string `json:"cwd"`
	Kind      string `json:"kind"`
	StartedAt int64  `json:"startedAt"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	State     string `json:"state"`
}

func (r rawAgent) toAgent() Agent {
	id := r.SessionID
	if id == "" {
		id = r.ID
	}
	started := time.Time{}
	if r.StartedAt > 0 {
		started = time.UnixMilli(r.StartedAt)
	}
	return Agent{
		Tool:      "claude",
		ID:        id,
		Name:      r.Name,
		Cwd:       r.Cwd,
		Kind:      r.Kind,
		Status:    r.Status,
		State:     r.State,
		PID:       r.PID,
		StartedAt: started,
	}
}

// liveAgentRunner shells out to the claude CLI; injectable for tests.
type liveAgentRunner func(ctx context.Context) ([]byte, error)

// defaultLiveAgentRunner runs `claude agents --json` with a 5s cap.
func defaultLiveAgentRunner(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, liveAgentTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "claude", "agents", "--json").Output()
}

// LiveAgents returns the currently-running Claude Code agents via the official
// local API. Best-effort: a missing `claude` binary, a nonzero exit, or bad
// JSON all yield an empty slice (never an error) so the TUI degrades to
// historical sessions only.
func LiveAgents(ctx context.Context) []Agent {
	return liveAgents(ctx, defaultLiveAgentRunner)
}

func liveAgents(ctx context.Context, run liveAgentRunner) []Agent {
	raw, err := run(ctx)
	if err != nil {
		return nil
	}
	var raws []rawAgent
	if err := json.Unmarshal(raw, &raws); err != nil {
		return nil
	}
	out := make([]Agent, 0, len(raws))
	for _, r := range raws {
		out = append(out, r.toAgent())
	}
	return out
}

// AttributeLive buckets live agents by repo using the same longest-dir
// attribution as historical sessions: each agent lands on the repo whose
// main/worktree dir is the longest prefix of its cwd, so a background agent
// inside a worktree resolves to that worktree's repo and an agent launched
// from a shared parent (e.g. ~/.claude/worktrees) resolves to the owning
// clone. Agents outside every known dir are dropped.
func AttributeLive(live []Agent, repoDirs map[string][]string) map[string][]Agent {
	dirs := withConductorVirtualDirs(repoDirs)
	out := make(map[string][]Agent)
	for _, a := range live {
		repo := matchRepo(a.Cwd, dirs)
		if repo == "" {
			continue
		}
		out[repo] = append(out[repo], a)
	}
	return out
}
