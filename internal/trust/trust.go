// Package trust records which workspaces the user has approved for
// project-controlled configuration. A repository can ship .collomia.json
// containing MCP servers, permission changes, and instructions; none of that
// may take effect until the user trusts the workspace, and trust is
// invalidated whenever the project configuration's content changes.
package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type Status string

const (
	// StatusTrusted means the project configuration matches the approved hash.
	StatusTrusted Status = "trusted"
	// StatusUntrusted means the workspace has never been trusted.
	StatusUntrusted Status = "untrusted"
	// StatusChanged means the project configuration changed since approval.
	StatusChanged Status = "changed"
)

type Record struct {
	ConfigSHA256 string    `json:"config_sha256"`
	TrustedAt    time.Time `json:"trusted_at"`
}

type Store struct {
	Version    int               `json:"version"`
	Workspaces map[string]Record `json:"workspaces"`
	path       string
}

// Path returns the trust database location inside the user configuration
// directory — never inside a workspace, so repositories cannot grant
// themselves trust.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "collomia", "trust.json"), nil
}

func Load() (*Store, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

func LoadFrom(path string) (*Store, error) {
	store := &Store{Version: 1, Workspaces: map[string]Record{}, path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, store); err != nil {
		return nil, err
	}
	if store.Workspaces == nil {
		store.Workspaces = map[string]Record{}
	}
	store.path = path
	return store, nil
}

func Hash(configData []byte) string {
	sum := sha256.Sum256(configData)
	return hex.EncodeToString(sum[:])
}

// Check reports whether workspace's project configuration (configData) is
// approved. An empty configData means the workspace has no project
// configuration, which never requires trust.
func (s *Store) Check(workspace string, configData []byte) Status {
	if len(configData) == 0 {
		return StatusTrusted
	}
	record, ok := s.Workspaces[normalize(workspace)]
	if !ok {
		return StatusUntrusted
	}
	if record.ConfigSHA256 != Hash(configData) {
		return StatusChanged
	}
	return StatusTrusted
}

func (s *Store) Trust(workspace string, configData []byte) error {
	s.Workspaces[normalize(workspace)] = Record{ConfigSHA256: Hash(configData), TrustedAt: time.Now().UTC()}
	return s.save()
}

func (s *Store) Revoke(workspace string) error {
	delete(s.Workspaces, normalize(workspace))
	return s.save()
}

func (s *Store) save() error {
	if s.path == "" {
		return errors.New("trust store has no backing path")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0o600)
}

func normalize(workspace string) string {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return filepath.Clean(workspace)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return filepath.Clean(abs)
}
