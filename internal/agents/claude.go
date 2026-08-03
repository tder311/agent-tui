package agents

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// claudeProjectsDir returns ~/.claude/projects.
func claudeProjectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// claudeJobsDir returns ~/.claude/jobs, the registry that records which
// sessions were launched by a background agent (state.json per job).
func claudeJobsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "jobs")
}

// claudeSlug converts an absolute cwd to Claude Code's project-dir slug:
// every character outside [a-zA-Z0-9-] becomes '-' (so '/', '.', '_', '+',
// spaces etc. all map to '-'; consecutive separators are NOT collapsed).
func claudeSlug(dir string) string {
	var b strings.Builder
	b.Grow(len(dir))
	for _, r := range dir {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// claudeSessions scans ~/.claude/projects/<slug>/*.jsonl and attributes each
// project dir to a discovered repo dir. Sessions launched by a background
// agent (jobs registry) are dropped unless opts.IncludeAgents is set, in which
// case they carry AgentName. Titles come from the jobs registry name when
// present (renamed interactive sessions and agent task names), else from the
// session file (custom-title > ai-title > last prompt), else the UUID.
// Attribution first tries an exact slug match against known dirs (repo paths +
// worktree paths), then falls back to the longest known dir whose slug is a
// '-'-delimited prefix of the project dir (sessions launched from
// subdirectories of a repo/worktree). Sessions are identified by file name;
// last activity is the file mtime; message count is the line count.
func claudeSessions(repoDirs map[string][]string, opts Options) []Session {
	return claudeSessionsFrom(claudeProjectsDir(), claudeJobsDir(), repoDirs, opts)
}

// claudeSessionsFrom is claudeSessions with injectable roots, for hermetic
// tests.
func claudeSessionsFrom(projectsRoot, jobsRoot string, repoDirs map[string][]string, opts Options) []Session {
	if projectsRoot == "" {
		return nil
	}
	projDirs, err := os.ReadDir(projectsRoot)
	if err != nil {
		return nil
	}

	// Known dirs sorted by slug length, longest first, so exact matches and
	// the most specific prefix win.
	var known []knownDir
	seen := map[string]bool{}
	for _, dirs := range repoDirs {
		for _, d := range dirs {
			if d == "" || seen[d] {
				continue
			}
			seen[d] = true
			known = append(known, knownDir{slug: claudeSlug(d), dir: d})
		}
	}
	sort.Slice(known, func(i, j int) bool { return len(known[i].slug) > len(known[j].slug) })

	var out []Session
	for _, pd := range projDirs {
		if !pd.IsDir() {
			continue
		}
		slug := pd.Name()
		dir := resolveSlug(slug, known)
		if dir == "" {
			continue
		}
		files, err := os.ReadDir(filepath.Join(projectsRoot, slug))
		if err != nil {
			continue
		}
		for _, e := range files {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			id := strings.TrimSuffix(e.Name(), ".jsonl")
			info, err := e.Info()
			if err != nil {
				continue
			}

			st, hasJob := claudeJobFor(jobsRoot, id)
			var agentName string
			if hasJob && isAgentJob(st) {
				if !opts.IncludeAgents {
					continue // background-agent sessions are hidden by default
				}
				agentName = jobTitle(st)
			}

			lines, scanned := scanClaudeFile(filepath.Join(projectsRoot, slug, e.Name()))
			title := ""
			if hasJob {
				title = jobTitle(st)
			}
			if title == "" {
				title = scanned
			}
			if title == "" {
				title = id
			}

			out = append(out, Session{
				Tool:      "claude",
				ID:        id,
				Title:     title,
				AgentName: agentName,
				Dir:       dir,
				Updated:   info.ModTime(),
				Messages:  lines,
			})
		}
	}
	return out
}

type knownDir struct {
	slug string
	dir  string
}

// resolveSlug maps a project-dir slug back to a known directory. Exact match
// first; otherwise the longest known dir whose slug is a '-'-delimited prefix.
// known must be sorted by slug length descending.
func resolveSlug(slug string, known []knownDir) string {
	for _, k := range known {
		if k.slug == slug {
			return k.dir
		}
	}
	for _, k := range known {
		if strings.HasPrefix(slug, k.slug+"-") {
			return k.dir
		}
	}
	return ""
}

// withConductorVirtualDirs augments repoDirs so claude sessions whose cwd was
// a Conductor workspace attribute to the owning Conductor clone. Each clone
// ~/conductor/repos/<key> gains a virtual dir ~/conductor/workspaces/<key>:
// live workspace slugs still exact-match their worktree dir (longer slug
// wins), while stale/root workspace slugs (e.g.
// -Users-tomderrick-conductor-workspaces-monodo-abuja for a deleted
// workspace) fall back to the virtual dir via longest-prefix and resolve to
// the clone. No-op when Conductor isn't installed.
func withConductorVirtualDirs(repoDirs map[string][]string) map[string][]string {
	home, err := os.UserHomeDir()
	if err != nil {
		return repoDirs
	}
	reposRoot := filepath.Join(home, "conductor", "repos")
	wsRoot := filepath.Join(home, "conductor", "workspaces")
	if _, err := os.Stat(wsRoot); err != nil {
		return repoDirs
	}
	reposRoot = filepath.Clean(reposRoot)

	out := make(map[string][]string, len(repoDirs))
	for repo, dirs := range repoDirs {
		out[repo] = append([]string{}, dirs...)
	}
	for repo := range out {
		if !dirWithin(repo, reposRoot) {
			continue
		}
		virtual := filepath.Join(wsRoot, filepath.Base(repo))
		out[repo] = append(out[repo], virtual)
	}
	return out
}

// claudeJobState mirrors the subset of ~/.claude/jobs/<id>/state.json used to
// recognize background-agent sessions and name them.
type claudeJobState struct {
	Template   string `json:"template"`
	Name       string `json:"name"`
	NameSource string `json:"nameSource"`
	Intent     string `json:"intent"`
	SessionID  string `json:"sessionId"`
	Cwd        string `json:"cwd"`
}

// isAgentJob reports whether a job entry belongs to a background agent rather
// than an interactive session. The jobs registry only records sessions that
// were named: interactive ones carry nameSource "user", spawned background
// agents "auto". Template alone is not a reliable discriminator — interactive
// sessions carry it too.
func isAgentJob(st claudeJobState) bool {
	return st.NameSource == "auto"
}

// claudeJobFor reads the job state for a session (job dir = the session id's
// first '-' segment). ok=false when no entry exists.
func claudeJobFor(jobsRoot, sessionID string) (st claudeJobState, ok bool) {
	if jobsRoot == "" || sessionID == "" {
		return claudeJobState{}, false
	}
	prefix := strings.SplitN(sessionID, "-", 2)[0]
	data, err := os.ReadFile(filepath.Join(jobsRoot, prefix, "state.json"))
	if err != nil {
		return claudeJobState{}, false
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return claudeJobState{}, false
	}
	return st, true
}

// jobTitle returns the free-form name a job entry carries — the user's renamed
// title or the agent's task name — falling back to the intent prompt.
func jobTitle(st claudeJobState) string {
	if st.Name != "" {
		return st.Name
	}
	return st.Intent
}

// maxTitleScanLines bounds how far into a session file we search for
// title-setting events (custom-title / ai-title), which always land in the
// opening messages; the file tail is separately checked for the last prompt.
const (
	maxTitleScanLines = 500
	titleTailLines    = 20
)

// scanClaudeFile returns the line count (message count) and the best title
// found in a session jsonl: the first custom-title wins, else the first
// ai-title, else the last prompt's text. Title events are tiny; user/assistant
// message lines are only substring-checked, never JSON-decoded, so multi-MB
// lines stay cheap.
func scanClaudeFile(path string) (lines int, title string) {
	f, err := os.Open(path)
	if err != nil {
		return 0, ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)

	custom, ai := "", ""
	var tail []string
	for sc.Scan() {
		line := sc.Bytes()
		lines++
		if lines <= maxTitleScanLines {
			switch {
			case bytes.Contains(line, []byte(`"type":"custom-title"`)):
				if custom == "" {
					custom = jsonField(line, "customTitle", "title")
				}
			case bytes.Contains(line, []byte(`"type":"ai-title"`)):
				if ai == "" {
					ai = jsonField(line, "aiTitle", "title")
				}
			}
		}
		tail = append(tail, string(line))
		if len(tail) > titleTailLines {
			tail = tail[1:]
		}
	}
	switch {
	case custom != "":
		title = custom
	case ai != "":
		title = ai
	default:
		for _, l := range tail {
			if bytes.Contains([]byte(l), []byte(`"type":"last-prompt"`)) {
				if p := jsonField([]byte(l), "lastPrompt"); p != "" {
					title = p
					break
				}
			}
		}
	}
	return lines, title
}

// jsonField extracts the string value of the first present key from a JSON
// object line without a full decode. Returns "" when absent or malformed.
func jsonField(line []byte, keys ...string) string {
	for _, key := range keys {
		needle := []byte(`"` + key + `":`)
		i := bytes.Index(line, needle)
		if i < 0 {
			continue
		}
		rest := bytes.TrimLeft(line[i+len(needle):], " \t")
		if len(rest) == 0 || rest[0] != '"' {
			continue
		}
		rest = rest[1:]
		var b strings.Builder
		for j := 0; j < len(rest); j++ {
			c := rest[j]
			if c == '\\' {
				if j+1 < len(rest) {
					b.WriteByte(rest[j+1])
					j++
				}
				continue
			}
			if c == '"' {
				return b.String()
			}
			b.WriteByte(c)
		}
	}
	return ""
}
