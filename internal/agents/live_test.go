package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tder311/agent-tui/internal/config"
	"github.com/tder311/agent-tui/internal/gitx"
)

// sampleOutput is the verified `claude agents --json` output shape on this
// machine (IDs/names truncated for the fixture).
const sampleOutput = `[
  {"id":"d84f63de","cwd":"/Users/tomderrick/repos/monodo/.claude/worktrees","kind":"background","startedAt":1784766495519,"sessionId":"d84f63de-4ac3-42cf-ae15-94d4bb607315","name":"wind profile reshuffling gdm","state":"blocked"},
  {"pid":44387,"id":"c6352eb3","cwd":"/Users/tomderrick/repos/monodo/.claude/worktrees/nem-bess-replacement-cost-srmc","kind":"background","startedAt":1784953026786,"sessionId":"c6352eb3-253d-4b2b-aa06-ea686da0d47e","name":"battery cycle cost timing","status":"busy","state":"done"},
  {"pid":43021,"id":"6809a9de","cwd":"/Users/tomderrick/repos/monodo/.claude/worktrees/solar-bess-loss-comparison","kind":"background","startedAt":1784953028000,"sessionId":"6809a9de-0000-0000-0000-000000000001","name":"solar bess loss comparison","status":"idle","state":"done"},
  {"pid":77849,"id":"77849a1b","cwd":"/Users/tomderrick/repos/monodo","kind":"interactive","startedAt":1784953030000,"sessionId":"77849a1b-0000-0000-0000-000000000002","name":"worktrees-9f","status":"idle"}
]`

func fakeRunner(raw string, err error) liveAgentRunner {
	return func(context.Context) ([]byte, error) {
		return []byte(raw), err
	}
}

func TestLiveAgentsParse(t *testing.T) {
	live := liveAgents(context.Background(), fakeRunner(sampleOutput, nil))
	if len(live) != 4 {
		t.Fatalf("expected 4 live agents, got %d", len(live))
	}
	want := Agent{
		Tool:      "claude",
		ID:        "c6352eb3-253d-4b2b-aa06-ea686da0d47e",
		Name:      "battery cycle cost timing",
		Cwd:       "/Users/tomderrick/repos/monodo/.claude/worktrees/nem-bess-replacement-cost-srmc",
		Kind:      "background",
		Status:    "busy",
		State:     "done",
		PID:       44387,
		StartedAt: time.UnixMilli(1784953026786),
	}
	if live[1] != want {
		t.Errorf("agent[1] = %+v, want %+v", live[1], want)
	}
	if live[0].Status != "" || live[0].State != "blocked" {
		t.Errorf("agent with no status should still carry state: %+v", live[0])
	}
	if live[3].Kind != "interactive" || live[3].PID != 77849 {
		t.Errorf("interactive agent wrong: %+v", live[3])
	}
	for _, a := range live {
		if a.Tool != "claude" || a.ID == "" || a.Cwd == "" {
			t.Errorf("malformed agent: %+v", a)
		}
	}
}

func TestLiveAgentsFailSilently(t *testing.T) {
	if got := liveAgents(context.Background(), fakeRunner("", errors.New("exec: claude: not found"))); len(got) != 0 {
		t.Errorf("runner error should yield empty, got %+v", got)
	}
	if got := liveAgents(context.Background(), fakeRunner("not json", nil)); len(got) != 0 {
		t.Errorf("bad JSON should yield empty, got %+v", got)
	}
	if got := liveAgents(context.Background(), fakeRunner("[]", nil)); len(got) != 0 {
		t.Errorf("empty list should yield empty, got %+v", got)
	}
}

func TestAttributeLive(t *testing.T) {
	repoDirs := map[string][]string{
		"/repos/monodo": {
			"/repos/monodo",
			"/repos/monodo/.claude/worktrees/agent-1",
			"/repos/monodo/.claude/worktrees/agent-2",
			"/worktrees/monodo/oc-1",
		},
		"/conductor/repos/monodo": {"/conductor/repos/monodo"},
	}
	live := []Agent{
		{Cwd: "/repos/monodo/.claude/worktrees/agent-1", Name: "in worktree"},
		{Cwd: "/repos/monodo/.claude/worktrees", Name: "shared parent"},
		{Cwd: "/repos/monodo", Name: "main"},
		{Cwd: "/tmp/somewhere-else", Name: "outside"},
		{Cwd: "/conductor/repos/monodo", Name: "clone2"},
	}
	got := AttributeLive(live, repoDirs)

	if n := len(got["/repos/monodo"]); n != 3 {
		t.Errorf("main repo should have 3 live agents, got %d: %+v", n, got["/repos/monodo"])
	}
	if n := len(got["/conductor/repos/monodo"]); n != 1 {
		t.Errorf("clone2 should have 1 live agent, got %d", n)
	}
	// The shared-parent agent must attribute to the deepest known prefix
	// (the clone's main checkout), not vanish.
	names := map[string]bool{}
	for _, a := range got["/repos/monodo"] {
		names[a.Name] = true
	}
	if !names["shared parent"] {
		t.Errorf("shared-parent agent not attributed to main clone: %+v", got)
	}
	if _, ok := got[""]; ok || len(got) != 2 {
		t.Errorf("outside agent should be dropped: %+v", got)
	}
}

// TestLiveAgentsSmoke runs the real `claude agents --json` probe and checks
// that live agents resolve to a scanned repo. Skips cleanly when claude is not
// installed; fails when agents are running but none attribute (a regression in
// the matching logic).
func TestLiveAgentsSmoke(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	ctx := context.Background()
	live := LiveAgents(ctx)
	if len(live) == 0 {
		t.Log("no live agents reported (claude missing or nothing running)")
		return
	}
	if _, err := os.Stat(filepath.Join(home, "repos")); err != nil {
		t.Skip("~/repos not present")
	}
	data, _ := gitx.ScanAll(ctx, config.Default())
	byRepo := AttributeLive(live, gitx.AllDirs(data))
	total := 0
	for _, a := range byRepo {
		total += len(a)
	}
	t.Logf("live=%d attributed=%d of %d repos", len(live), total, len(data))
	for _, a := range live {
		t.Logf("%s %s cwd=%s status=%s state=%s", a.Kind, a.Name, a.Cwd, a.Status, a.State)
	}
	if total == 0 {
		t.Errorf("live agents found (%d) but none attributed to a scanned repo", len(live))
	}
}
