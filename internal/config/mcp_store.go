package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	mcpServerNameRE  = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	environmentKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	headerNameRE     = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")
)

// ValidateMCPServer validates a single definition without requiring the rest
// of a complete Collomia configuration. This lets lifecycle commands reject a
// bad entry before they modify a file.
func ValidateMCPServer(name string, server MCPServer) []FieldError {
	field := "mcp." + name
	var errs []FieldError
	if !mcpServerNameRE.MatchString(name) {
		errs = append(errs, FieldError{field, "server name must contain only letters, digits, hyphens, or underscores"})
	}
	transport := strings.ToLower(strings.TrimSpace(server.Transport))
	switch transport {
	case "", "stdio":
		if strings.TrimSpace(server.Command) == "" {
			errs = append(errs, FieldError{field + ".command", "required for stdio transport"})
		}
	case "http", "streamable-http":
		if strings.TrimSpace(server.URL) == "" {
			errs = append(errs, FieldError{field + ".url", "required for HTTP transport"})
		} else if !strings.Contains(server.URL, "${") {
			parsed, err := url.Parse(server.URL)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
				errs = append(errs, FieldError{field + ".url", "must be an absolute HTTP(S) URL without embedded credentials"})
			}
		}
	default:
		errs = append(errs, FieldError{field + ".transport", fmt.Sprintf("must be stdio, http, or streamable-http (got %q)", server.Transport)})
	}
	if server.Timeout < 0 {
		errs = append(errs, FieldError{field + ".timeout_seconds", "must not be negative"})
	}
	for key := range server.Env {
		if !environmentKeyRE.MatchString(key) {
			errs = append(errs, FieldError{field + ".env." + key, "must be a valid environment variable name"})
		}
	}
	for key := range server.Headers {
		if !headerNameRE.MatchString(key) {
			errs = append(errs, FieldError{field + ".headers." + key, "must be a valid HTTP header name"})
		}
	}
	return errs
}

// ReadMCPFile returns the MCP entries present in exactly one configuration
// file. It does not apply inheritance, trust, defaults, or environment
// expansion, so callers can safely explain the stored representation.
func ReadMCPFile(path string) (map[string]MCPServer, bool, error) {
	root, exists, err := readConfigObject(path)
	if err != nil || !exists {
		return nil, exists, err
	}
	raw, ok := root["mcp"]
	if !ok {
		return map[string]MCPServer{}, true, nil
	}
	var entries map[string]MCPServer
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, true, fmt.Errorf("parse mcp in %s: %w", path, err)
	}
	if entries == nil {
		entries = map[string]MCPServer{}
	}
	return entries, true, nil
}

// PutMCPServer creates or replaces one server while preserving every other
// top-level field and untouched server entry, including unknown future fields.
// Replacement must be explicitly authorized by the caller.
func PutMCPServer(path, name string, server MCPServer, replace bool) (bool, error) {
	if errs := ValidateMCPServer(name, server); len(errs) > 0 {
		return false, ValidationError{Errors: errs}
	}
	created := false
	err := mutateMCPFile(path, func(entries map[string]json.RawMessage) (bool, error) {
		if _, exists := entries[name]; exists && !replace {
			return false, fmt.Errorf("MCP server %q already exists in %s; re-run with --yes to replace it", name, path)
		}
		data, err := json.Marshal(server)
		if err != nil {
			return false, err
		}
		_, existed := entries[name]
		entries[name] = data
		created = !existed
		return true, nil
	})
	return created, err
}

// RemoveMCPServer removes one server from exactly one configuration layer.
func RemoveMCPServer(path, name string) (bool, error) {
	removed := false
	err := mutateMCPFile(path, func(entries map[string]json.RawMessage) (bool, error) {
		if _, ok := entries[name]; !ok {
			return false, nil
		}
		delete(entries, name)
		removed = true
		return true, nil
	})
	return removed, err
}

// SetMCPDisabled changes only the disabled field of an entry in one layer.
// Unknown fields on that entry remain intact.
func SetMCPDisabled(path, name string, disabled bool) (bool, error) {
	changed := false
	err := mutateMCPFile(path, func(entries map[string]json.RawMessage) (bool, error) {
		raw, ok := entries[name]
		if !ok {
			return false, nil
		}
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entry); err != nil {
			return false, fmt.Errorf("parse MCP server %q in %s: %w", name, path, err)
		}
		if disabled {
			entry["disabled"] = json.RawMessage("true")
		} else {
			delete(entry, "disabled")
		}
		updated, err := json.Marshal(entry)
		if err != nil {
			return false, err
		}
		entries[name] = updated
		changed = true
		return true, nil
	})
	return changed, err
}

func mutateMCPFile(path string, mutate func(map[string]json.RawMessage) (bool, error)) error {
	root, exists, err := readConfigObject(path)
	if err != nil {
		return err
	}
	if !exists {
		root = map[string]json.RawMessage{}
		schema, _ := json.Marshal(CurrentSchemaVersion)
		root["schema_version"] = schema
	}
	entries := map[string]json.RawMessage{}
	if raw, ok := root["mcp"]; ok {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return fmt.Errorf("parse mcp in %s: %w", path, err)
		}
		if entries == nil {
			entries = map[string]json.RawMessage{}
		}
	}
	write, err := mutate(entries)
	if err != nil || !write {
		return err
	}
	mcpData, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	root["mcp"] = mcpData
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return writeConfigAtomically(path, append(data, '\n'))
}

func readConfigObject(path string) (map[string]json.RawMessage, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read config %s: %w", path, err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, true, fmt.Errorf("parse config %s: %w", path, err)
	}
	if root == nil {
		return nil, true, fmt.Errorf("parse config %s: root must be an object", path)
	}
	if raw, ok := root["schema_version"]; ok {
		var version int
		if err := json.Unmarshal(raw, &version); err != nil {
			return nil, true, fmt.Errorf("parse config %s: schema_version: %w", path, err)
		}
		if version > CurrentSchemaVersion {
			return nil, true, fmt.Errorf("config %s: schema_version %d is newer than this build supports (%d); upgrade collo", path, version, CurrentSchemaVersion)
		}
	}
	return root, true, nil
}

func writeConfigAtomically(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	temp, err := os.CreateTemp(dir, ".collomia-mcp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	// On Unix this preserves the complete permission mode. Go maps only the
	// owner-write bit to Windows' read-only attribute; Windows access remains
	// governed by the ACL inherited from the containing user/project directory.
	if err = temp.Chmod(mode); err == nil {
		_, err = temp.Write(data)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

// MCPNames returns sorted names for deterministic lifecycle output.
func MCPNames(entries map[string]MCPServer) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
