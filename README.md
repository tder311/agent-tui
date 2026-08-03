# agent-tui

Terminal UI for your local AI coding agents — browse git worktrees, branches, and Claude Code / OpenCode sessions, organized by **project** (with duplicate clones of the same origin collapsed into one).

## Quickstart

```bash
go install github.com/tder311/agent-tui@latest

agent-tui
```

On first run a config file is created at `~/.config/agent-tui/config.json` with defaults, and the TUI scans your home directory for git worktrees, branches, and agent sessions.

## Features

- **Nav tree** — projects → clones → Worktrees / Branches / Agents (live) / Agents sections, collapsible, `/` filter. Worktree rows are tagged `WT` so they don't read as branches; projects with no remote are flagged `local`
- **Worktrees** — branch, kind (main / spawner / other), dirty state, ahead/behind vs upstream, last commit; `o` opens a new terminal tab with the right tool, `d` removes (y/n confirm, `--force` offer when dirty)
- **Branches** — upstream, tracking, checked-out location, last commit; `D` deletes (y/n confirm, `-D` offer when unmerged)
- **Agents** — Claude Code sessions (from `~/.claude/projects`) and OpenCode sessions (from the OpenCode sqlite db, read-only) matched to repos/worktrees by directory, with real titles (renamed/auto-titled names from the session files and the Claude jobs registry) instead of raw UUIDs; `o` opens/resumes the session in a terminal. Sessions launched by *background agents* are hidden by default (`include_agent_sessions: false`) so the historical list stays focused on interactive work — turn it on to see them with a ⚙ badge and their task name
- **Live agents** — the agents actually *running right now*, from the official Claude local API (`claude agents --json`), shown in a distinct **Agents (live)** section with a status dot (busy = green, idle = gray, blocked = yellow) and a `bg`/`fg` (background/interactive) tag; `o` resumes the agent with `claude --resume <sessionId>`. If `claude` isn't on your PATH this section simply doesn't appear — you keep the historical sessions only
- **Pull requests** — open PRs for each `github.com` project (via the `gh` CLI), listed in a **Pull requests** section under the project with state badges (open = green, draft = gray, merged/closed faded), author, head → base refs, and +/- additions/deletions; `o` opens a PR in your browser. Fetched once per project and shared across its clones, so N clones cost one `gh` call. If `gh` isn't installed or a repo has no remote, the section just doesn't appear
- **Prune** — `p` runs `git worktree prune` per repo (with confirm)
- **Auto-refresh** — background rescan every 10s (configurable) plus `r` manual refresh; all git/fs I/O runs off the UI thread with timeouts
- **Help** — `?` for full keybindings

## How discovery works

Worktree discovery is **app-agnostic** — agent-tui doesn't hardcode where any tool puts its worktrees. Instead it sweeps its configured scan roots (default `~`) for `.git` markers: a `.git` directory for a main checkout, or a `.git` file (`gitdir: <path>`) for every linked worktree — the same marker git itself uses. Anything git considers a worktree is found, no matter which app created it (Claude Code, Conductor, OpenCode, or something unregistered).

Markers are resolved to their repo's git common dir and grouped, so a repo's main checkout and every linked worktree land under one node — including worktrees-of-worktrees and worktrees of bare repos. Submodule markers are filtered out. Per-worktree state (branch, dirty, ahead/behind, last commit) is read individually with short `git` probes, so unknown apps' worktrees get full detail even if git's administrative registry is stale; `git worktree list` is used only as supplementary locked/prunable info.

### Projects

Clones of the same upstream are grouped into a single **project**. Grouping is by the repo's `origin` URL, normalized so these all count as the same project:

- `git@github.com:modoenergy/monodo.git`
- `ssh://git@github.com/modoenergy/monodo`
- `https://github.com/modoenergy/monodo`

(`.git` suffixes, trailing slashes, ports, scheme and host casing are all folded; the repo name for display is taken from the URL.) Expand a project to see each clone, labelled by its parent dir (`~/repos`, `~/conductor/repos`) — when two clones share a parent, the full path is shown instead. Repos with **no remote** get a `local:<path>` identity, show up as their own project flagged `local`, and are easy to exclude via `skip`.

Selecting a **project** shows an overview pane: its origin URL, aggregate worktree/branch/session/live-agent/PR counts across all clones, a compact per-clone summary line, the live agents running anywhere in the project, and its open pull requests.

### Data sources

- Sweep of configured `scan_roots` (default `~`, minus the default skip list) for `.git` markers — the source of truth for repos and worktrees
- `git status --short --branch`, `git rev-parse --short HEAD`, `git log -1`, `git branch` per worktree/repo
- `git worktree list --porcelain` per repo — supplementary (locked / prunable)
- `~/.claude/projects/<slug>/*.jsonl` — Claude Code sessions (mtime = last activity, lines = messages; titles from `custom-title`/`ai-title`/last-prompt events)
- `~/.claude/jobs/<id>/state.json` — the Claude jobs registry: identifies background-agent sessions (`nameSource: "auto"`) and carries their real task name. Used to hide agent sessions by default and to name sessions the user renamed (`nameSource: "user"`)
- `claude agents --json` — **live** Claude Code agents (background + interactive), attributed to the deepest known worktree (agents launched from a shared parent like `~/.claude/worktrees` resolve to the owning clone). Best-effort: missing `claude` or a nonzero exit yields zero live agents, never an error
- `gh pr list` per distinct github.com project origin — open pull requests (best-effort: missing `gh`, nonzero exit, or a non-github origin yields no PRs, never an error)
- `~/.local/share/opencode/opencode.db` — OpenCode sessions (opened read-only; skipped gracefully if unavailable)
- `~/.config/opencode/orchestrator/repos.json` — only used to add an `opencode` spawner for the orchestrator's worktree root

Sessions are inherently app-specific and best-effort; they never affect discovery. A worktree always shows up even when no session data exists for its app.

agent-tui is read-only by default: scanning never mutates any repo. The only mutations are the explicit `d` / `D` / `p` actions, each behind a y/n confirm.

## Configuration

`~/.config/agent-tui/config.json`:

```json
{
  "terminal": "auto",
  "scan_roots": ["~"],
  "skip": ["~/Library", "~/.Trash", "node_modules", "target"],
  "spawners": {
    "claude":    { "pattern": "/.claude/worktrees/", "cli": "claude", "color": "orange" },
    "conductor": { "pattern": "/conductor/workspaces/", "cli": "claude", "color": "purple" }
  },
  "include_agent_sessions": false,
  "session_days": 30,
  "session_cap": 50,
  "refresh_seconds": 10
}
```

- `terminal` — terminal app for the `o` action: `auto` (iTerm2 if installed, else Terminal.app), `terminal`, or `iterm2`
- `scan_roots` — directories swept for `.git` markers; default `["~"]`
- `skip` — directory patterns excluded from the sweep. A pattern with a path separator matches that absolute path (the whole subtree is skipped); a bare name matches any directory of that name at any depth. Defaults skip `~/Library`, `~/.Trash`, `~/.cache`, `~/.npm`, `~/.yarn`, `~/.claude`, `~/Downloads`, `~/Music`, `~/Movies`, `~/Pictures`, `node_modules`, `.git`, `__pycache__`, `.venv`, `venv`, `.tox`, `.pytest_cache`, `.gradle`, `.m2`, `.cargo`, `.rustup`, `.pkg`, `target`, `.next`, `.dart_tool`, plus the bare names `.codex`, `.nvm`, `.cache`, `.Trash` (toolchains and app caches that never hold repos worth browsing — `.codex` also hides the transient `.codex/.tmp/*` backup noise)
- `spawners` — label/color/CLI mapping for worktrees by path substring. Unknown worktrees are shown as neutral `other` (plain shell on `o`). `claude` and `conductor` are built in; the `opencode` worktree root is matched automatically from the orchestrator registry when present. Add your own apps without code changes
- `include_agent_sessions` — list Claude sessions launched by background agents (detected via the jobs registry) alongside interactive ones. Default `false` keeps the historical Agents list free of agent noise; `true` shows them with a ⚙ badge and their task name
- `session_days` — only show sessions updated within this many days (default `30`; `0` = unlimited)
- `session_cap` — cap historical sessions per clone, most recent first (default `50`; `0` = unlimited)
- `refresh_seconds` — auto-refresh interval (minimum 2)

Legacy `scan_root` and `repo_roots` settings are still honored and merged into `scan_roots`.

## Build from source

```bash
git clone https://github.com/tder311/agent-tui
cd agent-tui
go build -o agent-tui .
```

## License

MIT
