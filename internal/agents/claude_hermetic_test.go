package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// newHermeticClaudeRoots builds a fake projects root and jobs root with one
// project dir (slug of /fake/repo) containing the given sessions, and returns
// both roots. State.json for a session is written from the raw JSON when non-empty.
func newHermeticClaudeRoots(t *testing.T, files map[string]string, jobs map[string]string) (string, string) {
	t.Helper()
	projectsRoot := t.TempDir()
	jobsRoot := t.TempDir()

	slug := claudeSlug("/fake/repo")
	projDir := filepath.Join(projectsRoot, slug)
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(projDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for prefix, content := range jobs {
		if content == "" {
			continue
		}
		dir := filepath.Join(jobsRoot, prefix)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return projectsRoot, jobsRoot
}

func sessionsByID(ss []Session) map[string]Session {
	out := make(map[string]Session, len(ss))
	for _, s := range ss {
		out[s.ID] = s
	}
	return out
}

func assertSession(t *testing.T, m map[string]Session, id string, want *Session) {
	t.Helper()
	s, ok := m[id]
	if !ok {
		t.Fatalf("missing session %q in %v", id, ids(m))
	}
	if want != nil {
		if s.Title != want.Title || s.AgentName != want.AgentName || s.Dir != want.Dir {
			t.Errorf("session %q = %+v, want Title=%q AgentName=%q Dir=%q", id, s, want.Title, want.AgentName, want.Dir)
		}
	}
}

func ids(m map[string]Session) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func state(nameSource, name, template string) string {
	return fmt.Sprintf(`{"name":%q,"nameSource":%q,"template":%q,"intent":"the intent"}`, name, nameSource, template)
}

// TestClaudeJobsRegistry exercises agent-session detection and naming: a job
// with nameSource "auto" marks a background agent (hidden by default, named
// when included); "user" jobs and sessions with no job entry are always shown
// with their real title.
func TestClaudeJobsRegistry(t *testing.T) {
	files := map[string]string{
		"auto1-abc.jsonl": `{"type":"ai-title","aiTitle":"auto titled","sessionId":"auto1-abc"}` + "\n",
		"user1-abc.jsonl": `{"type":"ai-title","aiTitle":"auto titled","sessionId":"user1-abc"}` + "\n",
		"plain-abc.jsonl": `{"type":"ai-title","aiTitle":"interactive stuff","sessionId":"plain-abc"}` + "\n",
	}
	jobs := map[string]string{
		"auto1": state("auto", "wind profile reshuffling gdm", "claude"),
		"user1": state("user", "RES Advisory Project", "claude"),
	}
	projectsRoot, jobsRoot := newHermeticClaudeRoots(t, files, jobs)
	repoDirs := map[string][]string{"/fake/repo": {"/fake/repo"}}

	// Default: agent session hidden, others shown with real titles.
	got := sessionsByID(claudeSessionsFrom(projectsRoot, jobsRoot, repoDirs, Options{}))
	if _, ok := got["auto1-abc"]; ok {
		t.Errorf("auto job session should be hidden by default")
	}
	assertSession(t, got, "user1-abc", &Session{Title: "RES Advisory Project", AgentName: "", Dir: "/fake/repo"})
	assertSession(t, got, "plain-abc", &Session{Title: "interactive stuff", AgentName: "", Dir: "/fake/repo"})

	// With IncludeAgents: agent session appears, named from the job.
	got = sessionsByID(claudeSessionsFrom(projectsRoot, jobsRoot, repoDirs, Options{IncludeAgents: true}))
	assertSession(t, got, "auto1-abc", &Session{Title: "wind profile reshuffling gdm", AgentName: "wind profile reshuffling gdm", Dir: "/fake/repo"})
	assertSession(t, got, "user1-abc", &Session{Title: "RES Advisory Project", AgentName: "", Dir: "/fake/repo"})
}

// TestClaudeTitlePriority verifies title precedence: job name > intent >
// custom-title > ai-title > last prompt > uuid.
func TestClaudeTitlePriority(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		content string
		job     map[string]string // job prefix -> state.json
		want    string
	}{
		{
			name:    "custom beats ai",
			file:    "customx-abc.jsonl",
			content: `{"type":"ai-title","aiTitle":"ai guess","sessionId":"x"}` + "\n" + `{"type":"custom-title","customTitle":"user renamed","sessionId":"x"}` + "\n",
			want:    "user renamed",
		},
		{
			name:    "ai only",
			file:    "aionly-abc.jsonl",
			content: `{"type":"ai-title","aiTitle":"ai guess","sessionId":"x"}` + "\n",
			want:    "ai guess",
		},
		{
			name:    "last prompt only",
			file:    "noprompt-abc.jsonl",
			content: `{"type":"user","message":{"content":"a message"}}` + "\n" + `{"type":"last-prompt","leafUuid":"l","lastPrompt":"make it work"}` + "\n",
			want:    "make it work",
		},
		{
			name:    "last prompt without text falls back to uuid",
			file:    "noprompt-abc.jsonl",
			content: `{"type":"last-prompt","leafUuid":"l","sessionId":"x"}` + "\n",
			want:    "noprompt-abc",
		},
		{
			name:    "no events falls back to uuid",
			file:    "uuidonly-abc.jsonl",
			content: "{}\n{}\n",
			want:    "uuidonly-abc",
		},
		{
			name:    "job intent used when no name",
			file:    "intent-abc.jsonl",
			content: `{"type":"ai-title","leafUuid":"l","title":"ai guess"}` + "\n",
			job:     map[string]string{"intent": `{"name":"","nameSource":"user","template":"claude","intent":"the real task"}`},
			want:    "the real task",
		},
		{
			name:    "legacy title field fallback",
			file:    "legacy-abc.jsonl",
			content: `{"type":"ai-title","leafUuid":"l","title":"legacy guess"}` + "\n" + `{"type":"custom-title","leafUuid":"l","title":"legacy rename"}` + "\n",
			want:    "legacy rename",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projectsRoot, jobsRoot := newHermeticClaudeRoots(t, map[string]string{tc.file: tc.content}, tc.job)
			repoDirs := map[string][]string{"/fake/repo": {"/fake/repo"}}
			got := claudeSessionsFrom(projectsRoot, jobsRoot, repoDirs, Options{IncludeAgents: true})
			if len(got) != 1 {
				t.Fatalf("expected 1 session, got %d: %+v", len(got), got)
			}
			if got[0].Title != tc.want {
				t.Errorf("title = %q, want %q", got[0].Title, tc.want)
			}
		})
	}
}

// TestClaudeJobFor verifies the job lookup uses the session id's first
// '-' segment, and ignores a malformed state.json.
func TestClaudeJobFor(t *testing.T) {
	jobsRoot := t.TempDir()
	dir := filepath.Join(jobsRoot, "deadbeef")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"name":"task","nameSource":"auto"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(jobsRoot, "cafebabe"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobsRoot, "cafebabe", "state.json"), []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}

	st, ok := claudeJobFor(jobsRoot, "deadbeef-2026-07-01T00-00-00-123")
	if !ok || st.Name != "task" || !isAgentJob(st) {
		t.Errorf("claudeJobFor(deadbeef) = %+v, ok=%v", st, ok)
	}
	if _, ok := claudeJobFor(jobsRoot, "cafebabe-xyz"); ok {
		t.Errorf("malformed state.json should not resolve a job")
	}
	if _, ok := claudeJobFor(jobsRoot, "nosuch-xyz"); ok {
		t.Errorf("missing job should not resolve")
	}
	if _, ok := claudeJobFor("", "deadbeef-xyz"); ok {
		t.Errorf("empty jobs root should not resolve")
	}
}

// TestApplyRecencyCap covers the recency window and per-clone cap.
func TestApplyRecencyCap(t *testing.T) {
	now := time.Now()
	mk := func(id string, age time.Duration) Session {
		return Session{ID: id, Updated: now.Add(-age)}
	}
	// Sorted newest-first, as Discover leaves it.
	all := []Session{
		mk("just", time.Minute),
		mk("hour", time.Hour),
		mk("week", 7*24*time.Hour),
		mk("old", 60*24*time.Hour),
	}

	// Recency: 30-day window keeps the three recent ones.
	got := applyRecencyCap(all, 30, 0)
	if len(got) != 3 {
		t.Errorf("recency filter: got %d, want 3 (%v)", len(got), idsSessions(got))
	}
	// Cap: keep the two newest of the kept set.
	got = applyRecencyCap(all, 30, 2)
	if len(got) != 2 || got[0].ID != "just" || got[1].ID != "hour" {
		t.Errorf("cap: got %+v, want [just hour]", got)
	}
	// 0 age / 0 cap: unlimited.
	if got := applyRecencyCap(all, 0, 0); len(got) != 4 {
		t.Errorf("unlimited should keep all, got %d", len(got))
	}
	// Zero timestamps survive the recency window (no mtime data).
	if got := applyRecencyCap([]Session{{ID: "z", Updated: time.Time{}}}, 30, 0); len(got) != 1 {
		t.Errorf("zero-time session should survive recency, got %d", len(got))
	}
}

func idsSessions(ss []Session) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return out
}
