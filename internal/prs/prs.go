// Package prs fetches GitHub pull requests for a project via the gh CLI.
// Best-effort and read-only: a missing `gh` binary, a nonzero exit, or bad
// JSON yields no PRs (never an error), so the TUI degrades exactly like the
// live-agents feature. Only github.com projects are queried — gitlab and
// local/no-remote origins are skipped. PRs belong to the origin repo, so they
// are fetched once per distinct project identity and shared across that
// project's clones.
package prs

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// State is the lifecycle state of a pull request. DRAFT is derived from the
// isDraft flag on open PRs.
type State string

const (
	StateOpen   State = "OPEN"
	StateDraft  State = "DRAFT"
	StateMerged State = "MERGED"
	StateClosed State = "CLOSED"
)

// PR is one pull request of a github.com project.
type PR struct {
	Number    int
	Title     string
	State     State
	IsDraft   bool
	HeadRef   string
	BaseRef   string
	Author    string
	CreatedAt time.Time
	UpdatedAt time.Time
	Additions int
	Deletions int
	URL       string
}

// prTimeout bounds each `gh pr list` probe so a hung gh never stalls a refresh.
const prTimeout = 5 * time.Second

// maxParallel bounds concurrent gh probes during a scan, consistent with the
// scan's own worker pool.
const maxParallel = 8

// Runner executes the gh command for one project; injectable for tests.
type Runner func(ctx context.Context) ([]byte, error)

// Factory builds a Runner for a project; injectable for tests.
type Factory func(owner, repo string) Runner

// defaultRunner builds the `gh pr list` command for a project.
func defaultRunner(owner, repo string) Runner {
	args := []string{
		"pr", "list",
		"-R", owner + "/" + repo,
		"--state", "open",
		"--json", "number,title,isDraft,headRefName,baseRefName,author,createdAt,updatedAt,additions,deletions,url,state",
	}
	return func(ctx context.Context) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, prTimeout)
		defer cancel()
		return exec.CommandContext(ctx, "gh", args...).Output()
	}
}

// ParseProject splits a normalized origin identity ("host/owner/repo") into
// owner and repo. Only github.com hosts are supported; ok=false for gitlab,
// local, or malformed identities.
func ParseProject(originIdentity string) (owner, repo string, ok bool) {
	segs := strings.Split(originIdentity, "/")
	if len(segs) != 3 || segs[0] != "github.com" {
		return "", "", false
	}
	if segs[1] == "" || segs[2] == "" {
		return "", "", false
	}
	return segs[1], segs[2], true
}

// rawPR mirrors one `gh pr list --json` entry. author may be null for
// accounts that no longer exist; the Login field stays empty.
type rawPR struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	State       string `json:"state"`
	IsDraft     bool   `json:"isDraft"`
	HeadRefName string `json:"headRefName"`
	BaseRefName string `json:"baseRefName"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	URL       string `json:"url"`
}

func (r rawPR) toPR() PR {
	pr := PR{
		Number:    r.Number,
		Title:     r.Title,
		IsDraft:   r.IsDraft,
		HeadRef:   r.HeadRefName,
		BaseRef:   r.BaseRefName,
		Author:    r.Author.Login,
		Additions: r.Additions,
		Deletions: r.Deletions,
		URL:       r.URL,
	}
	switch strings.ToUpper(r.State) {
	case "MERGED":
		pr.State = StateMerged
	case "CLOSED":
		pr.State = StateClosed
	default:
		if r.IsDraft {
			pr.State = StateDraft
		} else {
			pr.State = StateOpen
		}
	}
	pr.CreatedAt, _ = time.Parse(time.RFC3339, r.CreatedAt)
	pr.UpdatedAt, _ = time.Parse(time.RFC3339, r.UpdatedAt)
	return pr
}

// ListPRs returns the open pull requests for a github.com project via the gh
// CLI. Best-effort: a missing `gh` binary, a nonzero exit, or bad JSON all
// yield an empty slice (never an error). runner is injectable for tests; nil
// uses the real `gh pr list`. Non-github identities return empty.
func ListPRs(ctx context.Context, originIdentity string, runner Runner) []PR {
	owner, repo, ok := ParseProject(originIdentity)
	if !ok {
		return nil
	}
	if runner == nil {
		runner = defaultRunner(owner, repo)
	}
	raw, err := runner(ctx)
	if err != nil {
		return nil
	}
	var raws []rawPR
	if err := json.Unmarshal(raw, &raws); err != nil {
		return nil
	}
	out := make([]PR, 0, len(raws))
	for _, r := range raws {
		out = append(out, r.toPR())
	}
	return out
}

// FetchAll lists PRs for each distinct github project identity, at most once
// per identity (so clones of one project share a single fetch), with probes
// capped to maxParallel concurrent gh calls. The result maps identity → PRs;
// non-github/local identities are skipped. Never fails: a broken gh yields no
// entry for the affected projects, and the caller's 60s scan budget is not
// exceeded thanks to the per-call 5s cap.
func FetchAll(ctx context.Context, identities []string, factory Factory) map[string][]PR {
	out := make(map[string][]PR, len(identities))
	var distinct []string
	seen := make(map[string]bool, len(identities))
	for _, id := range identities {
		if _, _, ok := ParseProject(id); !ok {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		distinct = append(distinct, id)
	}
	if len(distinct) == 0 {
		return out
	}

	type result struct {
		id  string
		prs []PR
	}
	results := make(chan result, len(distinct))
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup
	for _, id := range distinct {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			owner, repo, _ := ParseProject(id)
			var run Runner
			if factory != nil {
				run = factory(owner, repo)
			} else {
				run = defaultRunner(owner, repo)
			}
			results <- result{id, ListPRs(ctx, id, run)}
		}(id)
	}
	wg.Wait()
	close(results)
	for r := range results {
		// nil prs marks a failed/absent fetch; a successful fetch yields a
		// non-nil (possibly empty) slice.
		if r.prs != nil {
			out[r.id] = r.prs
		}
	}
	return out
}
