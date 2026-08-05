package goalgraph

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	workspaceStateTimeout = 10 * time.Second
	workspaceStateLimit   = 128 << 20
)

// WorkspaceStateToken binds combined-workspace verification to the current
// Git repository state. HEAD represents unchanged tracked bytes; deterministic
// binary diffs represent index and worktree changes; untracked non-ignored
// files are hashed with their paths, modes, and contents. Ignored build output
// is deliberately excluded. In-process potentially mutating actions also
// advance the graph's mutation generation, so a later action stales evidence
// even when it touched only ignored output.
func WorkspaceStateToken(ctx context.Context, workspace string) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", errors.New("workspace path is empty")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return "", errors.New("git is not installed or not in PATH")
	}
	runCtx, cancel := context.WithTimeout(ctx, workspaceStateTimeout)
	defer cancel()
	root, err := gitOutput(runCtx, workspace, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("workspace is not a Git repository: %w", err)
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("Git returned an empty repository root")
	}
	h := sha256.New()
	limited := &limitedHash{Hash: h, remaining: workspaceStateLimit}
	writeField(limited, "root", filepath.Clean(root))
	head, headErr := gitOutput(runCtx, root, "rev-parse", "--verify", "HEAD")
	if headErr != nil {
		head = "unborn"
	}
	writeField(limited, "head", strings.TrimSpace(head))
	// The first diff is always worktree-versus-index and therefore also works
	// before the repository has its first commit. The second is index-versus-
	// HEAD; omit HEAD for an unborn repository so staged bytes remain covered.
	commands := [][]string{{"diff", "--binary", "--no-ext-diff", "--"}}
	if strings.TrimSpace(head) == "unborn" {
		commands = append(commands, []string{"diff", "--cached", "--binary", "--no-ext-diff", "--"})
	} else {
		commands = append(commands, []string{"diff", "--cached", "--binary", "--no-ext-diff", "HEAD", "--"})
	}
	for _, command := range commands {
		writeField(limited, "command", strings.Join(command, " "))
		if err := gitToWriter(runCtx, root, limited, command...); err != nil {
			return "", err
		}
	}
	if err := hashIndex(runCtx, root, limited); err != nil {
		return "", err
	}
	if err := hashUntracked(runCtx, root, limited); err != nil {
		return "", err
	}
	if limited.err != nil {
		return "", limited.err
	}
	return "workspace-" + hex.EncodeToString(h.Sum(nil)), nil
}

func hashIndex(ctx context.Context, root string, out io.Writer) error {
	writeField(out, "command", "ls-files --stage -z")
	return gitToWriter(ctx, root, out, "ls-files", "--stage", "-z")
}

func hashUntracked(ctx context.Context, root string, out io.Writer) error {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--others", "--exclude-standard", "-z")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	reader := bufio.NewReader(stdout)
	for {
		raw, readErr := reader.ReadString(0)
		if len(raw) > 0 {
			rel := strings.TrimSuffix(raw, "\x00")
			if rel != "" {
				path := filepath.Join(root, filepath.FromSlash(rel))
				info, statErr := os.Lstat(path)
				if statErr != nil {
					_ = cmd.Wait()
					return fmt.Errorf("inspect untracked file %s: %w", rel, statErr)
				}
				if !info.Mode().IsRegular() {
					_ = cmd.Wait()
					return fmt.Errorf("untracked path %s is not a regular file", rel)
				}
				writeField(out, "untracked", rel)
				writeField(out, "mode", fmt.Sprintf("%o", info.Mode().Perm()))
				file, openErr := os.Open(path)
				if openErr != nil {
					_ = cmd.Wait()
					return fmt.Errorf("read untracked file %s: %w", rel, openErr)
				}
				_, copyErr := io.Copy(out, file)
				closeErr := file.Close()
				if copyErr != nil {
					_ = cmd.Wait()
					return copyErr
				}
				if closeErr != nil {
					_ = cmd.Wait()
					return closeErr
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				_ = cmd.Wait()
				return readErr
			}
			break
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("list untracked files: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	var output strings.Builder
	limited := &limitedStringWriter{builder: &output, remaining: 2 << 20}
	cmd.Stdout, cmd.Stderr = limited, limited
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(output.String()))
	}
	if limited.err != nil {
		return "", limited.err
	}
	return output.String(), nil
}

func gitToWriter(ctx context.Context, root string, out io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	var stderr strings.Builder
	cmd.Stdout, cmd.Stderr = out, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func writeField(w io.Writer, name, value string) {
	_, _ = fmt.Fprintf(w, "%d:%s=%d:%s\x00", len(name), name, len(value), value)
}

type limitedHash struct {
	hash.Hash
	remaining int64
	err       error
}

func (w *limitedHash) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if int64(len(p)) > w.remaining {
		w.err = fmt.Errorf("combined workspace state exceeds the %d MiB hashing bound", workspaceStateLimit>>20)
		return 0, w.err
	}
	w.remaining -= int64(len(p))
	return w.Hash.Write(p)
}

type limitedStringWriter struct {
	builder   *strings.Builder
	remaining int
	err       error
}

func (w *limitedStringWriter) Write(p []byte) (int, error) {
	original := len(p)
	if w.err != nil {
		return 0, w.err
	}
	if len(p) > w.remaining {
		p = p[:max(0, w.remaining)]
		w.err = errors.New("Git state inspection exceeded its output bound")
	}
	w.remaining -= len(p)
	_, _ = w.builder.Write(p)
	return original, nil
}
