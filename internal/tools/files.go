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
	return Action{Risk: RiskRead, Summary: "read " + p, Outside: o}, e
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
	return provider.ToolDefinition{Name: "list_files", Description: "List a directory tree without invoking a shell. Hidden files are included; .git and Collomia session data are skipped.", InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string"},"max_depth":{"type":"integer","minimum":1,"maximum":8}},"additionalProperties":false}`)}
}
func (t ListFilesTool) Assess(raw json.RawMessage) (Action, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, err
	}
	p, o, e := t.Guard.Resolve(a.Path)
	return Action{Risk: RiskRead, Summary: "list " + p, Outside: o}, e
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
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "sessions" && filepath.Base(filepath.Dir(p)) == ".collomia") {
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
	return provider.ToolDefinition{Name: "search_files", Description: "Search text files using a Go regular expression. Returns matching file paths, line numbers, and text without invoking a platform-specific grep command.", InputSchema: schema(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"},"file_glob":{"type":"string","description":"Optional filepath glob such as *.go"},"max_results":{"type":"integer","minimum":1,"maximum":1000}},"required":["pattern"],"additionalProperties":false}`)}
}
func (t SearchFilesTool) Assess(raw json.RawMessage) (Action, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, err
	}
	p, o, e := t.Guard.Resolve(a.Path)
	return Action{Risk: RiskRead, Summary: "search " + p, Outside: o}, e
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
			if d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "vendor" {
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

type WriteFileTool struct{ Guard *PathGuard }

func (t WriteFileTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "write_file", Description: "Create or replace a text file. Parent directories are created. Prefer edit_file for focused changes to an existing file.", InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)}
}
func (t WriteFileTool) Assess(raw json.RawMessage) (Action, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, err
	}
	p, o, e := t.Guard.Resolve(a.Path)
	return Action{Risk: RiskWrite, Summary: "write " + p, Outside: o}, e
}
func (t WriteFileTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var a struct{ Path, Content string }
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	p, _, err := t.Guard.Resolve(a.Path)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	mode := os.FileMode(0o644)
	if info, e := os.Stat(p); e == nil {
		mode = info.Mode().Perm()
	}
	if err = os.WriteFile(p, []byte(a.Content), mode); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), p), nil
}

type EditFileTool struct{ Guard *PathGuard }

func (t EditFileTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{Name: "edit_file", Description: "Replace one exact, unique text fragment in a file. The operation fails if old_text is missing or appears more than once, preventing ambiguous edits.", InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"}},"required":["path","old_text","new_text"],"additionalProperties":false}`)}
}
func (t EditFileTool) Assess(raw json.RawMessage) (Action, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return Action{}, err
	}
	p, o, e := t.Guard.Resolve(a.Path)
	return Action{Risk: RiskWrite, Summary: "edit " + p, Outside: o}, e
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
	p, _, err := t.Guard.Resolve(a.Path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	count := strings.Count(string(data), a.Old)
	if count != 1 {
		return "", fmt.Errorf("old_text must match exactly once (found %d)", count)
	}
	updated := strings.Replace(string(data), a.Old, a.New, 1)
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(p, []byte(updated), info.Mode().Perm()); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", p), nil
}
