package cleanup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tder311/agent-tui/internal/agents"
)

var now = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func setMtime(t *testing.T, root string, at time.Time) {
	t.Helper()
	_ = filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err == nil {
			_ = os.Chtimes(p, at, at)
		}
		return nil
	})
}

func job(t *testing.T, root, id, state, nameSource string, updated time.Time, scratch string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	writeFile(t, filepath.Join(dir, "state.json"),
		`{"state":"`+state+`","name":"job `+id+`","nameSource":"`+nameSource+`","updatedAt":"`+updated.Format(time.RFC3339Nano)+`"}`)
	if scratch != "" {
		writeFile(t, filepath.Join(dir, "tmp", "out.bin"), scratch)
	} else if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func testOptions(t *testing.T) Options {
	return Options{
		JobsRoot:     t.TempDir(),
		ProjectsRoot: t.TempDir(),
		MinAge:       7 * 24 * time.Hour,
		Now:          now,
	}
}

func byKindID(cs []Candidate) map[string]Candidate {
	m := make(map[string]Candidate, len(cs))
	for _, c := range cs {
		m[c.Kind.String()+"/"+c.ID] = c
	}
	return m
}

func TestScanClassifiesJobs(t *testing.T) {
	o := testOptions(t)
	old := now.Add(-10 * 24 * time.Hour)
	recent := now.Add(-time.Hour)

	job(t, o.JobsRoot, "aaaaaaa1", "done", "auto", old, "xxxx")
	job(t, o.JobsRoot, "aaaaaaa2", "done", "user", old, "")
	job(t, o.JobsRoot, "aaaaaaa3", "done", "user", recent, "yyyyyyyy")
	job(t, o.JobsRoot, "aaaaaaa4", "blocked", "user", old, "zz")
	job(t, o.JobsRoot, "aaaaaaa5", "working", "auto", old, "zz")
	job(t, o.JobsRoot, "aaaaaaa6", "blocked", "user", recent, "")
	job(t, o.JobsRoot, "aaaaaaa7", "done", "auto", old, "zz")
	o.Live = []agents.Agent{{ID: "aaaaaaa7-1111-2222", Status: "busy"}}

	m := byKindID(Scan(o))

	if c, ok := m["Finished agents/aaaaaaa1"]; !ok || !c.Default {
		t.Errorf("old auto-named done job should be a preselected finished agent: %+v", c)
	}
	if c, ok := m["Job scratch dirs/aaaaaaa1"]; !ok || c.Bytes != 4 || !c.Default {
		t.Errorf("old done job scratch should be preselected with 4 bytes: %+v", c)
	}
	if c, ok := m["Finished agents/aaaaaaa2"]; !ok || c.Default {
		t.Errorf("user-named finished agent must not be preselected: %+v", c)
	}
	if _, ok := m["Job scratch dirs/aaaaaaa2"]; ok {
		t.Error("empty scratch must not be a candidate")
	}
	if _, ok := m["Finished agents/aaaaaaa3"]; ok {
		t.Error("recently finished agent must not be a candidate")
	}
	if c, ok := m["Job scratch dirs/aaaaaaa3"]; !ok || c.Default {
		t.Errorf("recent done scratch should be offered but not preselected: %+v", c)
	}
	if c, ok := m["Stale agents/aaaaaaa4"]; !ok || c.Default {
		t.Errorf("old blocked job should be an unselected stale agent: %+v", c)
	}
	for _, id := range []string{"aaaaaaa5", "aaaaaaa6", "aaaaaaa7"} {
		for _, k := range Kinds {
			if _, ok := m[k.String()+"/"+id]; ok {
				t.Errorf("%s must not be a candidate (%s)", id, k)
			}
		}
	}
}

func TestScanOrphanJobs(t *testing.T) {
	o := testOptions(t)
	writeFile(t, filepath.Join(o.JobsRoot, "bbbbbbb1", "tmp", "a.csv"), "123")
	setMtime(t, filepath.Join(o.JobsRoot, "bbbbbbb1"), now.Add(-2*time.Hour))
	writeFile(t, filepath.Join(o.JobsRoot, "bbbbbbb2", "tmp", "a.csv"), "123")
	setMtime(t, filepath.Join(o.JobsRoot, "bbbbbbb2"), now.Add(-10*time.Minute))
	writeFile(t, filepath.Join(o.JobsRoot, "bbbbbbb3", "tmp", "a.csv"), "123")
	setMtime(t, filepath.Join(o.JobsRoot, "bbbbbbb3"), now.Add(-2*time.Hour))
	writeFile(t, filepath.Join(o.JobsRoot, "cache", "x"), "1")
	setMtime(t, filepath.Join(o.JobsRoot, "cache"), now.Add(-48*time.Hour))
	o.Live = []agents.Agent{{ID: "bbbbbbb3-0000", Status: "idle"}}

	m := byKindID(Scan(o))
	if c, ok := m["Orphaned job dirs/bbbbbbb1"]; !ok || c.Bytes != 3 || !c.Default {
		t.Errorf("stateless job dir should be a preselected orphan: %+v", c)
	}
	if _, ok := m["Orphaned job dirs/bbbbbbb2"]; ok {
		t.Error("a just-created job dir must get a grace period")
	}
	if _, ok := m["Orphaned job dirs/bbbbbbb3"]; ok {
		t.Error("a live job dir must never be an orphan")
	}
	if len(m) != 1 {
		t.Errorf("want only one candidate, got %v", m)
	}
}

func TestScanProjectStubs(t *testing.T) {
	o := testOptions(t)
	old := now.Add(-30 * 24 * time.Hour)

	dead := filepath.Join(o.ProjectsRoot, "-Users-x-gone")
	writeFile(t, filepath.Join(dead, "sessions-index.json"), `{"entries":[{"fullPath":"/nonexistent/a.jsonl"}]}`)
	setMtime(t, dead, old)

	alive := filepath.Join(o.ProjectsRoot, "-Users-x-alive")
	transcript := filepath.Join(o.ProjectsRoot, "-Users-x-elsewhere", "s.jsonl")
	writeFile(t, transcript, "{}")
	writeFile(t, filepath.Join(alive, "sessions-index.json"), `{"entries":[{"fullPath":"`+transcript+`"}]}`)
	setMtime(t, alive, old)

	withMemory := filepath.Join(o.ProjectsRoot, "-Users-x-mem")
	writeFile(t, filepath.Join(withMemory, "sessions-index.json"), `{"entries":[]}`)
	writeFile(t, filepath.Join(withMemory, "memory", "MEMORY.md"), "keep")
	setMtime(t, withMemory, old)

	fresh := filepath.Join(o.ProjectsRoot, "-Users-x-fresh")
	writeFile(t, filepath.Join(fresh, "sessions-index.json"), `{"entries":[]}`)

	m := byKindID(Scan(o))
	if _, ok := m["Dead transcript stubs/-Users-x-gone"]; !ok {
		t.Error("index-only dir with missing transcripts should be a stub")
	}
	for _, id := range []string{"-Users-x-alive", "-Users-x-mem", "-Users-x-fresh", "-Users-x-elsewhere"} {
		if _, ok := m["Dead transcript stubs/"+id]; ok {
			t.Errorf("%s must not be a stub", id)
		}
	}
}

func TestApplyFilesystemKinds(t *testing.T) {
	o := testOptions(t)
	old := now.Add(-10 * 24 * time.Hour)
	dir := job(t, o.JobsRoot, "ccccccc1", "done", "user", old, "data")
	writeFile(t, filepath.Join(o.JobsRoot, "ccccccc2", "tmp", "x"), "1")
	setMtime(t, filepath.Join(o.JobsRoot, "ccccccc2"), old)
	stub := filepath.Join(o.ProjectsRoot, "-stub")
	writeFile(t, filepath.Join(stub, "sessions-index.json"), `{"entries":[]}`)
	setMtime(t, stub, old)

	var toApply []Candidate
	for _, c := range Scan(o) {
		if c.Kind != KindFinishedAgent {
			toApply = append(toApply, c)
		}
	}
	rs := ApplyAll(context.Background(), toApply, o, nil)
	for _, r := range rs {
		if r.Err != nil {
			t.Fatalf("apply %s/%s: %v", r.Candidate.Kind, r.Candidate.ID, r.Err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); err != nil {
		t.Error("clearing scratch must keep the job's state.json")
	}
	if ents, _ := os.ReadDir(filepath.Join(dir, "tmp")); len(ents) != 0 {
		t.Error("scratch tmp/ should be empty")
	}
	if _, err := os.Stat(filepath.Join(o.JobsRoot, "ccccccc2")); !os.IsNotExist(err) {
		t.Error("orphan job dir should be deleted")
	}
	if _, err := os.Stat(stub); !os.IsNotExist(err) {
		t.Error("stub should be deleted")
	}
}

func TestApplyRefusesOutsideRoots(t *testing.T) {
	o := testOptions(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "keep"), "1")

	cases := []Candidate{
		{Kind: KindOrphanJob, ID: "dddddddd", Path: outside},
		{Kind: KindProjectStub, Path: outside},
		{Kind: KindJobScratch, Path: filepath.Join(o.JobsRoot, "..", filepath.Base(outside))},
		{Kind: KindOrphanJob, Path: o.JobsRoot},
	}
	for _, c := range cases {
		if err := Apply(context.Background(), c, o, nil); err == nil {
			t.Errorf("Apply(%+v) should refuse", c)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatal("file outside roots was deleted")
	}
}

func TestApplyRevalidatesStub(t *testing.T) {
	o := testOptions(t)
	stub := filepath.Join(o.ProjectsRoot, "-stub")
	writeFile(t, filepath.Join(stub, "sessions-index.json"), `{"entries":[]}`)
	c := Candidate{Kind: KindProjectStub, ID: "-stub", Path: stub}
	writeFile(t, filepath.Join(stub, "memory", "MEMORY.md"), "keep")
	if err := Apply(context.Background(), c, o, nil); err == nil {
		t.Fatal("stub that gained a memory dir must not be deleted")
	}
}

func TestApplyAgentUsesClaudeRm(t *testing.T) {
	o := testOptions(t)
	var calls []string
	run := func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[1] == "eeeeeee2" {
			return []byte("worktree has unpushed commits; pass --discard-unpushed abc@wt"), errors.New("exit 1")
		}
		return nil, nil
	}
	rs := ApplyAll(context.Background(), []Candidate{
		{Kind: KindFinishedAgent, ID: "eeeeeee1", Bytes: 10},
		{Kind: KindJobScratch, ID: "eeeeeee1", Path: "/should/be/skipped"},
		{Kind: KindStaleAgent, ID: "eeeeeee2"},
		{Kind: KindFinishedAgent, ID: "../etc"},
	}, o, run)

	if strings.Join(calls, ",") != "rm eeeeeee1,rm eeeeeee2" {
		t.Fatalf("unexpected claude calls: %v", calls)
	}
	if len(rs) != 3 {
		t.Fatalf("scratch of a removed job should be skipped, got %d results", len(rs))
	}
	if rs[0].Err != nil || rs[1].Err == nil || !strings.Contains(rs[1].Err.Error(), "discard-unpushed") || rs[2].Err == nil {
		t.Fatalf("unexpected results: %+v", rs)
	}
	if got := Summarize(rs); got != "1 cleaned, 10 B freed, 2 failed" {
		t.Errorf("Summarize = %q", got)
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 7 << 30: "7.0 GB"} {
		if got := HumanBytes(n); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
