// Package workspace provides bounded, read-only inspection of the active
// workspace for user-interface status surfaces.
package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const gitStatusOutputLimit = 2 * 1024 * 1024

// GitStatus is a point-in-time repository summary. A path outside a Git
// repository is a normal state (InRepository=false), not a command failure.
type GitStatus struct {
	InRepository bool
	Branch       string
	Upstream     string
	Ahead        int
	Behind       int
	Staged       int
	Modified     int
	Untracked    int
	Conflicted   int
	Error        string
}

// InspectGit runs one shell-free, read-only status command. The short timeout
// keeps slow network filesystems and broken Git installations from stalling
// an interactive terminal.
func InspectGit(ctx context.Context, root string) GitStatus {
	if _, err := exec.LookPath("git"); err != nil {
		return GitStatus{Error: "git is not installed or not in PATH"}
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", "-C", root, "--no-pager", "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	output := &limitedBuffer{limit: gitStatusOutputLimit}
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	if runCtx.Err() != nil {
		return GitStatus{Error: "git status timed out"}
	}
	if err != nil {
		message := strings.TrimSpace(output.String())
		if strings.Contains(strings.ToLower(message), "not a git repository") {
			return GitStatus{}
		}
		if message == "" {
			message = err.Error()
		}
		return GitStatus{Error: compactError(message)}
	}
	if output.truncated {
		return GitStatus{InRepository: true, Error: "git status exceeded the 2 MiB inspection limit"}
	}
	status, parseErr := ParseGitStatus(output.String())
	if parseErr != nil {
		status.Error = parseErr.Error()
	}
	return status
}

type limitedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.truncated = true
		return original, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = b.Buffer.Write(p)
	return original, nil
}

// ParseGitStatus parses `git status --porcelain=v2 --branch`. It is exported
// so every supported platform can verify identical behavior without needing
// a Git executable in the test environment.
func ParseGitStatus(output string) (GitStatus, error) {
	status := GitStatus{InRepository: true}
	for _, raw := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			status.Branch = strings.TrimPrefix(line, "# branch.head ")
			if status.Branch == "(detached)" {
				status.Branch = "detached HEAD"
			}
		case strings.HasPrefix(line, "# branch.upstream "):
			status.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			fields := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			if len(fields) != 2 {
				return status, fmt.Errorf("unrecognized Git ahead/behind status")
			}
			var err error
			if status.Ahead, err = signedCount(fields[0]); err != nil {
				return status, err
			}
			if status.Behind, err = signedCount(fields[1]); err != nil {
				return status, err
			}
		case strings.HasPrefix(line, "? "):
			status.Untracked++
		case strings.HasPrefix(line, "u "):
			status.Conflicted++
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "):
			fields := strings.Fields(line)
			if len(fields) < 2 || len(fields[1]) != 2 {
				return status, errors.New("unrecognized Git file status")
			}
			if fields[1][0] != '.' {
				status.Staged++
			}
			if fields[1][1] != '.' {
				status.Modified++
			}
		}
	}
	if status.Branch == "" {
		status.Branch = "unknown"
	}
	return status, nil
}

func signedCount(value string) (int, error) {
	count, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("unrecognized Git ahead/behind count %q", value)
	}
	if count < 0 {
		count = -count
	}
	return count, nil
}

func compactError(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	const limit = 240
	if len(value) > limit {
		return value[:limit] + "…"
	}
	return value
}
