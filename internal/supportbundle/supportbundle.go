// Package supportbundle creates a privacy-conscious diagnostic archive for
// troubleshooting Collomia. The default archive deliberately excludes
// configuration values, environment variables, prompts, transcripts, source
// files, audit records, and logs.
package supportbundle

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/term"

	"github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/failureid"
	"github.com/robert-mcdermott/collomia/internal/logging"
	"github.com/robert-mcdermott/collomia/internal/redact"
	"github.com/robert-mcdermott/collomia/internal/sandbox"
)

const (
	SchemaVersion     = 1
	maxIncludedLogs   = 5
	maxIncludedLog    = 1 << 20
	maxIncludedLogSum = 3 << 20
)

// Options controls one support bundle. Output must not already exist.
type Options struct {
	Workspace    string
	Output       string
	Version      string
	IncludeLogs  bool
	Capabilities string
	Now          func() time.Time
}

// Result describes the archive that was created without exposing its
// collected diagnostic data.
type Result struct {
	Path      string
	ID        string
	LogFiles  int
	CreatedAt time.Time
}

type manifest struct {
	SchemaVersion int          `json:"schema_version"`
	BundleID      string       `json:"bundle_id"`
	CreatedAt     time.Time    `json:"created_at"`
	Collomia      string       `json:"collomia"`
	Platform      platformInfo `json:"platform"`
	Privacy       privacyInfo  `json:"privacy"`
	Health        healthInfo   `json:"health"`
}

type platformInfo struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Terminal     bool   `json:"terminal"`
}

type privacyInfo struct {
	LogsRequested bool     `json:"logs_requested"`
	LogsIncluded  bool     `json:"logs_included"`
	LogFiles      int      `json:"log_files"`
	LogCollection string   `json:"log_collection"`
	Excluded      []string `json:"excluded"`
}

type healthInfo struct {
	Configuration configInfo    `json:"configuration"`
	Workspace     workspaceInfo `json:"workspace"`
	Providers     providerInfo  `json:"providers"`
	MCP           mcpInfo       `json:"mcp"`
	Sandbox       sandboxInfo   `json:"sandbox"`
	Extensions    extensionInfo `json:"extensions"`
	// RecentFailureIDs contains only opaque correlation values extracted from
	// bounded local debug-log tails. No log messages or attributes are copied.
	RecentFailureIDs []string `json:"recent_failure_ids,omitempty"`
}

type configInfo struct {
	Status           string         `json:"status"`
	Error            string         `json:"error,omitempty"`
	SchemaVersion    int            `json:"schema_version,omitempty"`
	ProjectTrust     string         `json:"project_trust"`
	Layers           []layerInfo    `json:"layers,omitempty"`
	SettingOrigins   map[string]int `json:"setting_origins,omitempty"`
	StrictValidation string         `json:"strict_validation"`
	StrictError      string         `json:"strict_error,omitempty"`
}

type layerInfo struct {
	Name    string   `json:"name"`
	Applied bool     `json:"applied"`
	Keys    []string `json:"keys,omitempty"`
}

type workspaceInfo struct {
	ProjectConfig string `json:"project_config"`
	GitAvailable  bool   `json:"git_available"`
	GitRepository bool   `json:"git_repository"`
}

type providerInfo struct {
	Configured int            `json:"configured"`
	Types      map[string]int `json:"types,omitempty"`
	AuthModes  map[string]int `json:"auth_modes,omitempty"`
}

type mcpInfo struct {
	Configured int `json:"configured"`
	Enabled    int `json:"enabled"`
	Disabled   int `json:"disabled"`
	Trusted    int `json:"trusted"`
	Untrusted  int `json:"untrusted"`
}

type sandboxInfo struct {
	Mode             string   `json:"mode"`
	Backend          string   `json:"backend"`
	Available        bool     `json:"available"`
	Error            string   `json:"error,omitempty"`
	Capabilities     string   `json:"capabilities,omitempty"`
	Network          string   `json:"network"`
	OutsideUserReads string   `json:"outside_user_reads"`
	Missing          []string `json:"missing,omitempty"`
}

type extensionInfo struct {
	Agents int `json:"agent_profiles"`
	Hooks  int `json:"hooks"`
	LSPs   int `json:"language_servers"`
}

type archiveFile struct {
	name string
	data []byte
}

var defaultExclusions = []string{
	"configuration values and credential material",
	"environment variable names and values",
	"provider endpoints, models, deployments, profiles, and provider names",
	"MCP server names, URLs, commands, arguments, headers, and environment",
	"workspace path, source files, prompts, transcripts, sessions, and audit records",
	"debug logs unless --include-logs is explicitly requested",
}

// DefaultPath returns a timestamped bundle name in dir.
func DefaultPath(dir string, now time.Time) string {
	return filepath.Join(dir, "collomia-support-"+now.UTC().Format("20060102-150405")+".zip")
}

// Create performs local, read-only health inspection and atomically creates a
// ZIP archive. It never initializes providers or MCP clients and never makes a
// network request.
func Create(opts Options) (Result, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	created := opts.Now().UTC()
	workspace, err := filepath.Abs(opts.Workspace)
	if err != nil {
		return Result{}, fmt.Errorf("resolve workspace: %w", err)
	}
	output, err := filepath.Abs(opts.Output)
	if err != nil {
		return Result{}, fmt.Errorf("resolve output: %w", err)
	}
	if _, err := os.Stat(output); err == nil {
		return Result{}, fmt.Errorf("support bundle already exists: %s", output)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	}

	id := bundleID()
	data, redactor := inspect(workspace, opts.Version, id, created, opts.IncludeLogs)
	data.Privacy.LogsRequested = opts.IncludeLogs
	files := []archiveFile{}
	if opts.Capabilities != "" {
		files = append(files, archiveFile{name: "capabilities.md", data: []byte(opts.Capabilities)})
	}
	if opts.IncludeLogs {
		logs, logErr := collectLogs(workspace, redactor)
		if logErr != nil {
			// Filesystem errors can contain user-controlled path components. The
			// manifest records the state without copying those details into an
			// archive intended to be safe to inspect and share.
			data.Privacy.LogCollection = "unavailable"
		} else {
			files = append(files, logs...)
			data.Privacy.LogFiles = len(logs)
			if len(logs) == 0 {
				data.Privacy.LogCollection = "no logs found"
			} else {
				data.Privacy.LogsIncluded = true
				data.Privacy.LogCollection = "included"
			}
		}
	}
	manifestData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return Result{}, err
	}
	manifestData = append(manifestData, '\n')
	files = append([]archiveFile{
		{name: "README.txt", data: []byte(bundleReadme(opts.IncludeLogs))},
		{name: "manifest.json", data: manifestData},
	}, files...)
	if err := writeArchive(output, created, files); err != nil {
		return Result{}, err
	}
	return Result{Path: output, ID: id, LogFiles: data.Privacy.LogFiles, CreatedAt: created}, nil
}

func inspect(workspace, buildVersion, id string, created time.Time, resolveLogSecrets bool) (manifest, *redact.Redactor) {
	data := manifest{
		SchemaVersion: SchemaVersion,
		BundleID:      id,
		CreatedAt:     created,
		Collomia:      buildVersion,
		Platform: platformInfo{
			OS: runtime.GOOS, Architecture: runtime.GOARCH,
			Terminal: term.IsTerminal(int(os.Stdout.Fd())),
		},
		Privacy: privacyInfo{LogCollection: "not requested", Excluded: append([]string(nil), defaultExclusions...)},
	}
	common := redact.New()
	loadOptions := config.LoadOptions{SkipEnvironmentExpansion: true}
	cfg, err := config.LoadWithOptions(workspace, loadOptions)
	if err != nil {
		if resolveLogSecrets {
			common = configuredRedactor(cfg)
		}
		// Validation errors may echo user-defined provider/MCP names, header
		// names, paths, patterns, or invalid values. Keep support archives
		// anonymous and direct the user to the local validator for specifics.
		data.Health.Configuration = configInfo{Status: "error", Error: "configuration could not be loaded; run collo config validate --strict in this workspace", ProjectTrust: projectTrust(workspace, cfg), StrictValidation: "not run"}
	} else {
		if resolveLogSecrets {
			common = configuredRedactor(cfg)
		}
		data.Health.Configuration = summarizeConfig(workspace, cfg)
		loadOptions.Strict = true
		if _, strictErr := config.LoadWithOptions(workspace, loadOptions); strictErr != nil {
			data.Health.Configuration.StrictValidation = "failed"
			data.Health.Configuration.StrictError = "strict validation failed; run collo config validate --strict in this workspace"
		} else {
			data.Health.Configuration.StrictValidation = "passed"
		}
		data.Health.Providers = summarizeProviders(cfg)
		data.Health.MCP = summarizeMCP(cfg)
		data.Health.Extensions = extensionInfo{Agents: len(cfg.Agents), Hooks: hookCount(cfg), LSPs: len(cfg.LSP)}
	}
	data.Health.Workspace = inspectWorkspace(workspace)
	data.Health.Sandbox = inspectSandbox(workspace, cfg, err, common)
	data.Health.RecentFailureIDs = recentFailureIDs()
	return data, common
}

func recentFailureIDs() []string {
	dir, err := logging.Dir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	var candidates []candidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".log") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr == nil && info.Mode().IsRegular() {
			candidates = append(candidates, candidate{path: filepath.Join(dir, entry.Name()), mod: info.ModTime()})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].mod.After(candidates[j].mod) })
	if len(candidates) > maxIncludedLogs {
		candidates = candidates[:maxIncludedLogs]
	}
	seen := map[string]bool{}
	const maxIDs = 8
	var result []string
	for _, candidate := range candidates {
		ids := failureIDsFromLogTail(candidate.path)
		for i := len(ids) - 1; i >= 0 && len(result) < maxIDs; i-- {
			if !seen[ids[i]] {
				seen[ids[i]] = true
				result = append(result, ids[i])
			}
		}
		if len(result) == maxIDs {
			break
		}
	}
	return result
}

func failureIDsFromLogTail(path string) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil
	}
	start := max(int64(0), info.Size()-maxIncludedLog)
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil
	}
	scanner := bufio.NewScanner(io.LimitReader(file, maxIncludedLog))
	scanner.Buffer(make([]byte, 4096), maxIncludedLog)
	if start > 0 {
		// The tail may begin in a partial JSON object.
		scanner.Scan()
	}
	var ids []string
	for scanner.Scan() {
		var record struct {
			FailureID string `json:"failure_id"`
		}
		if json.Unmarshal(scanner.Bytes(), &record) == nil && failureid.Valid(record.FailureID) {
			ids = append(ids, record.FailureID)
		}
	}
	return ids
}

func summarizeConfig(workspace string, cfg config.Config) configInfo {
	info := configInfo{Status: "ok", SchemaVersion: cfg.SchemaVersion, ProjectTrust: projectTrust(workspace, cfg), StrictValidation: "not run"}
	for _, layer := range cfg.Layers {
		item := layerInfo{Name: layer.Name, Applied: layer.Applied}
		seen := map[string]bool{}
		for _, key := range layer.Keys {
			key = anonymousConfigKey(key)
			if key != "" && !seen[key] {
				seen[key] = true
				item.Keys = append(item.Keys, key)
			}
		}
		sort.Strings(item.Keys)
		info.Layers = append(info.Layers, item)
	}
	info.SettingOrigins = map[string]int{}
	for _, origin := range cfg.Origins {
		info.SettingOrigins[origin]++
	}
	return info
}

func projectTrust(workspace string, cfg config.Config) string {
	if _, err := os.Stat(filepath.Join(workspace, config.ProjectFile)); errors.Is(err, os.ErrNotExist) {
		return "no project configuration"
	} else if err != nil {
		return "project configuration status unavailable"
	}
	if cfg.ProjectTrusted {
		return "trusted"
	}
	return "quarantined"
}

func inspectWorkspace(workspace string) workspaceInfo {
	info := workspaceInfo{ProjectConfig: "absent"}
	if _, err := os.Stat(filepath.Join(workspace, config.ProjectFile)); err == nil {
		info.ProjectConfig = "present"
	} else if !errors.Is(err, os.ErrNotExist) {
		info.ProjectConfig = "unavailable"
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return info
	}
	info.GitAvailable = true
	cmd := exec.Command(git, "-C", workspace, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.Output()
	info.GitRepository = err == nil && strings.TrimSpace(string(out)) == "true"
	return info
}

func summarizeProviders(cfg config.Config) providerInfo {
	info := providerInfo{Configured: len(cfg.Providers), Types: map[string]int{}, AuthModes: map[string]int{}}
	for _, provider := range cfg.Providers {
		typ := strings.TrimSpace(provider.Type)
		if typ == "" {
			typ = "unspecified"
		}
		auth := strings.TrimSpace(provider.Auth)
		if auth == "" {
			auth = "default"
		}
		info.Types[typ]++
		info.AuthModes[auth]++
	}
	return info
}

func summarizeMCP(cfg config.Config) mcpInfo {
	info := mcpInfo{Configured: len(cfg.MCP)}
	for _, server := range cfg.MCP {
		if server.Disabled {
			info.Disabled++
		} else {
			info.Enabled++
		}
		if server.Trusted {
			info.Trusted++
		} else {
			info.Untrusted++
		}
	}
	return info
}

func inspectSandbox(workspace string, cfg config.Config, cfgErr error, redactor *redact.Redactor) sandboxInfo {
	info := sandboxInfo{Mode: "unknown", Network: "unknown", OutsideUserReads: "unknown"}
	if cfgErr == nil {
		info.Mode = cfg.Permissions.Sandbox
		if info.Mode == "" {
			info.Mode = string(sandbox.ModeOff)
		}
		if cfg.Permissions.SandboxAllowNetwork {
			info.Network = "allowed"
		} else {
			info.Network = "denied when enforced"
		}
		if cfg.Permissions.SandboxAllowReadOutsideWorkspace {
			info.OutsideUserReads = "allowed"
		} else {
			info.OutsideUserReads = "confined when enforced"
		}
	}
	backend := sandbox.ForPlatform()
	info.Backend = backend.Name()
	if err := backend.Available(); err != nil {
		info.Error = sanitize(err.Error(), workspace, redactor)
		return info
	}
	info.Available = true
	info.Capabilities = backend.Capabilities().Summary()
	if cfgErr == nil && info.Mode != string(sandbox.ModeOff) {
		policy := sandbox.Policy{WorkspaceRoot: workspace, AllowNetwork: cfg.Permissions.SandboxAllowNetwork, ConstrainReads: !cfg.Permissions.SandboxAllowReadOutsideWorkspace}
		info.Missing = backend.Capabilities().Missing(policy)
	}
	return info
}

func configuredRedactor(cfg config.Config) *redact.Redactor {
	r := redact.New()
	for _, providerConfig := range cfg.Providers {
		r.AddSecret(expandEnvironment(providerConfig.APIKey))
		if providerConfig.APIKeyEnv != "" {
			r.AddSecret(os.Getenv(providerConfig.APIKeyEnv))
		}
		for _, value := range providerConfig.Headers {
			r.AddSecret(expandEnvironment(value))
		}
		if providerConfig.Type == "bedrock" {
			r.AddSecret(os.Getenv("AWS_BEARER_TOKEN_BEDROCK"))
		}
		if providerConfig.Auth == "entra" {
			r.AddSecret(os.Getenv("AZURE_CLIENT_SECRET"))
			r.AddSecret(os.Getenv("AZURE_CLIENT_CERTIFICATE_PASSWORD"))
		}
	}
	for _, server := range cfg.MCP {
		for _, value := range server.Env {
			r.AddSecret(expandEnvironment(value))
		}
		for _, value := range server.Headers {
			r.AddSecret(expandEnvironment(value))
		}
	}
	return r
}

func expandEnvironment(value string) string {
	return os.Expand(value, func(name string) string { return os.Getenv(name) })
}

func hookCount(cfg config.Config) int {
	count := 0
	for _, hooks := range cfg.Hooks {
		count += len(hooks)
	}
	return count
}

func anonymousConfigKey(key string) string {
	parts := strings.Split(strings.TrimSpace(key), ".")
	if len(parts) == 0 {
		return ""
	}
	switch parts[0] {
	case "providers", "mcp", "agents", "lsp", "hooks":
		if len(parts) > 1 {
			parts[1] = "*"
		}
	}
	return strings.Join(parts, ".")
}

func collectLogs(workspace string, redactor *redact.Redactor) ([]archiveFile, error) {
	dir, err := logging.Dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	var candidates []candidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		candidates = append(candidates, candidate{path: filepath.Join(dir, entry.Name()), mod: info.ModTime()})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].mod.After(candidates[j].mod) })
	if len(candidates) > maxIncludedLogs {
		candidates = candidates[:maxIncludedLogs]
	}
	remaining := maxIncludedLogSum
	var files []archiveFile
	for _, candidate := range candidates {
		if remaining <= 0 {
			break
		}
		limit := min(maxIncludedLog, remaining)
		file, err := os.Open(candidate.path)
		if err != nil {
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(file, int64(limit)+1))
		_ = file.Close()
		if readErr != nil {
			continue
		}
		truncated := len(data) > limit
		if truncated {
			data = data[:limit]
		}
		text := sanitize(string(data), workspace, redactor)
		if truncated {
			text += "\n[log truncated by support bundle]\n"
		}
		files = append(files, archiveFile{name: fmt.Sprintf("logs/log-%d.txt", len(files)+1), data: []byte(text)})
		remaining -= len(data)
	}
	return files, nil
}

func sanitize(text, workspace string, redactor *redact.Redactor) string {
	if redactor == nil {
		redactor = redact.New()
	}
	text = redactor.Redact(text)
	if workspace != "" {
		text = strings.ReplaceAll(text, workspace, "$WORKSPACE")
		text = strings.ReplaceAll(text, filepath.ToSlash(workspace), "$WORKSPACE")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		text = strings.ReplaceAll(text, home, "~")
		text = strings.ReplaceAll(text, filepath.ToSlash(home), "~")
	}
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, text)
}

func bundleReadme(logs bool) string {
	logText := "Debug logs were not included."
	if logs {
		logText = "Up to five recent debug logs were included because --include-logs was explicitly requested. They were bounded, had configured/common credential values redacted, and had home/workspace paths normalized. Redaction is defense in depth: inspect the archive before sharing it."
	}
	return "Collomia support bundle\n\n" +
		"This archive was generated locally without contacting providers, MCP servers, or any other network service.\n\n" +
		"The default bundle excludes configuration values, environment variables, credentials, provider endpoints/models, MCP definitions, workspace paths, source files, prompts, transcripts, sessions, audit records, and debug logs.\n\n" +
		"The manifest may include up to eight recent opaque failure IDs read from bounded debug-log tails. IDs contain no error text or user data and are included only to correlate a report with diagnostics the user chooses to share separately.\n\n" +
		logText + "\n\nAlways review this archive before sending it to another person or attaching it to an issue.\n"
}

func writeArchive(output string, created time.Time, files []archiveFile) (retErr error) {
	dir := filepath.Dir(output)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".collomia-support-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		if retErr != nil {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	zw := zip.NewWriter(temp)
	for _, file := range files {
		header := &zip.FileHeader{Name: filepath.ToSlash(file.name), Method: zip.Deflate}
		header.SetMode(0o600)
		header.SetModTime(created)
		entry, err := zw.CreateHeader(header)
		if err != nil {
			_ = zw.Close()
			return err
		}
		if _, err := io.Copy(entry, bytes.NewReader(file.data)); err != nil {
			_ = zw.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	// Linking the completed same-directory temporary file publishes the
	// archive atomically and, unlike rename on Unix, can never replace a path
	// another process created after our initial existence check.
	if err := os.Link(tempPath, output); err != nil {
		return err
	}
	// The output link now names the complete synced archive. A failure to
	// remove the private temporary alias must not make callers retry and create
	// a second bundle, so cleanup remains best effort.
	_ = os.Remove(tempPath)
	return nil
}

func bundleID() string {
	data := make([]byte, 6)
	if _, err := rand.Read(data); err == nil {
		return hex.EncodeToString(data)
	}
	return fmt.Sprintf("%012x", time.Now().UnixNano()&0xffffffffffff)
}
