package userconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirUsesUserHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".collomia"); dir != want {
		t.Fatalf("Dir()=%q, want %q", dir, want)
	}
	path, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".collomia", "config.json"); path != want {
		t.Fatalf("ConfigPath()=%q, want %q", path, want)
	}
}

func TestGlobalArtifactPathsShareOneRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	for _, elements := range [][]string{
		{"config.json"},
		{"skills"},
		{"sessions"},
		{"logs"},
		{"audit"},
		{"trust.json"},
		{"mcp-pins.json"},
	} {
		got, err := Path(elements...)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(append([]string{home, ".collomia"}, elements...)...)
		if got != want {
			t.Errorf("Path(%q)=%q, want %q", elements, got, want)
		}
	}
}
