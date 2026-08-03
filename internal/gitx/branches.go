package gitx

import (
	"context"
	"strings"
)

const branchFormat = "%(refname:short)|%(upstream:short)|%(upstream:track)|%(committerdate:relative)|%(subject)|%(worktreepath)"

// ListBranches lists local branches only (no remotes).
func ListBranches(ctx context.Context, repoPath string) ([]Branch, error) {
	out, err := runGit(ctx, repoPath, "branch", "--format="+branchFormat)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}

	var branches []Branch
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		// Subject may contain '|' — split into at most 6 fields.
		parts := strings.SplitN(line, "|", 6)
		if len(parts) < 6 {
			continue
		}
		branches = append(branches, Branch{
			Name:         parts[0],
			Upstream:     parts[1],
			Track:        strings.Trim(parts[2], "[]"),
			Date:         parts[3],
			Subject:      parts[4],
			WorktreePath: parts[5],
		})
	}
	return branches, nil
}
