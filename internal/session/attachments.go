package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/safefile"
)

const (
	// AttachmentFileLimit bounds one image sent to a model. It is deliberately
	// smaller than typical provider limits so the same behavior is predictable
	// across providers and terminal sessions.
	AttachmentFileLimit = 5 << 20
	// AttachmentSessionLimit bounds all user and MCP images retained by one
	// durable session.
	AttachmentSessionLimit = 24 << 20
	// AttachmentTurnLimit prevents one prompt or tool batch from overwhelming
	// a provider even when every individual image is small.
	AttachmentTurnLimit = 4
)

// AttachmentManager follows the active durable session. Files contain only
// raw image bytes; names, MIME types, sizes, and hashes live in the message
// record that references them.
type AttachmentManager struct {
	mu       sync.RWMutex
	opMu     sync.Mutex
	dir      string
	openFile durableFileOpener
}

func NewAttachmentManager() *AttachmentManager { return &AttachmentManager{} }

func (m *AttachmentManager) Use(sess *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess == nil || sess.store == nil {
		m.dir = ""
		return
	}
	m.dir = sess.store.attachmentDir(sess.Meta.ID)
}

func (m *AttachmentManager) currentDir() (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.dir == "" {
		return "", errors.New("session attachment storage is unavailable")
	}
	return m.dir, nil
}

func (m *AttachmentManager) Available() bool {
	_, err := m.currentDir()
	return err == nil
}

// InspectImage validates a regular image file without retaining it. The
// caller is responsible for applying its workspace/path policy first.
func InspectImage(path string) (provider.ContentPart, error) {
	f, err := os.Open(path)
	if err != nil {
		return provider.ContentPart{}, fmt.Errorf("open image: %w", err)
	}
	defer f.Close()
	return inspectImageFile(f, filepath.Base(path), false)
}

// ReadWorkspaceImage opens and reads a workspace image through an anchored
// directory handle. The final read therefore cannot escape the workspace if a
// path component or symbolic link changes after the path was selected.
func ReadWorkspaceImage(workspace, path string) (provider.ContentPart, error) {
	target, err := safefile.Open(workspace, path)
	if err != nil {
		return provider.ContentPart{}, fmt.Errorf("anchor workspace image: %w", err)
	}
	defer target.Close()
	f, err := target.OpenFile()
	if err != nil {
		return provider.ContentPart{}, fmt.Errorf("open workspace image: %w", err)
	}
	defer f.Close()
	return inspectImageFile(f, filepath.Base(path), true)
}

func inspectImageFile(f *os.File, name string, includeData bool) (provider.ContentPart, error) {
	info, err := f.Stat()
	if err != nil {
		return provider.ContentPart{}, fmt.Errorf("inspect image: %w", err)
	}
	if !info.Mode().IsRegular() {
		return provider.ContentPart{}, errors.New("attachment must be a regular image file")
	}
	if info.Size() <= 0 {
		return provider.ContentPart{}, errors.New("attachment is empty")
	}
	if info.Size() > AttachmentFileLimit {
		return provider.ContentPart{}, fmt.Errorf("attachment is %d bytes; the per-image limit is %d bytes", info.Size(), AttachmentFileLimit)
	}
	limit := int64(512)
	if includeData {
		limit = AttachmentFileLimit + 1
	}
	data, readErr := io.ReadAll(io.LimitReader(f, limit))
	if readErr != nil {
		return provider.ContentPart{}, fmt.Errorf("read image: %w", readErr)
	}
	if includeData && len(data) != int(info.Size()) {
		return provider.ContentPart{}, errors.New("attachment changed while it was being read; inspect it and try again")
	}
	mediaType, err := supportedImageType(data)
	if err != nil {
		return provider.ContentPart{}, err
	}
	part := provider.ContentPart{Type: provider.ContentImage, Name: safeAttachmentName(name), MediaType: mediaType, Size: int(info.Size())}
	if includeData {
		part.Data = data
	}
	return part, nil
}

// SaveFile validates and copies a user-selected image into the active session.
func (m *AttachmentManager) SaveFile(path string) (provider.ContentPart, error) {
	f, err := os.Open(path)
	if err != nil {
		return provider.ContentPart{}, fmt.Errorf("open image: %w", err)
	}
	defer f.Close()
	part, err := inspectImageFile(f, filepath.Base(path), true)
	if err != nil {
		return provider.ContentPart{}, err
	}
	return m.SaveBytes(part.Name, part.MediaType, part.Data)
}

// SaveBytes retains a bounded image returned by an external tool. The detected
// media type is authoritative; mismatched advertised types are rejected.
func (m *AttachmentManager) SaveBytes(name, advertisedType string, data []byte) (provider.ContentPart, error) {
	if len(data) == 0 {
		return provider.ContentPart{}, errors.New("attachment is empty")
	}
	if len(data) > AttachmentFileLimit {
		return provider.ContentPart{}, fmt.Errorf("attachment is %d bytes; the per-image limit is %d bytes", len(data), AttachmentFileLimit)
	}
	mediaType, err := supportedImageType(data)
	if err != nil {
		return provider.ContentPart{}, err
	}
	if advertisedType = strings.TrimSpace(strings.ToLower(advertisedType)); advertisedType != "" && advertisedType != mediaType {
		return provider.ContentPart{}, fmt.Errorf("attachment advertises %s but contains %s", advertisedType, mediaType)
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	dir, err := m.currentDir()
	if err != nil {
		return provider.ContentPart{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return provider.ContentPart{}, fmt.Errorf("create session attachment directory: %w", err)
	}
	used, err := attachmentDirBytes(dir)
	if err != nil {
		return provider.ContentPart{}, err
	}
	if used+int64(len(data)) > AttachmentSessionLimit {
		return provider.ContentPart{}, fmt.Errorf("session attachment quota would exceed %d bytes", AttachmentSessionLimit)
	}
	id, err := attachmentID()
	if err != nil {
		return provider.ContentPart{}, err
	}
	path := filepath.Join(dir, id+".bin")
	if err := writeExclusiveDurable(path, data, 0o600, m.openFile); err != nil {
		return provider.ContentPart{}, fmt.Errorf("write session attachment: %w", err)
	}
	digest := sha256.Sum256(data)
	return provider.ContentPart{Type: provider.ContentImage, AttachmentID: id, Name: safeAttachmentName(name), MediaType: mediaType, Size: len(data), SHA256: hex.EncodeToString(digest[:])}, nil
}

// ResolveMessages loads and verifies every referenced image for one provider
// request. Returned message and part slices are independent copies.
func (m *AttachmentManager) ResolveMessages(messages []provider.Message) ([]provider.Message, error) {
	resolved := make([]provider.Message, len(messages))
	copy(resolved, messages)
	for i := range resolved {
		resolved[i].Parts = append([]provider.ContentPart(nil), messages[i].Parts...)
		for j := range resolved[i].Parts {
			part := &resolved[i].Parts[j]
			if part.Type != provider.ContentImage || len(part.Data) > 0 {
				continue
			}
			data, err := m.Resolve(*part)
			if err != nil {
				return nil, fmt.Errorf("resolve attachment %q: %w", part.Name, err)
			}
			part.Data = data
		}
	}
	return resolved, nil
}

// Resolve verifies an attachment reference before returning its bytes.
func (m *AttachmentManager) Resolve(part provider.ContentPart) ([]byte, error) {
	if !validArtifactID(part.AttachmentID) {
		return nil, errors.New("invalid attachment id")
	}
	dir, err := m.currentDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, part.AttachmentID+".bin")
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != int64(part.Size) || info.Size() > AttachmentFileLimit {
		return nil, errors.New("attachment size or file type no longer matches its session record")
	}
	data, err := io.ReadAll(io.LimitReader(f, AttachmentFileLimit+1))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	if part.SHA256 == "" || !strings.EqualFold(part.SHA256, hex.EncodeToString(digest[:])) {
		return nil, errors.New("attachment integrity check failed")
	}
	mediaType, err := supportedImageType(data)
	if err != nil || mediaType != part.MediaType {
		return nil, errors.New("attachment media type no longer matches its session record")
	}
	return data, nil
}

// Remove deletes a just-retained attachment when preparing the rest of the
// same prompt fails before its message is persisted.
func (m *AttachmentManager) Remove(id string) error {
	if !validArtifactID(id) {
		return errors.New("invalid attachment id")
	}
	dir, err := m.currentDir()
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(dir, id+".bin"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func supportedImageType(data []byte) (string, error) {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0]))
	switch mediaType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return mediaType, nil
	default:
		return "", fmt.Errorf("unsupported image type %q; use PNG, JPEG, GIF, or WebP", mediaType)
	}
}

func attachmentID() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate attachment id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func safeAttachmentName(name string) string {
	name = strings.Join(strings.Fields(strings.ToValidUTF8(filepath.Base(name), "�")), " ")
	if name == "" || name == "." {
		return "image"
	}
	runes := []rune(name)
	if len(runes) > 128 {
		name = string(runes[:128])
	}
	return name
}

func attachmentDirBytes(dir string) (int64, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".bin") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return 0, err
		}
		total += info.Size()
	}
	return total, nil
}
