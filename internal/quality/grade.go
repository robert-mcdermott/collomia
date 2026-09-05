package quality

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/tools"
)

// readOutput confines grader reads even if generated code replaces an output
// with a symlink. Graders never execute a model-supplied command string.
func readOutput(workspace, path string) ([]byte, error) {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return nil, fmt.Errorf("output must be a regular file at most 4 MiB")
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4<<20+1))
	if len(data) > 4<<20 {
		return nil, fmt.Errorf("output exceeded 4 MiB")
	}
	return data, err
}

func grade(ctx context.Context, task Task, workspace, answer string, receipts map[string]string, command *tools.RunCommandTool) []CheckResult {
	var results []CheckResult
	paths := make([]string, 0, len(task.Files))
	for path := range task.Files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		if slices.Contains(task.Editable, path) {
			continue
		}
		data, err := readOutput(workspace, path)
		results = append(results, CheckResult{Name: "preserve input " + path, Passed: err == nil && string(data) == task.Files[path]})
	}
	for _, check := range task.Checks {
		result := CheckResult{Name: check.Kind + " " + check.Path}
		var data []byte
		var err error
		if check.Path != "" && check.Kind != "absent" {
			data, err = readOutput(workspace, check.Path)
		}
		if err != nil {
			result.Detail = "missing or unreadable output"
			results = append(results, result)
			continue
		}
		switch check.Kind {
		case "answer_contains":
			result.Passed = strings.Contains(strings.ToLower(answer), strings.ToLower(check.Want))
		case "file_contains":
			result.Passed = strings.Contains(strings.ToLower(string(data)), strings.ToLower(check.Want))
		case "file_not_contains":
			result.Passed = !strings.Contains(strings.ToLower(string(data)), strings.ToLower(check.Want))
		case "file_exact":
			result.Passed = strings.TrimSpace(string(data)) == check.Want
		case "absent":
			_, err = os.Lstat(filepath.Join(workspace, check.Path))
			result.Passed = os.IsNotExist(err)
		case "json_equal":
			var actual, want any
			result.Passed = json.Unmarshal(data, &actual) == nil && json.Unmarshal([]byte(check.Want), &want) == nil && reflect.DeepEqual(actual, want)
		case "receipt":
			hash := sha256.Sum256(data)
			result.Passed = receipts[check.Path] == "sha256:"+hex.EncodeToString(hash[:])
		case "no_changes":
			result.Passed = true
			err = filepath.WalkDir(workspace, func(path string, entry os.DirEntry, walkErr error) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if walkErr != nil {
					return walkErr
				}
				relative, _ := filepath.Rel(workspace, path)
				if entry.IsDir() {
					if relative == ".collo-cache" {
						return filepath.SkipDir
					}
					return nil
				}
				if _, exists := task.Files[filepath.ToSlash(relative)]; !exists {
					result.Passed = false
				}
				return nil
			})
			if err != nil {
				result.Passed = false
			}
		case "go_test":
			root, openErr := os.OpenRoot(workspace)
			if openErr != nil {
				err = openErr
			} else {
				// Never follow a model-created symlink or overwrite a submitted
				// test with the grader's trusted source.
				var file *os.File
				file, err = root.OpenFile("collo_acceptance_test.go", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
				if err == nil {
					_, err = file.Write([]byte(check.Want))
					if closeErr := file.Close(); err == nil {
						err = closeErr
					}
				}
				root.Close()
			}
			if err == nil {
				raw, _ := json.Marshal(map[string]any{"command": goCheckCommand(workspace), "timeout_seconds": 45})
				output, runErr := command.Execute(ctx, raw)
				err = runErr
				result.Detail = clip(output, 2000)
			}
			result.Passed = err == nil
			if err != nil {
				result.Detail += " acceptance test failed"
			}
		default:
			result.Detail = "unknown check"
		}
		results = append(results, result)
	}
	return results
}

func goCheckCommand(workspace string) string {
	cache := filepath.Join(workspace, ".collo-cache")
	if runtime.GOOS == "windows" {
		return `set "GOCACHE=` + cache + `" && set "GOTOOLCHAIN=local" && go test -count=1 ./...`
	}
	return "GOCACHE='" + strings.ReplaceAll(cache, "'", "'\"'\"'") + "' GOTOOLCHAIN=local go test -count=1 ./..."
}

func clip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && (text[limit]&0xc0) == 0x80 {
		limit--
	}
	return text[:limit] + "…"
}
