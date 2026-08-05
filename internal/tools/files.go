package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/robert-mcdermott/collomia/internal/diffmodel"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

const maxReadBytes = 1024 * 1024

func readFileDefinition() json.RawMessage {
	return schema(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace-relative or absolute file path"},"offset":{"type":"integer","minimum":1,"description":"First 1-based line to return"},"limit":{"type":"integer","minimum":1,"maximum":5000}},"required":["path"],"additionalProperties":false}`)
}

type ReadFileTool struct{ Guard *PathGuard }

func (t ReadFileTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "read_file", Description: "Read a UTF-8 text file with line numbers. Use offset and limit for large files. Files larger than 1 MiB must be read in chunks.", InputSchema: readFileDefinition()}
}
func (t ReadFileTool) Assess(raw json.RawMessage) (Action, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, err
	}
	p, o, e := t.Guard.Resolve(a.Path)
	return Action{Risk: RiskRead, Summary: "read " + p, Outside: o, Paths: []string{p}}, e
}
func (t ReadFileTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	p, _, err := t.Guard.Resolve(a.Path)
	if err != nil {
		return "", err
	}
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if a.Offset < 1 {
		a.Offset = 1
	}
	if a.Limit <= 0 {
		a.Limit = 400
	}
	if a.Limit > 5000 {
		a.Limit = 5000
	}
	s := bufio.NewScanner(io.LimitReader(f, maxReadBytes+1))
	s.Buffer(make([]byte, 64*1024), maxReadBytes+1)
	var b strings.Builder
	line := 0
	shown := 0
	for s.Scan() {
		line++
		if line < a.Offset {
			continue
		}
		if shown >= a.Limit {
			break
		}
		fmt.Fprintf(&b, "%6d\t%s\n", line, s.Text())
		shown++
	}
	if err := s.Err(); err != nil {
		return "", err
	}
	if shown == 0 {
		return "(no lines)", nil
	}
	return b.String(), nil
}

type ListFilesTool struct{ Guard *PathGuard }

func (t ListFilesTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "list_files", Description: "List a directory tree without invoking a shell. Hidden source files are included; VCS metadata, dependency trees, build output, caches, virtual environments, and Collomia session data are skipped.", InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string"},"max_depth":{"type":"integer","minimum":1,"maximum":8}},"additionalProperties":false}`)}
}
func (t ListFilesTool) Assess(raw json.RawMessage) (Action, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, err
	}
	p, o, e := t.Guard.Resolve(a.Path)
	return Action{Risk: RiskRead, Summary: "list " + p, Outside: o, Paths: []string{p}}, e
}
func (t ListFilesTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Path     string `json:"path"`
		MaxDepth int    `json:"max_depth"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if a.MaxDepth <= 0 {
		a.MaxDepth = 3
	}
	root, _, err := t.Guard.Resolve(a.Path)
	if err != nil {
		return "", err
	}
	var lines []string
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		depth := len(strings.Split(rel, string(filepath.Separator)))
		if d.IsDir() && (skipGeneratedTree(d.Name()) || d.Name() == "sessions" && filepath.Base(filepath.Dir(p)) == ".collomia") {
			return filepath.SkipDir
		}
		if depth > a.MaxDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		suffix := ""
		if d.IsDir() {
			suffix = "/"
		}
		lines = append(lines, filepath.ToSlash(rel)+suffix)
		if len(lines) >= 5000 {
			return errors.New("listing exceeded 5000 entries; choose a narrower path")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n"), nil
}

type SearchFilesTool struct{ Guard *PathGuard }

func (t SearchFilesTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "search_files", Description: "Search source text using a Go regular expression. Returns matching file paths, line numbers, and text without invoking grep; skips VCS metadata, dependencies, build output, caches, and virtual environments.", InputSchema: schema(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"},"file_glob":{"type":"string","description":"Optional filepath glob such as *.go"},"max_results":{"type":"integer","minimum":1,"maximum":1000}},"required":["pattern"],"additionalProperties":false}`)}
}
func (t SearchFilesTool) Assess(raw json.RawMessage) (Action, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, err
	}
	p, o, e := t.Guard.Resolve(a.Path)
	return Action{Risk: RiskRead, Summary: "search " + p, Outside: o, Paths: []string{p}}, e
}
func (t SearchFilesTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		FileGlob   string `json:"file_glob"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return "", err
	}
	root, _, err := t.Guard.Resolve(a.Path)
	if err != nil {
		return "", err
	}
	if a.MaxResults <= 0 {
		a.MaxResults = 200
	}
	var matches []string
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.IsDir() {
			if skipGeneratedTree(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if a.FileGlob != "" {
			ok, _ := filepath.Match(a.FileGlob, d.Name())
			if !ok {
				return nil
			}
		}
		info, e := d.Info()
		if e != nil || info.Size() > maxReadBytes {
			return nil
		}
		f, e := os.Open(p)
		if e != nil {
			return nil
		}
		s := bufio.NewScanner(f)
		s.Buffer(make([]byte, 64*1024), maxReadBytes)
		line := 0
		for s.Scan() {
			line++
			text := s.Text()
			if strings.IndexByte(text, 0) >= 0 {
				break
			}
			if re.MatchString(text) {
				rel, _ := filepath.Rel(t.Guard.Workspace, p)
				matches = append(matches, fmt.Sprintf("%s:%d:%s", filepath.ToSlash(rel), line, text))
				if len(matches) >= a.MaxResults {
					break
				}
			}
		}
		f.Close()
		if len(matches) >= a.MaxResults {
			return io.EOF
		}
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if len(matches) == 0 {
		return "(no matches)", nil
	}
	return strings.Join(matches, "\n"), nil
}

func skipGeneratedTree(name string) bool {
	switch name {
	case ".git", ".venv", "venv", ".uv-cache", ".pytest_cache", ".mypy_cache", ".ruff_cache", "__pycache__", "node_modules", "vendor", "dist", "build", "target":
		return true
	default:
		return false
	}
}

type WriteFileTool struct {
	Guard   *PathGuard
	Tracker *diffmodel.Tracker
}

func (t WriteFileTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "write_file", Description: "Create or replace a text file. Parent directories are created. Prefer edit_file for focused changes to an existing file.", InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)}
}
func (t WriteFileTool) Assess(raw json.RawMessage) (Action, error) {
	var a struct{ Path, Content string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, err
	}
	p, o, e := t.Guard.Resolve(a.Path)
	action := Action{Risk: RiskWrite, Summary: "write " + p, Outside: o, Paths: []string{p}}
	if e == nil {
		before := ""
		if data, readErr := os.ReadFile(p); readErr == nil {
			before = string(data)
		}
		action.Preview = diffmodel.Unified(displayName(t.Guard.Workspace, p), before, a.Content)
	}
	return action, e
}
func (t WriteFileTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var a struct{ Path, Content string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	target, _, err := t.Guard.MutationTarget(a.Path)
	if err != nil {
		return "", err
	}
	defer target.Close()
	p := target.Path()
	mode := os.FileMode(0o644)
	var before *string
	beforeMode := os.FileMode(0)
	if info, e := target.Stat(); e == nil {
		mode = info.Mode().Perm()
		beforeMode = mode
		data, readErr := target.ReadFile()
		if readErr != nil {
			return "", fmt.Errorf("read existing target before replacement: %w", readErr)
		}
		text := string(data)
		before = &text
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", fmt.Errorf("inspect write target: %w", e)
	}
	if err = target.Replace([]byte(a.Content), mode); err != nil {
		return "", err
	}
	if t.Tracker != nil {
		after := a.Content
		t.Tracker.RecordWithMode(p, "write", before, &after, beforeMode, mode)
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), p), nil
}

func displayName(workspace, path string) string {
	if rel, err := filepath.Rel(workspace, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

type EditFileTool struct {
	Guard   *PathGuard
	Tracker *diffmodel.Tracker
}

func (t EditFileTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "edit_file", Description: "Replace one exact, unique text fragment in a file. The operation fails if old_text is missing or appears more than once, preventing ambiguous edits.", InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"}},"required":["path","old_text","new_text"],"additionalProperties":false}`)}
}
func (t EditFileTool) Assess(raw json.RawMessage) (Action, error) {
	var a struct {
		Path string `json:"path"`
		Old  string `json:"old_text"`
		New  string `json:"new_text"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, err
	}
	p, o, e := t.Guard.Resolve(a.Path)
	action := Action{Risk: RiskWrite, Summary: "edit " + p, Outside: o, Paths: []string{p}}
	if e == nil && a.Old != "" {
		if data, readErr := os.ReadFile(p); readErr == nil && strings.Count(string(data), a.Old) == 1 {
			updated := strings.Replace(string(data), a.Old, a.New, 1)
			action.Preview = diffmodel.Unified(displayName(t.Guard.Workspace, p), string(data), updated)
		}
	}
	return action, e
}
func (t EditFileTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
		Old  string `json:"old_text"`
		New  string `json:"new_text"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if a.Old == "" {
		return "", errors.New("old_text must not be empty")
	}
	target, _, err := t.Guard.MutationTarget(a.Path)
	if err != nil {
		return "", err
	}
	defer target.Close()
	p := target.Path()
	data, err := target.ReadFile()
	if err != nil {
		return "", err
	}
	count := strings.Count(string(data), a.Old)
	if count != 1 {
		return "", fmt.Errorf("old_text must match exactly once (found %d)", count)
	}
	updated := strings.Replace(string(data), a.Old, a.New, 1)
	info, err := target.Stat()
	if err != nil {
		return "", err
	}
	if err = target.Replace([]byte(updated), info.Mode().Perm()); err != nil {
		return "", err
	}
	if t.Tracker != nil {
		before := string(data)
		t.Tracker.RecordWithMode(p, "edit", &before, &updated, info.Mode().Perm(), info.Mode().Perm())
	}
	return fmt.Sprintf("edited %s", p), nil
}
