package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	appconfig "github.com/robert-mcdermott/collomia/internal/config"
)

type editorFinishedMsg struct {
	path string
	err  error
}

func (m Model) openExternalEditor() (tea.Model, tea.Cmd) {
	state := m.diffView
	if state == nil || len(state.files) == 0 {
		return m, nil
	}
	file := state.files[state.file]
	line := 1
	if len(state.hunkLines) > 0 && state.hunk < len(state.hunkLines) {
		line = max(1, state.hunkLines[state.hunk])
	}
	cmd, err := externalEditorCommand(m.runtime.Config.Options.Editor, m.runtime.Workspace, file.Path, line, 1)
	if err != nil {
		state.notice = err.Error()
		return m, nil
	}
	state.notice = "opening " + file.Name + " in external editor"
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editorFinishedMsg{path: file.Path, err: err}
	})
}

func (m *Model) finishExternalEditor(msg editorFinishedMsg) {
	state := m.diffView
	if state == nil {
		return
	}
	currentPath := msg.path
	files := m.runtime.Changes.FileDiffs(m.runtime.Workspace)
	if len(files) == 0 {
		m.diffView = nil
		m.input.Focus()
		m.addSystem("External editor left no session changes to review.")
		m.refresh()
		return
	}
	state.files = files
	state.file = 0
	for i := range files {
		if files[i].Path == currentPath {
			state.file = i
			break
		}
	}
	state.stats = make([]diffStats, len(files))
	for i, file := range files {
		state.stats[i].added, state.stats[i].deleted = diffCounts(file)
	}
	state.hunk = 0
	state.viewport.GotoTop()
	if msg.err != nil {
		state.notice = "editor failed: " + compactEditorError(msg.err.Error())
	} else {
		state.notice = "returned from external editor · diff refreshed"
	}
	m.rebuildDiffView()
}

func externalEditorCommand(cfg appconfig.EditorOptions, workspace, path string, line, column int) (*exec.Cmd, error) {
	contained, err := containedWorkspacePath(workspace, path)
	if err != nil {
		return nil, fmt.Errorf("cannot open editor: %w", err)
	}
	command := strings.TrimSpace(cfg.Command)
	args := append([]string(nil), cfg.Args...)
	if command == "" {
		value := strings.TrimSpace(os.Getenv("VISUAL"))
		if value == "" {
			value = strings.TrimSpace(os.Getenv("EDITOR"))
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return nil, errors.New("configure options.editor or set VISUAL/EDITOR to use e")
		}
		command, args = fields[0], fields[1:]
	}
	if _, err := exec.LookPath(command); err != nil {
		return nil, fmt.Errorf("editor %q is not installed or not in PATH", command)
	}
	replacements := map[string]string{
		"{file}":   contained,
		"{line}":   strconv.Itoa(max(1, line)),
		"{column}": strconv.Itoa(max(1, column)),
	}
	hasFile := false
	for i, arg := range args {
		for placeholder, value := range replacements {
			if strings.Contains(arg, placeholder) {
				arg = strings.ReplaceAll(arg, placeholder, value)
				if placeholder == "{file}" {
					hasFile = true
				}
			}
		}
		args[i] = arg
	}
	if !hasFile {
		args = append(args, contained)
	}
	cmd := exec.Command(command, args...)
	cmd.Dir = workspace
	return cmd, nil
}

func containedWorkspacePath(workspace, path string) (string, error) {
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root, err = resolvePathSymlinks(root)
	if err != nil {
		return "", err
	}
	target, err = resolvePathSymlinks(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errors.New("diff file is outside the workspace")
	}
	return target, nil
}

// resolvePathSymlinks resolves the longest existing prefix, then rejoins any
// missing suffix. This keeps deleted/new diff targets inside the workspace
// even when one of their parent directories is a symlink.
func resolvePathSymlinks(path string) (string, error) {
	path = filepath.Clean(path)
	current := path
	var suffix []string
	for {
		if evaluated, err := filepath.EvalSymlinks(current); err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				evaluated = filepath.Join(evaluated, suffix[i])
			}
			return filepath.Clean(evaluated), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("cannot resolve path %q", path)
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func compactEditorError(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 160 {
		return value[:160] + "…"
	}
	return value
}
