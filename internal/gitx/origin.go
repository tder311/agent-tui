package gitx

import (
	"net/url"
	"path/filepath"
	"strings"
)

// Origin is the normalized identity of a repository's origin remote (or a
// marker for repositories with no real remote). Normalization folds the common
// URL forms — scp-like (git@host:path), ssh://, https, git://, with or without
// port, .git suffix or trailing slash — down to "host/owner/repo", so clones of
// the same upstream project group under one Project node.
type Origin struct {
	HasRemote bool   // a real (non-local-path) remote; false => local project
	Identity  string // "host/owner/repo" (lowercase) or "local:<path>"
	Slug      string // repo slug for display (lowercase for remote origins)
	Host      string // lowercase host, "" for local origins
}

// NormalizeOrigin parses a git remote URL into its normalized identity. The
// three returns are the identity, host, and display slug. For a remote origin
// remote is true; for a local filesystem path (e.g. an origin pointing at a
// local bare clone) remote is false and identity is "local:<path>" so clones
// of the same local repo still dedup. Returns ok=false for empty/unparseable
// inputs (the caller falls back to a unique local identity for the repo).
func NormalizeOrigin(raw string) (identity, host, slug string, remote, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", "", false, false
	}

	var p string
	if i := strings.Index(raw, "://"); i >= 0 {
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", "", false, false
		}
		host = u.Hostname()
		p = strings.TrimPrefix(u.Path, "/")
	} else if at := strings.LastIndex(raw, "@"); at >= 0 {
		// scp-like: [user@]host:path
		rest := raw[at+1:]
		colon := strings.Index(rest, ":")
		if colon <= 0 {
			return "", "", "", false, false
		}
		host = rest[:colon]
		p = rest[colon+1:]
	} else {
		// host:path without a user, or a local filesystem path.
		colon := strings.Index(raw, ":")
		slash := strings.Index(raw, "/")
		if colon > 0 && !strings.HasPrefix(raw, "/") && (slash < 0 || slash > colon) {
			host = raw[:colon]
			p = raw[colon+1:]
		} else {
			cleaned := filepath.Clean(raw)
			return "local:" + cleaned, "", filepath.Base(cleaned), false, true
		}
	}

	host = strings.ToLower(host)
	p = strings.TrimSuffix(p, "/")
	if strings.HasSuffix(p, ".git") {
		p = p[:len(p)-4]
	}
	p = strings.ToLower(p)
	if host == "" || p == "" {
		return "", "", "", false, false
	}
	segs := strings.Split(p, "/")
	if len(segs) < 2 {
		return "", "", "", false, false
	}
	return host + "/" + p, host, segs[len(segs)-1], true, true
}

// localIdentity is the fallback identity for a repository with no (parseable)
// remote: unique per checkout, marked local so it can't merge with other repos.
func localIdentity(repoPath string) Origin {
	return Origin{Identity: "local:" + canonicalPath(repoPath), Slug: filepath.Base(repoPath)}
}
