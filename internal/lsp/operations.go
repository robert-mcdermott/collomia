package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
)

// Location is one position a server points at, in workspace terms.
type Location struct {
	Path string // workspace-relative when inside the workspace
	Line int    // 1-based
	// Character is a 1-based column measured in runes, not the UTF-16 code
	// units the protocol uses. Callers display it; nothing round-trips it back
	// to the server.
	Character int
}

// TextEdit is one replacement in protocol coordinates: zero-based lines and
// UTF-16 character offsets. Apply converts them.
type TextEdit struct {
	StartLine, StartChar int
	EndLine, EndChar     int
	NewText              string
}

// Open sends didOpen for one file so the server has content to answer
// questions about. Servers are free to read the file themselves, but a server
// that has not indexed the workspace yet will answer with nothing unless the
// document is open.
func (c *Client) Open(rel, content, languageID string) error {
	return c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
		"uri": c.uriFor(rel), "languageId": languageID, "version": 1, "text": content,
	}})
}

// Definition resolves the symbol at a position to where it is defined.
func (c *Client) Definition(ctx context.Context, rel string, line, character int) ([]Location, error) {
	raw, err := c.call(ctx, "textDocument/definition", c.positionParams(rel, line, character, nil))
	if err != nil {
		return nil, err
	}
	return c.decodeLocations(raw), nil
}

// References lists the places a symbol is used.
func (c *Client) References(ctx context.Context, rel string, line, character int, includeDeclaration bool) ([]Location, error) {
	extra := map[string]any{"context": map[string]any{"includeDeclaration": includeDeclaration}}
	raw, err := c.call(ctx, "textDocument/references", c.positionParams(rel, line, character, extra))
	if err != nil {
		return nil, err
	}
	return c.decodeLocations(raw), nil
}

// Formatting asks the server to format a whole document and returns the edits
// it proposes. Nothing is written here; the caller decides.
func (c *Client) Formatting(ctx context.Context, rel string, tabSize int, insertSpaces bool) ([]TextEdit, error) {
	params := map[string]any{
		"textDocument": map[string]any{"uri": c.uriFor(rel)},
		"options": map[string]any{
			"tabSize": tabSize, "insertSpaces": insertSpaces,
			"trimTrailingWhitespace": true, "insertFinalNewline": true,
		},
	}
	raw, err := c.call(ctx, "textDocument/formatting", params)
	if err != nil {
		return nil, err
	}
	var edits []struct {
		Range struct {
			Start protocolPosition `json:"start"`
			End   protocolPosition `json:"end"`
		} `json:"range"`
		NewText string `json:"newText"`
	}
	if json.Unmarshal(raw, &edits) != nil {
		return nil, nil
	}
	out := make([]TextEdit, 0, len(edits))
	for _, e := range edits {
		out = append(out, TextEdit{
			StartLine: e.Range.Start.Line, StartChar: e.Range.Start.Character,
			EndLine: e.Range.End.Line, EndChar: e.Range.End.Character, NewText: e.NewText,
		})
	}
	return out, nil
}

type protocolPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

func (c *Client) positionParams(rel string, line, character int, extra map[string]any) map[string]any {
	params := map[string]any{
		"textDocument": map[string]any{"uri": c.uriFor(rel)},
		"position":     map[string]any{"line": line, "character": character},
	}
	for key, value := range extra {
		params[key] = value
	}
	return params
}

func (c *Client) uriFor(rel string) string {
	return pathToURI(filepath.Join(c.root, filepath.FromSlash(rel)))
}

// decodeLocations accepts every shape the protocol permits for a definition or
// reference result: a single Location, an array of Location, an array of
// LocationLink, or null. A server that answers in a shape we cannot read is
// reported as "no results" rather than as an error, because the alternative —
// failing a lookup that simply found nothing — is indistinguishable to a
// caller and more alarming.
func (c *Client) decodeLocations(raw json.RawMessage) []Location {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	type wire struct {
		URI   string `json:"uri"`
		Range *struct {
			Start protocolPosition `json:"start"`
		} `json:"range"`
		TargetURI            string `json:"targetUri"`
		TargetSelectionRange *struct {
			Start protocolPosition `json:"start"`
		} `json:"targetSelectionRange"`
		TargetRange *struct {
			Start protocolPosition `json:"start"`
		} `json:"targetRange"`
	}
	var many []wire
	if err := json.Unmarshal(raw, &many); err != nil {
		var single wire
		if json.Unmarshal(raw, &single) != nil {
			return nil
		}
		many = []wire{single}
	}
	out := make([]Location, 0, len(many))
	for _, item := range many {
		uri, start := item.URI, item.Range
		if uri == "" {
			uri = item.TargetURI
			if start = item.TargetSelectionRange; start == nil {
				start = item.TargetRange
			}
		}
		if uri == "" || start == nil {
			continue
		}
		out = append(out, Location{Path: c.relPath(uriToPath(uri)), Line: start.Start.Line + 1, Character: start.Start.Character + 1})
	}
	return out
}

// Apply returns content with edits applied. Edits address the original
// document, so they are applied from the end backwards; overlapping edits are
// rejected rather than silently producing a mangled file.
func Apply(content string, edits []TextEdit) (string, error) {
	if len(edits) == 0 {
		return content, nil
	}
	lines := splitLines(content)
	type resolved struct {
		start, end int
		text       string
	}
	offsets := lineOffsets(lines)
	list := make([]resolved, 0, len(edits))
	for _, edit := range edits {
		start, err := absoluteOffset(lines, offsets, edit.StartLine, edit.StartChar)
		if err != nil {
			return "", err
		}
		end, err := absoluteOffset(lines, offsets, edit.EndLine, edit.EndChar)
		if err != nil {
			return "", err
		}
		if end < start {
			return "", fmt.Errorf("language server returned an inverted edit range")
		}
		list = append(list, resolved{start: start, end: end, text: edit.NewText})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].start > list[j].start })
	for i, edit := range list {
		if i > 0 && edit.end > list[i-1].start {
			return "", fmt.Errorf("language server returned overlapping edits")
		}
	}
	out := content
	for _, edit := range list {
		out = out[:edit.start] + edit.text + out[edit.end:]
	}
	return out, nil
}

// splitLines keeps the document's own line boundaries. The protocol counts
// lines, so a file with CRLF endings must not have them normalized away before
// offsets are computed.
func splitLines(content string) []string {
	if content == "" {
		return []string{""}
	}
	// SplitAfter already yields a trailing empty element for a document that
	// ends in a newline, which is the line servers address when appending.
	return strings.SplitAfter(content, "\n")
}

func lineOffsets(lines []string) []int {
	offsets := make([]int, len(lines))
	position := 0
	for i, line := range lines {
		offsets[i] = position
		position += len(line)
	}
	return offsets
}

// absoluteOffset converts a protocol position to a byte offset. A position one
// line past the end is legal in the protocol (servers use it to append), so it
// clamps to the document end rather than failing.
func absoluteOffset(lines []string, offsets []int, line, character int) (int, error) {
	if line < 0 || character < 0 {
		return 0, fmt.Errorf("language server returned a negative position")
	}
	if line >= len(lines) {
		last := len(lines) - 1
		return offsets[last] + len(lines[last]), nil
	}
	text := strings.TrimRight(lines[line], "\r\n")
	return offsets[line] + byteOffsetForUTF16(text, character), nil
}

// byteOffsetForUTF16 converts a UTF-16 code-unit offset — what the protocol
// counts — to a byte offset in the line. Anything past the end clamps to the
// end of the line's text.
func byteOffsetForUTF16(line string, offset int) int {
	if offset <= 0 {
		return 0
	}
	units := 0
	for index, r := range line {
		if units >= offset {
			return index
		}
		units += utf16.RuneLen(r)
		if units < 0 { // invalid rune: count it as one unit
			units++
		}
	}
	return len(line)
}

// UTF16Column converts a byte index within a line to the UTF-16 code-unit
// offset the protocol expects.
func UTF16Column(line string, byteIndex int) int {
	if byteIndex <= 0 {
		return 0
	}
	if byteIndex > len(line) {
		byteIndex = len(line)
	}
	return len(utf16.Encode([]rune(line[:byteIndex])))
}
