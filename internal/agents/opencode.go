package agents

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// opencodeDBPath returns ~/.local/share/opencode/opencode.db.
func opencodeDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db")
}

// opencodeSessions reads sessions from the OpenCode sqlite db, read-only.
// Any failure (missing db, locked, schema drift) yields nil — the rest of the
// app carries on without OpenCode session data.
func opencodeSessions() []Session {
	dbPath := opencodeDBPath()
	if dbPath == "" {
		return nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}

	sessions, err := queryOpenCode(dbPath, "mode=ro")
	if err != nil {
		// WAL read-only can fail without the -shm file; try immutable as fallback.
		sessions, err = queryOpenCode(dbPath, "immutable=1")
		if err != nil {
			return nil
		}
	}
	return sessions
}

func queryOpenCode(dbPath, mode string) ([]Session, error) {
	dsn := fmt.Sprintf("file:%s?%s", dbPath, mode)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx,
		`SELECT id, directory, title, time_updated FROM session WHERE time_archived IS NULL OR time_archived = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var s Session
		var updatedMs int64
		if err := rows.Scan(&s.ID, &s.Dir, &s.Title, &updatedMs); err != nil {
			return out, nil // schema drift — return what we have
		}
		s.Tool = "opencode"
		s.Updated = time.UnixMilli(updatedMs)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	counts := opencodeMessageCounts(ctx, db)
	for i := range out {
		out[i].Messages = counts[out[i].ID]
	}
	return out, nil
}

func opencodeMessageCounts(ctx context.Context, db *sql.DB) map[string]int {
	counts := make(map[string]int)
	rows, err := db.QueryContext(ctx, `SELECT session_id, COUNT(*) FROM message GROUP BY session_id`)
	if err != nil {
		return counts
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err == nil {
			counts[id] = n
		}
	}
	return counts
}
