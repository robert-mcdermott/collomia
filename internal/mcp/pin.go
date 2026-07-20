package mcpclient

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/userconfig"
)

// pinRecord remembers what a trusted server looked like the last time it was
// seen, so material changes are surfaced instead of silently accepted.
type pinRecord struct {
	DefinitionSHA256 string    `json:"definition_sha256"`
	ServerName       string    `json:"server_name,omitempty"`
	ServerVersion    string    `json:"server_version,omitempty"`
	FirstSeen        time.Time `json:"first_seen"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// pinStore lives under the per-user Collomia root (like the trust database) —
// never inside a workspace — keyed by workspace and server name.
type pinStore struct {
	Version int                  `json:"version"`
	Servers map[string]pinRecord `json:"servers"`
	path    string
}

func pinPath() (string, error) {
	return userconfig.Path("mcp-pins.json")
}

func loadPins() (*pinStore, error) {
	path, err := pinPath()
	if err != nil {
		return nil, err
	}
	store := &pinStore{Version: 1, Servers: map[string]pinRecord{}, path: path}
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
	if store.Servers == nil {
		store.Servers = map[string]pinRecord{}
	}
	store.path = path
	return store, nil
}

func (p *pinStore) save() error {
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.path, append(data, '\n'), 0o600)
}

func pinKey(workspace, server string) string {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		abs = filepath.Clean(workspace)
	}
	return filepath.Clean(abs) + "|" + server
}

// definitionFingerprint hashes the parts of a server definition that decide
// what runs and where data goes: transport, command, arguments, and URL, plus
// the *names* of environment variables and headers. Values are excluded so a
// rotated token does not read as a definition change.
func definitionFingerprint(cfg appconfig.MCPServer) string {
	envKeys := make([]string, 0, len(cfg.Env))
	for key := range cfg.Env {
		envKeys = append(envKeys, key)
	}
	sort.Strings(envKeys)
	headerKeys := make([]string, 0, len(cfg.Headers))
	for key := range cfg.Headers {
		headerKeys = append(headerKeys, key)
	}
	sort.Strings(headerKeys)
	payload, _ := json.Marshal(struct {
		Transport  string   `json:"transport"`
		Command    string   `json:"command"`
		Args       []string `json:"args"`
		URL        string   `json:"url"`
		EnvKeys    []string `json:"env_keys"`
		HeaderKeys []string `json:"header_keys"`
	}{cfg.Transport, cfg.Command, cfg.Args, cfg.URL, envKeys, headerKeys})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
