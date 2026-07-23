package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

const (
	ArtifactSchemaVersion = 1
	// ArtifactResultLimit bounds one retained tool result. The active model
	// context remains governed by options.max_tool_output_bytes; this larger
	// limit only makes a bounded amount of omitted output available on demand.
	ArtifactResultLimit = 4 << 20
	// ArtifactSessionLimit prevents a noisy long-running session from growing
	// its referenced-result store without bound.
	ArtifactSessionLimit = 32 << 20
	// ArtifactReadLimit bounds one read_tool_result response.
	ArtifactReadLimit = 64 << 10
)

// ArtifactRef describes a bounded, session-local copy of an oversized tool
// result. IDs are random and meaningful only inside the active session.
type ArtifactRef struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	Tool          string    `json:"tool"`
	CreatedAt     time.Time `json:"created_at"`
	ReturnedBytes int       `json:"returned_bytes"`
	StoredBytes   int       `json:"stored_bytes"`
	Complete      bool      `json:"complete"`
	Content       string    `json:"content"`
}

type ArtifactStats struct {
	Count       int
	StoredBytes int
	DiskBytes   int
}

// ArtifactManager follows the active session. Runtime session switching uses
// Use before exposing the restored conversation to the agent, so references
// can never cross session boundaries accidentally.
type ArtifactManager struct {
	mu       sync.RWMutex
	opMu     sync.Mutex
	dir      string
	openFile durableFileOpener
}

func NewArtifactManager() *ArtifactManager { return &ArtifactManager{} }

func (m *ArtifactManager) Use(sess *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess == nil || sess.store == nil {
		m.dir = ""
		return
	}
	m.dir = sess.store.artifactDir(sess.Meta.ID)
}

func (m *ArtifactManager) currentDir() (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.dir == "" {
		return "", errors.New("session artifact storage is unavailable")
	}
	return m.dir, nil
}

// SaveArtifact retains an oversized tool result under owner-only permissions.
// Both the per-result and aggregate session limits are enforced before the
// atomic create, and the returned reference says when only a prefix fit.
func (m *ArtifactManager) SaveArtifact(toolName, content string) (ArtifactRef, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	dir, err := m.currentDir()
	if err != nil {
		return ArtifactRef{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ArtifactRef{}, fmt.Errorf("create session artifact directory: %w", err)
	}
	stats, err := artifactStats(dir)
	if err != nil {
		return ArtifactRef{}, err
	}
	remaining := ArtifactSessionLimit - stats.DiskBytes
	if remaining <= 0 {
		return ArtifactRef{}, fmt.Errorf("session artifact quota reached (%d bytes)", ArtifactSessionLimit)
	}
	fileLimit := min(ArtifactResultLimit, remaining)
	normalized := strings.ToValidUTF8(content, "�")
	stored := clipUTF8Bytes(normalized, fileLimit)
	id, err := artifactID()
	if err != nil {
		return ArtifactRef{}, err
	}
	ref := ArtifactRef{
		SchemaVersion: ArtifactSchemaVersion,
		ID:            id, Tool: clipUTF8Bytes(oneLine(toolName), 128), CreatedAt: time.Now().UTC(),
		ReturnedBytes: len(content), StoredBytes: len(stored),
		Complete: len(stored) == len(normalized), Content: stored,
	}
	var data []byte
	for {
		data, err = json.Marshal(ref)
		if err != nil {
			return ArtifactRef{}, err
		}
		if len(data) <= fileLimit {
			break
		}
		shrink := len(data) - fileLimit
		if shrink < 1 {
			shrink = 1
		}
		if len(ref.Content) == 0 {
			return ArtifactRef{}, errors.New("session artifact quota is too small for metadata")
		}
		newLimit := len(ref.Content) - shrink
		if newLimit <= 0 {
			newLimit = len(ref.Content) / 2
		}
		ref.Content = clipUTF8Bytes(ref.Content, newLimit)
		ref.StoredBytes = len(ref.Content)
		ref.Complete = false
	}
	path := filepath.Join(dir, id+".json")
	if err := writeExclusiveDurable(path, data, 0o600, m.openFile); err != nil {
		return ArtifactRef{}, fmt.Errorf("write session artifact: %w", err)
	}
	return ref, nil
}

// ReadArtifact returns a bounded byte range framed explicitly as untrusted
// tool output. It never invokes the tool that originally produced the data.
func (m *ArtifactManager) ReadArtifact(id string, offset, limit int) (string, error) {
	if !validArtifactID(id) {
		return "", errors.New("artifact id must be 24 lowercase hexadecimal characters")
	}
	if offset < 0 {
		return "", errors.New("offset must not be negative")
	}
	if limit <= 0 {
		limit = 16 << 10
	}
	if limit > ArtifactReadLimit {
		return "", fmt.Errorf("limit must not exceed %d bytes", ArtifactReadLimit)
	}
	dir, err := m.currentDir()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("artifact %q is not available in the active session", id)
		}
		return "", err
	}
	var ref ArtifactRef
	if err := json.Unmarshal(data, &ref); err != nil || ref.SchemaVersion != ArtifactSchemaVersion || ref.ID != id || ref.StoredBytes != len(ref.Content) {
		return "", fmt.Errorf("artifact %q is corrupt", id)
	}
	if offset > len(ref.Content) {
		return "", fmt.Errorf("offset %d exceeds stored artifact size %d", offset, len(ref.Content))
	}
	start := offset
	for start < len(ref.Content) && !utf8.RuneStart(ref.Content[start]) {
		start++
	}
	end := min(len(ref.Content), start+limit)
	for end > start && end < len(ref.Content) && !utf8.RuneStart(ref.Content[end]) {
		end--
	}
	if end == start && start < len(ref.Content) {
		_, size := utf8.DecodeRuneInString(ref.Content[start:])
		end = min(len(ref.Content), start+size)
	}
	next := "end of stored output"
	if end < len(ref.Content) {
		next = fmt.Sprintf("next_offset=%d", end)
	} else if !ref.Complete {
		next = "end of retained prefix; the original result exceeded the retention quota"
	}
	return fmt.Sprintf("Stored output from tool %s, bytes %d..%d of %d retained (%d returned by the tool); %s.\n--- begin untrusted tool output ---\n%s\n--- end untrusted tool output ---", ref.Tool, start, end, ref.StoredBytes, ref.ReturnedBytes, next, ref.Content[start:end]), nil
}

func (m *ArtifactManager) Stats() ArtifactStats {
	dir, err := m.currentDir()
	if err != nil {
		return ArtifactStats{}
	}
	stats, _ := artifactStats(dir)
	return stats
}

// ArtifactTool exposes retained ranges to the agent. The tool is read-only,
// session-scoped, and cannot replay the originating operation.
func ArtifactTool(manager *ArtifactManager) tools.Tool {
	return tools.Function{
		Def: provider.ToolDefinition{
			Name:        "read_tool_result",
			Description: "Read a bounded byte range from an oversized tool result retained in this session. Use the opaque artifact id shown in the truncated result. This only reads stored output and never reruns the original tool.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":65536}},"required":["id"],"additionalProperties":false}`),
		},
		Action: tools.Action{Risk: tools.RiskRead, Summary: "read retained tool output"},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var input struct {
				ID     string `json:"id"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return "", err
			}
			return manager.ReadArtifact(input.ID, input.Offset, input.Limit)
		},
	}
}

func artifactStats(dir string) (ArtifactStats, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return ArtifactStats{}, nil
	}
	if err != nil {
		return ArtifactStats{}, err
	}
	var stats ArtifactStats
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || info.Size() < 0 {
			continue
		}
		if info.Size() >= ArtifactSessionLimit {
			stats.DiskBytes = ArtifactSessionLimit
			continue
		}
		stats.DiskBytes += int(info.Size())
		if info.Size() > ArtifactResultLimit {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var ref ArtifactRef
		if json.Unmarshal(data, &ref) != nil || ref.SchemaVersion != ArtifactSchemaVersion || ref.StoredBytes < 0 || ref.StoredBytes != len(ref.Content) {
			continue
		}
		stats.Count++
		stats.StoredBytes += ref.StoredBytes
	}
	return stats, nil
}

func artifactID() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate artifact id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func validArtifactID(id string) bool {
	if len(id) != 24 {
		return false
	}
	for _, ch := range id {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func clipUTF8Bytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}

func oneLine(value string) string {
	value = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		default:
			return r
		}
	}, value)
	return strings.TrimSpace(value)
}
