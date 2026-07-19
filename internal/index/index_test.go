package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIndexExtractsAcrossLanguages(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "main.go", "package main\n\nfunc HandleRequest() {}\n\ntype Server struct{}\n\nfunc (s *Server) Start() {}\n")
	write(t, dir, "app.py", "class OrderService:\n    def process_order(self):\n        pass\n")
	write(t, dir, "ui.tsx", "export interface Props {}\nexport function Widget() {}\nexport const onClick = () => {}\n")
	write(t, dir, "lib.rs", "pub fn parse_config() {}\npub struct Config {}\n")

	ix := New(dir)
	if _, err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ query, kind, wantPath string; wantLine int }{
		{"HandleRequest", "func", "main.go", 3},
		{"Start", "method", "main.go", 7},
		{"Server", "type", "main.go", 5},
		{"OrderService", "class", "app.py", 1},
		{"process_order", "func", "app.py", 2},
		{"Props", "interface", "ui.tsx", 1},
		{"Widget", "func", "ui.tsx", 2},
		{"onClick", "const", "ui.tsx", 3},
		{"parse_config", "func", "lib.rs", 1},
		{"Config", "struct", "lib.rs", 2},
	}
	for _, c := range cases {
		got := ix.Query(c.query, c.kind, 5)
		if len(got) == 0 {
			t.Errorf("%s (%s): no results", c.query, c.kind)
			continue
		}
		if got[0].Path != c.wantPath || got[0].Line != c.wantLine {
			t.Errorf("%s: got %s:%d want %s:%d", c.query, got[0].Path, got[0].Line, c.wantPath, c.wantLine)
		}
	}
}

func TestIndexIncrementalRefresh(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "a.go", "package a\n\nfunc First() {}\n")
	write(t, dir, "b.go", "package a\n\nfunc Second() {}\n")
	ix := New(dir)
	parsed, err := ix.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if parsed != 2 {
		t.Fatalf("initial parse=%d", parsed)
	}
	// Unchanged tree: nothing re-parsed.
	if parsed, _ = ix.Refresh(); parsed != 0 {
		t.Fatalf("unchanged tree re-parsed %d files", parsed)
	}
	// Touch one file with new content and a new mtime.
	if err := os.WriteFile(path, []byte("package a\n\nfunc Renamed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if parsed, _ = ix.Refresh(); parsed != 1 {
		t.Fatalf("changed file should re-parse exactly 1, got %d", parsed)
	}
	if got := ix.Query("First", "", 5); len(got) != 0 {
		t.Fatalf("stale symbol survived: %+v", got)
	}
	if got := ix.Query("Renamed", "", 5); len(got) != 1 {
		t.Fatalf("new symbol missing: %+v", got)
	}
}

func TestIndexDropsDeletedFiles(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "gone.go", "package a\n\nfunc Doomed() {}\n")
	ix := New(dir)
	ix.Refresh()
	if got := ix.Query("Doomed", "", 5); len(got) != 1 {
		t.Fatal("expected symbol before deletion")
	}
	os.Remove(path)
	ix.Refresh()
	if got := ix.Query("Doomed", "", 5); len(got) != 0 {
		t.Fatalf("deleted file's symbols survived: %+v", got)
	}
}

func TestIndexSkipsIgnoredDirs(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "node_modules/dep/index.js", "function hidden() {}\n")
	write(t, dir, ".git/hooks/x.go", "package x\n\nfunc alsoHidden() {}\n")
	write(t, dir, "src/real.js", "function visible() {}\n")
	ix := New(dir)
	ix.Refresh()
	if got := ix.Query("hidden", "", 5); len(got) != 0 {
		t.Fatalf("node_modules should be skipped: %+v", got)
	}
	if got := ix.Query("visible", "", 5); len(got) != 1 {
		t.Fatalf("src should be indexed: %+v", got)
	}
}

func TestQueryRanking(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.go", "package a\n\nfunc Run() {}\n\nfunc RunAll() {}\n\nfunc DryRun() {}\n")
	ix := New(dir)
	ix.Refresh()
	got := ix.Query("run", "", 10)
	if len(got) != 3 {
		t.Fatalf("results=%d", len(got))
	}
	if got[0].Name != "Run" || got[1].Name != "RunAll" || got[2].Name != "DryRun" {
		t.Fatalf("ranking wrong: %v %v %v", got[0].Name, got[1].Name, got[2].Name)
	}
}
