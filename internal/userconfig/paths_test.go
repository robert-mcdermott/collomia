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
