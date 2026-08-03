package gitx

import (
	"context"
	"strings"
	"time"
)

const actionTimeout = 30 * time.Second

// RemoveWorktree removes a worktree. When force is false git refuses to
// remove dirty worktrees; ErrDirty is returned so the caller can offer a
// second, force-confirmed pass.
func RemoveWorktree(ctx context.Context, repoPath, wtPath string, force bool) error {
	ctx, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, wtPath)
	_, err := runGit(ctx, repoPath, args...)
	if err != nil && isDirtyErr(err) {
		return ErrDirty
	}
	return err
}

// ErrDirty signals the worktree has uncommitted/untracked changes and needs --force.
var ErrDirty = errString("worktree is dirty")

// ErrUnmerged signals the branch is not fully merged and needs -D.
var ErrUnmerged = errString("branch is not fully merged")

type errString string

func (e errString) Error() string { return string(e) }

func isDirtyErr(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "contains modified or untracked files") ||
		strings.Contains(msg, "is dirty")
}

// DeleteBranch deletes a local branch. force=false maps to `git branch -d`
// (refuses unmerged branches → ErrUnmerged); force=true maps to `-D`.
func DeleteBranch(ctx context.Context, repoPath, branch string, force bool) error {
	ctx, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := runGit(ctx, repoPath, "branch", flag, branch)
	if err != nil && strings.Contains(err.Error(), "not fully merged") {
		return ErrUnmerged
	}
	return err
}

// PruneWorktrees runs `git worktree prune` (removes administrative files for
// worktrees whose directories vanished).
func PruneWorktrees(ctx context.Context, repoPath string) error {
	ctx, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	_, err := runGit(ctx, repoPath, "worktree", "prune")
	return err
}
