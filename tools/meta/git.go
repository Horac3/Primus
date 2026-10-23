package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Git reads the staged state. Implementations must not change the index or HEAD.
type Git interface {
	StagedDiff(ctx context.Context) (string, error)
	Changes(ctx context.Context) ([]FileChange, error)
}

// RealGit runs read-only `git diff --cached` commands in Dir (the current
// directory when empty).
type RealGit struct{ Dir string }

func (g RealGit) StagedDiff(ctx context.Context) (string, error) {
	return g.output(ctx, "diff", "--cached", "--no-renames", "--no-color", "--no-ext-diff", "--no-textconv")
}

func (g RealGit) Changes(ctx context.Context) ([]FileChange, error) {
	status, err := g.output(ctx, "diff", "--cached", "--no-renames", "--name-status")
	if err != nil {
		return nil, err
	}
	numstat, err := g.output(ctx, "diff", "--cached", "--no-renames", "--numstat")
	if err != nil {
		return nil, err
	}
	return parseChanges(status, numstat), nil
}

func (g RealGit) output(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.quotepath=false"}, args...)...)
	cmd.Dir = g.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// parseChanges joins `--name-status` lines ("M\tpath") with `--numstat` lines
// ("added\tdeleted\tpath"). Binary files report "-" counts and become zero.
func parseChanges(nameStatus, numstat string) []FileChange {
	counts := map[string][2]int{}
	for _, line := range strings.Split(numstat, "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		added, _ := strconv.Atoi(fields[0])
		deleted, _ := strconv.Atoi(fields[1])
		counts[fields[2]] = [2]int{added, deleted}
	}
	var changes []FileChange
	for _, line := range strings.Split(nameStatus, "\n") {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 || fields[0] == "" {
			continue
		}
		c := counts[fields[1]]
		changes = append(changes, FileChange{Status: fields[0][:1], Path: fields[1], Added: c[0], Deleted: c[1]})
	}
	return changes
}
