// Package cleanup finds reclaimable local agent data — finished or stale
// Claude background agents, their scratch dirs, orphaned job dirs, and dead
// transcript stubs — and removes it on request. Scanning is read-only; every
// removal is an explicit Apply of one Candidate, re-validated first and
// confined to the Claude jobs/projects roots. Memory dirs and transcripts of
// sessions that still exist are never candidates.
package cleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tder311/agent-tui/internal/agents"
)

// Kind classifies a cleanup candidate.
type Kind int

const (
	// KindFinishedAgent is a background agent whose job finished (state
	// "done") longer ago than the age threshold. Removed via `claude rm`.
	KindFinishedAgent Kind = iota
	// KindStaleAgent is a background agent that is not busy and has had no
	// activity for longer than the age threshold (typically stuck blocked or
	// idle). Removed via `claude rm`.
	KindStaleAgent
	// KindJobScratch is the tmp/ scratch dir of a finished job. Clearing it
	// keeps the conversation resumable.
	KindJobScratch
	// KindOrphanJob is a jobs/<id> dir with no state.json and no live agent.
	KindOrphanJob
	// KindProjectStub is a projects/<slug> dir holding only a
	// sessions-index.json whose sessions no longer exist.
	KindProjectStub
)

// Kinds lists every kind in display order.
var Kinds = []Kind{KindFinishedAgent, KindStaleAgent, KindJobScratch, KindOrphanJob, KindProjectStub}

func (k Kind) String() string {
	switch k {
	case KindFinishedAgent:
		return "Finished agents"
	case KindStaleAgent:
		return "Stale agents"
	case KindJobScratch:
		return "Job scratch dirs"
	case KindOrphanJob:
		return "Orphaned job dirs"
	case KindProjectStub:
		return "Dead transcript stubs"
	}
	return "unknown"
}

// Action describes what applying a candidate of this kind does.
func (k Kind) Action() string {
	switch k {
	case KindFinishedAgent, KindStaleAgent:
		return "claude rm (session + worktree when safe)"
	case KindJobScratch:
		return "clear tmp/, keep conversation"
	case KindOrphanJob, KindProjectStub:
		return "delete dir"
	}
	return ""
}

// Candidate is one reclaimable item.
type Candidate struct {
	Kind    Kind
	ID      string // job short id, or project slug
	Title   string
	State   string // job state ("done", "blocked", …) when known
	Path    string // dir removed or cleared
	Bytes   int64
	Updated time.Time
	// Default marks candidates safe enough to preselect: scratch/orphan/stub
	// data, and finished agents that were auto-named (not user-named).
	Default bool
}

// Options configures a scan.
type Options struct {
	JobsRoot     string
	ProjectsRoot string
	// MinAge is how long an agent/job must have been inactive before it is a
	// candidate.
	MinAge time.Duration
	// Live is the current `claude agents --json` result; busy agents are never
	// candidates and live ids never count as orphans.
	Live []agents.Agent
	Now  time.Time
}

// orphanGrace keeps a job dir that is still being created (state.json not yet
// written) from being flagged as an orphan.
const orphanGrace = time.Hour

// DefaultOptions returns options for the real ~/.claude layout.
func DefaultOptions(minAgeDays int, live []agents.Agent) Options {
	home, _ := os.UserHomeDir()
	return Options{
		JobsRoot:     filepath.Join(home, ".claude", "jobs"),
		ProjectsRoot: filepath.Join(home, ".claude", "projects"),
		MinAge:       time.Duration(minAgeDays) * 24 * time.Hour,
		Live:         live,
		Now:          time.Now(),
	}
}

type jobState struct {
	State      string `json:"state"`
	Name       string `json:"name"`
	NameSource string `json:"nameSource"`
	Intent     string `json:"intent"`
	UpdatedAt  string `json:"updatedAt"`
}

func (s jobState) title() string {
	if s.Name != "" {
		return s.Name
	}
	return s.Intent
}

// Scan lists every candidate under the configured roots, grouped by kind and
// largest first within a kind. It never mutates anything.
func Scan(opts Options) []Candidate {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	var out []Candidate
	out = append(out, scanJobs(opts)...)
	out = append(out, scanProjects(opts)...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Bytes > out[j].Bytes
	})
	return out
}

func liveByShortID(live []agents.Agent) map[string]agents.Agent {
	m := make(map[string]agents.Agent, len(live))
	for _, a := range live {
		m[shortID(a.ID)] = a
	}
	return m
}

func shortID(id string) string {
	return strings.SplitN(id, "-", 2)[0]
}

func scanJobs(opts Options) []Candidate {
	if opts.JobsRoot == "" {
		return nil
	}
	entries, err := os.ReadDir(opts.JobsRoot)
	if err != nil {
		return nil
	}
	live := liveByShortID(opts.Live)
	var out []Candidate
	for _, e := range entries {
		if !e.IsDir() || !isJobID(e.Name()) {
			continue
		}
		id := e.Name()
		dir := filepath.Join(opts.JobsRoot, id)
		la, isLive := live[id]
		if isLive && la.Status == "busy" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, "state.json"))
		if errors.Is(err, fs.ErrNotExist) {
			if isLive {
				continue
			}
			mod := newestModTime(dir)
			if opts.Now.Sub(mod) < orphanGrace {
				continue
			}
			out = append(out, Candidate{
				Kind: KindOrphanJob, ID: id, Title: "(no state.json)", Path: dir,
				Bytes: dirSize(dir), Updated: mod, Default: true,
			})
			continue
		}
		if err != nil {
			continue
		}
		var st jobState
		if json.Unmarshal(data, &st) != nil {
			continue
		}
		if st.State == "working" {
			continue
		}
		updated, _ := time.Parse(time.RFC3339Nano, st.UpdatedAt)
		if updated.IsZero() {
			if info, err := os.Stat(filepath.Join(dir, "state.json")); err == nil {
				updated = info.ModTime()
			}
		}
		old := opts.Now.Sub(updated) >= opts.MinAge
		base := Candidate{ID: id, Title: st.title(), State: st.State, Updated: updated}

		if st.State == "done" {
			if old {
				c := base
				c.Kind = KindFinishedAgent
				c.Path = dir
				c.Bytes = dirSize(dir)
				c.Default = st.NameSource == "auto"
				out = append(out, c)
			}
			tmp := filepath.Join(dir, "tmp")
			if n := dirSize(tmp); n > 0 {
				c := base
				c.Kind = KindJobScratch
				c.Path = tmp
				c.Bytes = n
				c.Default = old
				out = append(out, c)
			}
			continue
		}
		if old {
			c := base
			c.Kind = KindStaleAgent
			c.Path = dir
			c.Bytes = dirSize(dir)
			out = append(out, c)
		}
	}
	return out
}

// isJobID matches the 8-hex-char job dir names, skipping registry files and
// the cache dir.
func isJobID(name string) bool {
	if len(name) != 8 {
		return false
	}
	for _, r := range name {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

type sessionsIndex struct {
	Entries []struct {
		FullPath string `json:"fullPath"`
	} `json:"entries"`
}

func scanProjects(opts Options) []Candidate {
	if opts.ProjectsRoot == "" {
		return nil
	}
	entries, err := os.ReadDir(opts.ProjectsRoot)
	if err != nil {
		return nil
	}
	var out []Candidate
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(opts.ProjectsRoot, e.Name())
		mod, ok := deadStub(dir)
		if !ok || opts.Now.Sub(mod) < opts.MinAge {
			continue
		}
		out = append(out, Candidate{
			Kind: KindProjectStub, ID: e.Name(), Path: dir,
			Bytes: dirSize(dir), Updated: mod, Default: true,
		})
	}
	return out
}

// deadStub reports whether dir contains nothing but a sessions-index.json
// whose indexed transcripts are all gone, returning the index mtime.
func deadStub(dir string) (time.Time, bool) {
	children, err := os.ReadDir(dir)
	if err != nil || len(children) != 1 || children[0].Name() != "sessions-index.json" || children[0].IsDir() {
		return time.Time{}, false
	}
	idxPath := filepath.Join(dir, "sessions-index.json")
	data, err := os.ReadFile(idxPath)
	if err != nil {
		return time.Time{}, false
	}
	var idx sessionsIndex
	if json.Unmarshal(data, &idx) != nil {
		return time.Time{}, false
	}
	for _, en := range idx.Entries {
		if en.FullPath == "" {
			continue
		}
		if _, err := os.Stat(en.FullPath); err == nil {
			return time.Time{}, false
		}
	}
	info, err := os.Stat(idxPath)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

func newestModTime(dir string) time.Time {
	var newest time.Time
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest
}

// Runner executes `claude <args…>`; injectable for tests.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

const claudeRmTimeout = 60 * time.Second

// DefaultRunner shells out to the claude CLI.
func DefaultRunner(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, claudeRmTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "claude", args...).CombinedOutput()
}

// Apply removes one candidate. Filesystem removals are re-validated against
// the candidate's kind and confined to opts' roots; agent removals go through
// `claude rm`, whose refusal (e.g. unpushed commits in its worktree) is
// returned verbatim.
func Apply(ctx context.Context, c Candidate, opts Options, run Runner) error {
	if run == nil {
		run = DefaultRunner
	}
	switch c.Kind {
	case KindFinishedAgent, KindStaleAgent:
		if !isJobID(c.ID) {
			return fmt.Errorf("invalid job id %q", c.ID)
		}
		out, err := run(ctx, "rm", c.ID)
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("claude rm %s: %s", c.ID, msg)
		}
		return nil
	case KindJobScratch:
		if err := requireWithin(c.Path, opts.JobsRoot); err != nil {
			return err
		}
		if filepath.Base(c.Path) != "tmp" || !isJobID(filepath.Base(filepath.Dir(c.Path))) {
			return fmt.Errorf("refusing to clear %s: not a job tmp dir", c.Path)
		}
		return clearDir(c.Path)
	case KindOrphanJob:
		if err := requireWithin(c.Path, opts.JobsRoot); err != nil {
			return err
		}
		if filepath.Dir(c.Path) != filepath.Clean(opts.JobsRoot) || !isJobID(filepath.Base(c.Path)) {
			return fmt.Errorf("refusing to delete %s: not a job dir", c.Path)
		}
		if _, err := os.Stat(filepath.Join(c.Path, "state.json")); err == nil {
			return fmt.Errorf("refusing to delete %s: it now has a state.json", c.Path)
		}
		return os.RemoveAll(c.Path)
	case KindProjectStub:
		if err := requireWithin(c.Path, opts.ProjectsRoot); err != nil {
			return err
		}
		if filepath.Dir(c.Path) != filepath.Clean(opts.ProjectsRoot) {
			return fmt.Errorf("refusing to delete %s: not a project dir", c.Path)
		}
		if _, ok := deadStub(c.Path); !ok {
			return fmt.Errorf("refusing to delete %s: no longer a dead stub", c.Path)
		}
		return os.RemoveAll(c.Path)
	}
	return fmt.Errorf("unknown cleanup kind %d", c.Kind)
}

// requireWithin rejects paths that are not strictly inside root or that are
// symlinks.
func requireWithin(path, root string) error {
	if root == "" {
		return fmt.Errorf("refusing to delete %s: no root configured", path)
	}
	root = filepath.Clean(root)
	p := filepath.Clean(path)
	if !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return fmt.Errorf("refusing to delete %s: outside %s", path, root)
	}
	info, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("refusing to delete %s: not a directory", path)
	}
	return nil
}

func clearDir(dir string) error {
	children, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, ch := range children {
		if err := os.RemoveAll(filepath.Join(dir, ch.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Result is the outcome of applying one candidate.
type Result struct {
	Candidate Candidate
	Err       error
}

// ApplyAll applies candidates in order. Scratch clears are skipped when the
// same job is also being removed by `claude rm`.
func ApplyAll(ctx context.Context, cs []Candidate, opts Options, run Runner) []Result {
	removing := map[string]bool{}
	for _, c := range cs {
		if c.Kind == KindFinishedAgent || c.Kind == KindStaleAgent {
			removing[c.ID] = true
		}
	}
	out := make([]Result, 0, len(cs))
	for _, c := range cs {
		if c.Kind == KindJobScratch && removing[c.ID] {
			continue
		}
		out = append(out, Result{Candidate: c, Err: Apply(ctx, c, opts, run)})
	}
	return out
}

// Summarize returns "N removed, M failed, X freed" for results.
func Summarize(rs []Result) string {
	var ok, failed int
	var freed int64
	for _, r := range rs {
		if r.Err != nil {
			failed++
			continue
		}
		ok++
		freed += r.Candidate.Bytes
	}
	s := fmt.Sprintf("%d cleaned, %s freed", ok, HumanBytes(freed))
	if failed > 0 {
		s += fmt.Sprintf(", %d failed", failed)
	}
	return s
}

// HumanBytes formats a byte count with binary units.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
