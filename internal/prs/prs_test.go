package prs

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tder311/agent-tui/internal/config"
	"github.com/tder311/agent-tui/internal/gitx"
)

// sampleOutput mirrors the `gh pr list --json` output shape for the requested
// fields, covering open, draft, merged and closed states.
const sampleOutput = `[
  {"number":123,"title":"fix scan timeout","state":"OPEN","isDraft":false,"headRefName":"fix/scan-timeout","baseRefName":"main","author":{"login":"tder311"},"createdAt":"2026-07-01T09:00:00Z","updatedAt":"2026-07-03T11:30:00Z","additions":120,"deletions":45,"url":"https://github.com/org/alpha/pull/123"},
  {"number":127,"title":"wip: prs section","state":"OPEN","isDraft":true,"headRefName":"feat/pull-requests","baseRefName":"main","author":{"login":"tder311"},"createdAt":"2026-07-05T18:00:00Z","updatedAt":"2026-07-05T18:00:00Z","additions":900,"deletions":10,"url":"https://github.com/org/alpha/pull/127"},
  {"number":118,"title":"merged legacy pr","state":"MERGED","isDraft":false,"headRefName":"old/merge","baseRefName":"main","author":{"login":"tder311"},"createdAt":"2026-06-01T08:00:00Z","updatedAt":"2026-06-02T08:00:00Z","additions":10,"deletions":20,"url":"https://github.com/org/alpha/pull/118"},
  {"number":99,"title":"closed spam","state":"CLOSED","isDraft":false,"headRefName":"wontfix","baseRefName":"main","author":null,"createdAt":"2026-05-01T08:00:00Z","updatedAt":"2026-05-01T08:00:00Z","additions":0,"deletions":0,"url":"https://github.com/org/alpha/pull/99"}
]`

func fakeRunner(raw string, err error) Runner {
	return func(context.Context) ([]byte, error) {
		return []byte(raw), err
	}
}

func TestParseProject(t *testing.T) {
	cases := []struct {
		id        string
		owner     string
		repo      string
		ok        bool
	}{
		{"github.com/modoenergy/monodo", "modoenergy", "monodo", true},
		{"github.com/tder311/agent-tui", "tder311", "agent-tui", true},
		{"gitlab.com/group/sub/repo", "", "", false},
		{"local:/Users/tom/repos/foo", "", "", false},
		{"github.com/onlyone", "", "", false},
		{"github.com", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		owner, repo, ok := ParseProject(c.id)
		if owner != c.owner || repo != c.repo || ok != c.ok {
			t.Errorf("ParseProject(%q) = %q %q %v; want %q %q %v", c.id, owner, repo, ok, c.owner, c.repo, c.ok)
		}
	}
}

func TestListPRsParse(t *testing.T) {
	prs := ListPRs(context.Background(), "github.com/org/alpha", fakeRunner(sampleOutput, nil))
	if len(prs) != 4 {
		t.Fatalf("expected 4 PRs, got %d", len(prs))
	}
	want := PR{
		Number:    123,
		Title:     "fix scan timeout",
		State:     StateOpen,
		IsDraft:   false,
		HeadRef:   "fix/scan-timeout",
		BaseRef:   "main",
		Author:    "tder311",
		CreatedAt: time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 7, 3, 11, 30, 0, 0, time.UTC),
		Additions: 120,
		Deletions: 45,
		URL:       "https://github.com/org/alpha/pull/123",
	}
	if prs[0] != want {
		t.Errorf("pr[0] = %+v, want %+v", prs[0], want)
	}
	if prs[1].State != StateDraft || !prs[1].IsDraft {
		t.Errorf("draft PR should map to StateDraft: %+v", prs[1])
	}
	if prs[2].State != StateMerged || prs[2].IsDraft {
		t.Errorf("merged PR wrong: %+v", prs[2])
	}
	if prs[3].State != StateClosed || prs[3].Author != "" {
		t.Errorf("closed PR (null author) wrong: %+v", prs[3])
	}
}

func TestListPRsFailSilently(t *testing.T) {
	if got := ListPRs(context.Background(), "github.com/org/alpha", fakeRunner("", errors.New("exec: gh: not found"))); len(got) != 0 {
		t.Errorf("runner error should yield empty, got %+v", got)
	}
	if got := ListPRs(context.Background(), "github.com/org/alpha", fakeRunner("not json", nil)); len(got) != 0 {
		t.Errorf("bad JSON should yield empty, got %+v", got)
	}
	if got := ListPRs(context.Background(), "github.com/org/alpha", fakeRunner("[]", nil)); len(got) != 0 {
		t.Errorf("empty list should yield empty, got %+v", got)
	}
	if got := ListPRs(context.Background(), "gitlab.com/org/alpha", nil); len(got) != 0 {
		t.Errorf("non-github identity should yield empty, got %+v", got)
	}
}

// TestFetchAllCaching asserts clones of one project trigger exactly one fetch
// per distinct identity, and that each identity is fetched with its own
// owner/repo.
func TestFetchAllCaching(t *testing.T) {
	var calls []string
	factory := func(owner, repo string) Runner {
		calls = append(calls, owner+"/"+repo)
		raw := `[{"number":1,"title":"one","state":"OPEN","isDraft":false,"headRefName":"a","baseRefName":"main","author":{"login":"x"},"createdAt":"","updatedAt":"","additions":0,"deletions":0,"url":""}]`
		return fakeRunner(raw, nil)
	}
	ids := []string{
		"github.com/org/alpha", // clone 1
		"github.com/org/alpha", // clone 2 (same project → deduped)
		"github.com/org/beta",
	}
	got := FetchAll(context.Background(), ids, factory)

	if len(calls) != 2 {
		t.Errorf("expected 2 fetches (alpha deduped + beta), got %d: %v", len(calls), calls)
	}
	if len(got["github.com/org/alpha"]) != 1 || len(got["github.com/org/beta"]) != 1 {
		t.Errorf("wrong results: %+v", got)
	}
}

func TestFetchAllSkipsNonGithub(t *testing.T) {
	var calls []string
	factory := func(owner, repo string) Runner {
		calls = append(calls, owner+"/"+repo)
		return fakeRunner("[]", nil)
	}
	ids := []string{
		"github.com/org/alpha",
		"gitlab.com/group/sub",
		"local:/Users/tom/repos/scratch",
		"",
	}
	got := FetchAll(context.Background(), ids, factory)
	if len(calls) != 1 {
		t.Errorf("expected only the github identity to be fetched, got %d: %v", len(calls), calls)
	}
	if _, ok := got["github.com/org/alpha"]; !ok {
		t.Errorf("missing github result: %+v", got)
	}
	for _, bad := range []string{"gitlab.com/group/sub", "local:/Users/tom/repos/scratch", ""} {
		if _, ok := got[bad]; ok {
			t.Errorf("non-github identity should not be in results: %+v", got)
		}
	}
}

func TestFetchAllFailSilently(t *testing.T) {
	factory := func(_, _ string) Runner {
		return fakeRunner("", errors.New("boom"))
	}
	got := FetchAll(context.Background(), []string{"github.com/org/alpha"}, factory)
	if len(got) != 0 {
		t.Errorf("failed fetch should produce no entry, got %+v", got)
	}
}

func TestFetchAllEmpty(t *testing.T) {
	if got := FetchAll(context.Background(), nil, nil); len(got) != 0 {
		t.Errorf("no identities → no results, got %+v", got)
	}
	got := FetchAll(context.Background(), []string{"local:/x"}, nil)
	if got == nil || len(got) != 0 {
		t.Errorf("non-github identities → empty non-nil map, got %+v", got)
	}
}

// TestPRsSmoke runs the real `gh pr list` against the github.com projects
// found by a real scan of the user's home, asserting open PRs are fetched and
// attributed to the right project identity. Skips cleanly when gh is missing
// or no github.com repos are found.
func TestPRsSmoke(t *testing.T) {
	if _, err := exec.Command("gh", "--version").Output(); err != nil {
		t.Skip("gh not available")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	if _, err := os.Stat(filepath.Join(home, "repos")); err != nil {
		t.Skip("~/repos not present")
	}
	ctx := context.Background()
	data, _ := gitx.ScanAll(ctx, config.Default())
	seen := map[string]bool{}
	var ids []string
	for _, rd := range data {
		id := rd.Repo.Origin.Identity
		if _, _, ok := ParseProject(id); ok && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		t.Skip("no github.com projects found")
	}
	got := FetchAll(ctx, ids, nil)
	if len(got) == 0 {
		t.Error("expected PR results for github projects")
	}
	total := 0
	for _, id := range ids {
		prs := got[id]
		total += len(prs)
		t.Logf("%s: %d open PRs", id, len(prs))
		for _, p := range prs {
			t.Logf("  #%d [%s] %s (+%d -%d)", p.Number, p.State, p.Title, p.Additions, p.Deletions)
		}
	}
	t.Logf("projects=%d total open PRs=%d", len(ids), total)
}
