package supportbundle

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateDefaultBundleExcludesSensitiveMaterial(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	secret := "sk-secret-support-bundle-value"
	providerName := "private-customer-provider"
	configData := `{
  "default_provider": "` + providerName + `",
  "default_model": "private-model-name",
  "providers": {
    "` + providerName + `": {
      "type": "openai-compatible",
      "base_url": "https://private-endpoint.example/v1",
      "api_key": "` + secret + `",
      "model": "private-model-name"
    }
  }
}`
	if err := os.MkdirAll(filepath.Join(home, ".collomia"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".collomia", "config.json"), []byte(configData), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(home, ".collomia", "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	const failureID = "err-0123456789abcdef"
	logData, err := json.Marshal(map[string]string{
		"time":       "2026-07-21T12:00:00Z",
		"msg":        "event",
		"failure_id": failureID,
		"error":      "secret=" + secret + " workspace=" + workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	logData = append(logData, '\n')
	if err := os.WriteFile(filepath.Join(logs, "debug.log"), logData, 0o600); err != nil {
		t.Fatal(err)
	}

	output := filepath.Join(t.TempDir(), "support.zip")
	result, err := Create(Options{
		Workspace: workspace, Output: output, Version: "collo test",
		Capabilities: "# capabilities\n", Now: func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != output || result.ID == "" || result.LogFiles != 0 {
		t.Fatalf("result=%+v", result)
	}
	contents := readBundle(t, output)
	for _, name := range []string{"README.txt", "manifest.json", "capabilities.md"} {
		if _, ok := contents[name]; !ok {
			t.Fatalf("bundle missing %s: %v", name, keys(contents))
		}
	}
	if _, ok := contents["logs/log-1.txt"]; ok {
		t.Fatal("default support bundle unexpectedly included a log")
	}
	all := joined(contents)
	for _, forbidden := range []string{secret, providerName, "private-model-name", "private-endpoint.example", workspace, home} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("bundle leaked %q:\n%s", forbidden, all)
		}
	}
	var manifest manifest
	if err := json.Unmarshal([]byte(contents["manifest.json"]), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != SchemaVersion || manifest.Privacy.LogsRequested || manifest.Privacy.LogsIncluded || manifest.Health.Providers.Types["openai-compatible"] < 1 {
		t.Fatalf("manifest=%+v", manifest)
	}
	if len(manifest.Health.RecentFailureIDs) != 1 || manifest.Health.RecentFailureIDs[0] != failureID {
		t.Fatalf("recent failure IDs=%v", manifest.Health.RecentFailureIDs)
	}
	for _, layer := range manifest.Health.Configuration.Layers {
		for _, key := range layer.Keys {
			if strings.Contains(key, providerName) {
				t.Fatalf("provider name leaked through setting key %q", key)
			}
		}
	}
}

func TestCreateWithLogsBoundsAndRedactsThem(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	secret := "literal-super-secret-value"
	t.Setenv("COLLOMIA_SUPPORT_TEST_KEY", secret)
	configData := `{"providers":{"fixture":{"type":"openai","api_key_env":"COLLOMIA_SUPPORT_TEST_KEY","model":"m"}},"default_provider":"fixture","default_model":"m"}`
	root := filepath.Join(home, ".collomia")
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(configData), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		text := strings.Repeat("line secret="+secret+" path="+workspace+"\n", 20)
		path := filepath.Join(root, "logs", "debug-"+string(rune('a'+i))+".log")
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		when := time.Date(2026, 7, 21, 12, i, 0, 0, time.UTC)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(t.TempDir(), "support.zip")
	result, err := Create(Options{Workspace: workspace, Output: output, Version: "test", IncludeLogs: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.LogFiles != maxIncludedLogs {
		t.Fatalf("included logs=%d", result.LogFiles)
	}
	contents := readBundle(t, output)
	all := joined(contents)
	if strings.Contains(all, secret) || strings.Contains(all, workspace) || strings.Contains(all, home) {
		t.Fatalf("included logs were not sanitized:\n%s", all)
	}
	if !strings.Contains(contents["logs/log-1.txt"], "[redacted]") || !strings.Contains(contents["logs/log-1.txt"], "$WORKSPACE") {
		t.Fatalf("sanitized log missing markers: %q", contents["logs/log-1.txt"])
	}
	var data manifest
	if err := json.Unmarshal([]byte(contents["manifest.json"]), &data); err != nil {
		t.Fatal(err)
	}
	if !data.Privacy.LogsRequested || !data.Privacy.LogsIncluded || data.Privacy.LogFiles != maxIncludedLogs || data.Privacy.LogCollection != "included" {
		t.Fatalf("privacy=%+v", data.Privacy)
	}
}

func TestCreateDoesNotCopyValidationDetailsIntoBundle(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	privateName := "secret-customer-provider-name"
	root := filepath.Join(home, ".collomia")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	configData := `{"default_provider":"` + privateName + `","providers":{"different-private-name":{"type":"openai","base_url":"https://example.invalid","model":"m"}}}`
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(configData), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "support.zip")
	if _, err := Create(Options{Workspace: workspace, Output: output, Version: "test"}); err != nil {
		t.Fatal(err)
	}
	all := joined(readBundle(t, output))
	for _, forbidden := range []string{privateName, "different-private-name", "example.invalid"} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("validation detail leaked %q:\n%s", forbidden, all)
		}
	}
}

func TestCreateRefusesToOverwriteExistingBundle(t *testing.T) {
	output := filepath.Join(t.TempDir(), "existing.zip")
	if err := os.WriteFile(output, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Create(Options{Workspace: t.TempDir(), Output: output, Version: "test"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error=%v", err)
	}
	data, readErr := os.ReadFile(output)
	if readErr != nil || string(data) != "keep" {
		t.Fatalf("existing output changed: data=%q err=%v", data, readErr)
	}
}

func TestArchivePublishCannotReplaceLateDestination(t *testing.T) {
	output := filepath.Join(t.TempDir(), "claimed.zip")
	if err := os.WriteFile(output, []byte("created concurrently"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := writeArchive(output, time.Now(), []archiveFile{{name: "manifest.json", data: []byte("new archive")}})
	if err == nil {
		t.Fatal("archive publish replaced an existing destination")
	}
	data, readErr := os.ReadFile(output)
	if readErr != nil || string(data) != "created concurrently" {
		t.Fatalf("late destination changed: data=%q err=%v", data, readErr)
	}
}

func readBundle(t *testing.T, path string) map[string]string {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	contents := map[string]string{}
	for _, item := range archive.File {
		reader, err := item.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		_ = reader.Close()
		contents[item.Name] = string(data)
	}
	return contents
}

func keys(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	return out
}

func joined(values map[string]string) string {
	var b strings.Builder
	for name, value := range values {
		b.WriteString(name)
		b.WriteByte('\n')
		b.WriteString(value)
	}
	return b.String()
}
