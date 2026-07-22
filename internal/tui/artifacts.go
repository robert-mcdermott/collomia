package tui

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/session"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// maxPromptFileBytes keeps an accidental drag of a generated artifact from
// flooding the composer and model context. Larger source files remain usable
// through an @ mention, which lets the agent inspect them with bounded tools.
const maxPromptFileBytes = 256 << 10

// quoteComposerPath keeps a selected workspace path readable as one path when
// it contains whitespace or quote characters. The model sees ordinary quoted
// text; no shell evaluates this value.
func quoteComposerPath(path string) string {
	if !strings.ContainsAny(path, " \t\r\n\"'") && strings.IndexFunc(path, unicode.IsControl) < 0 {
		return path
	}
	return fmt.Sprintf("%q", path)
}

// parseTerminalPath accepts the common forms produced by typing or dragging a
// path into a terminal: plain, single/double quoted, and backslash-escaped
// whitespace. Backslashes that are ordinary Windows separators are retained.
func parseTerminalPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("path is empty")
	}
	var out strings.Builder
	quote := byte(0)
	closed := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if quote != 0 {
			if c == quote {
				quote = 0
				closed = true
				continue
			}
			if quote == '"' && c == '\\' && i+1 < len(raw) && (raw[i+1] == '"' || raw[i+1] == '\\' || raw[i+1] == ' ') {
				i++
				out.WriteByte(raw[i])
				continue
			}
			out.WriteByte(c)
			continue
		}
		if closed {
			if strings.TrimSpace(raw[i:]) != "" {
				return "", fmt.Errorf("expected one path; quote a path containing spaces")
			}
			break
		}
		switch c {
		case '\'', '"':
			if out.Len() != 0 {
				return "", fmt.Errorf("quote the complete path, not only part of it")
			}
			quote = c
		case '\\':
			if i+1 < len(raw) && (raw[i+1] == ' ' || raw[i+1] == '\t' || raw[i+1] == '\'' || raw[i+1] == '"') {
				i++
				out.WriteByte(raw[i])
			} else {
				out.WriteByte(c)
			}
		case ' ', '\t', '\r', '\n':
			return "", fmt.Errorf("expected one path; quote it or escape spaces with backslashes")
		default:
			out.WriteByte(c)
		}
	}
	if quote != 0 {
		return "", fmt.Errorf("unterminated quoted path")
	}
	path := out.String()
	if strings.HasPrefix(path, "file://") {
		u, err := url.Parse(path)
		if err != nil || u.Path == "" || (u.Host != "" && u.Host != "localhost") {
			return "", fmt.Errorf("invalid local file URL %q", path)
		}
		path, err = url.PathUnescape(u.Path)
		if err != nil {
			return "", fmt.Errorf("decode file URL: %w", err)
		}
		if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
			path = path[1:]
		}
		path = filepath.FromSlash(path)
	}
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	return path, nil
}

func promptPathArgument(line string) (string, error) {
	space := strings.IndexAny(line, " \t")
	if space < 0 {
		return "", nil
	}
	raw := strings.TrimSpace(line[space:])
	if raw == "" {
		return "", nil
	}
	return parseTerminalPath(raw)
}

func (m *Model) loadPromptFile(requested string) error {
	guard, err := tools.NewPathGuard(m.runtime.Workspace, false)
	if err != nil {
		return err
	}
	resolved, outside, err := guard.Resolve(requested)
	if err != nil {
		if outside {
			return fmt.Errorf("prompt file %q is outside the active workspace; copy it into the workspace before loading it", requested)
		}
		return err
	}
	f, err := os.Open(resolved)
	if err != nil {
		return fmt.Errorf("open prompt file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("inspect prompt file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("prompt input must be a regular text file")
	}
	if info.Size() > maxPromptFileBytes {
		return fmt.Errorf("prompt file is %d bytes; the TUI limit is %d bytes (mention it with @ so the agent can read it in bounded chunks)", info.Size(), maxPromptFileBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPromptFileBytes+1))
	if err != nil {
		return fmt.Errorf("read prompt file: %w", err)
	}
	if len(data) > maxPromptFileBytes {
		return fmt.Errorf("prompt file exceeds the %d-byte TUI limit", maxPromptFileBytes)
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return fmt.Errorf("prompt input must be UTF-8 text; image and binary attachments are not supported by the current provider adapters")
	}
	for _, r := range string(data) {
		if (r < 0x20 && r != '\n' && r != '\r' && r != '\t') || r == 0x7f {
			return fmt.Errorf("prompt input contains terminal control characters and cannot be shown safely in the composer")
		}
	}
	display := resolved
	if rel, relErr := filepath.Rel(guard.Workspace, resolved); relErr == nil {
		display = filepath.ToSlash(rel)
	}
	m.setComposerValue("[Prompt loaded from " + quoteComposerPath(display) + "]\n\n" + string(data))
	m.input.Focus()
	m.addSystem(fmt.Sprintf("Loaded %s into the composer (%d bytes). Review or edit it, then press enter.", quoteComposerPath(display), len(data)))
	return nil
}

func (m *Model) openPromptFilePicker() {
	items := m.workspaceFiles()
	if len(items) == 0 {
		m.addPanel("Prompt from file", "No workspace files are available. Add a UTF-8 text file or run /prompt with a workspace path.")
		return
	}
	m.picker = newPicker("Load prompt from file", items, func(m *Model, item pickerItem) tea.Cmd {
		if err := m.loadPromptFile(item.id); err != nil {
			m.addError(err)
		}
		return nil
	})
	m.layout()
	m.refresh()
}

func (m *Model) addImageAttachment(requested string) error {
	if m.runtime.Session == nil || m.runtime.Attachments == nil {
		return fmt.Errorf("image attachments require a durable session")
	}
	if m.runtime.Agent.Capabilities().Images == provider.CapabilityUnsupported {
		providerName, model := m.runtime.Agent.Selection()
		return fmt.Errorf("%s/%s does not support image input; switch to an image-capable model first", providerName, model)
	}
	if len(m.pendingAttachments) >= session.AttachmentTurnLimit {
		return fmt.Errorf("a prompt may contain at most %d image attachments", session.AttachmentTurnLimit)
	}
	guard, err := tools.NewPathGuard(m.runtime.Workspace, false)
	if err != nil {
		return err
	}
	resolved, outside, err := guard.Resolve(requested)
	if err != nil {
		if outside {
			return fmt.Errorf("attachment %q is outside the active workspace; copy it into the workspace before attaching it", requested)
		}
		return err
	}
	for _, attachment := range m.pendingAttachments {
		if attachment.path == resolved {
			return fmt.Errorf("%s is already attached", quoteComposerPath(requested))
		}
	}
	part, err := session.InspectImage(resolved)
	if err != nil {
		return err
	}
	if rel, relErr := filepath.Rel(guard.Workspace, resolved); relErr == nil {
		part.Name = safeAttachmentDisplayName(filepath.ToSlash(rel))
	}
	m.pendingAttachments = append(m.pendingAttachments, pendingAttachment{path: resolved, part: part})
	m.addSystem(fmt.Sprintf("Attached image %s (%s, %s) to the pending prompt. It is copied into session storage only when you send the prompt.", quoteComposerPath(part.Name), part.MediaType, formatByteCount(part.Size)))
	return nil
}

func (m *Model) readPendingAttachments() ([]provider.ContentPart, error) {
	if len(m.pendingAttachments) == 0 {
		return nil, nil
	}
	parts := make([]provider.ContentPart, 0, len(m.pendingAttachments))
	for _, pending := range m.pendingAttachments {
		part, err := session.ReadWorkspaceImage(m.runtime.Workspace, pending.path)
		if err != nil {
			return nil, fmt.Errorf("read attachment %q: %w", pending.part.Name, err)
		}
		part.Name = pending.part.Name
		parts = append(parts, part)
	}
	return parts, nil
}

func (m *Model) showPendingAttachments() {
	if len(m.pendingAttachments) == 0 {
		m.addPanel("Pending attachments", "No images are attached to the pending prompt. Use /attach [workspace-image].")
		return
	}
	lines := make([]string, 0, len(m.pendingAttachments))
	for i, attachment := range m.pendingAttachments {
		lines = append(lines, fmt.Sprintf("%d. %s — %s, %s", i+1, attachment.part.Name, attachment.part.MediaType, formatByteCount(attachment.part.Size)))
	}
	m.addPanel("Pending attachments", strings.Join(lines, "\n")+"\n\n/detach <number> removes one; /detach all removes every pending image.")
}

func (m *Model) detachImage(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: /detach <number|all>")
	}
	if strings.EqualFold(args[0], "all") {
		count := len(m.pendingAttachments)
		m.pendingAttachments = nil
		m.addSystem(fmt.Sprintf("Removed %d pending image attachment(s).", count))
		return nil
	}
	index, err := strconv.Atoi(args[0])
	if err != nil || index < 1 || index > len(m.pendingAttachments) {
		return fmt.Errorf("attachment number must be between 1 and %d", len(m.pendingAttachments))
	}
	removed := m.pendingAttachments[index-1]
	m.pendingAttachments = append(m.pendingAttachments[:index-1], m.pendingAttachments[index:]...)
	m.addSystem("Detached " + quoteComposerPath(removed.part.Name) + " from the pending prompt.")
	return nil
}

func (m *Model) openImagePicker() {
	var items []pickerItem
	for _, item := range m.workspaceFiles() {
		ext := strings.ToLower(filepath.Ext(item.id))
		if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp" {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		m.addPanel("Attach image", "No PNG, JPEG, GIF, or WebP files are available in the workspace.")
		return
	}
	m.picker = newPicker("Attach image", items, func(m *Model, item pickerItem) tea.Cmd {
		if err := m.addImageAttachment(item.id); err != nil {
			m.addError(err)
		}
		return nil
	})
	m.layout()
	m.refresh()
}

func displayMessageWithAttachments(content string, parts []provider.ContentPart) string {
	var names []string
	for _, part := range parts {
		if part.Type == provider.ContentImage {
			names = append(names, fmt.Sprintf("%s (%s, %s)", part.Name, part.MediaType, formatByteCount(part.Size)))
		}
	}
	if len(names) == 0 {
		return content
	}
	return content + "\n\n[Attached images: " + strings.Join(names, "; ") + "]"
}

func safeAttachmentDisplayName(name string) string {
	name = strings.ToValidUTF8(name, "�")
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f {
			return -1
		}
		return r
	}, name)
	runes := []rune(name)
	if len(runes) > 256 {
		name = string(runes[:256]) + "…"
	}
	if name == "" {
		return "image"
	}
	return name
}
