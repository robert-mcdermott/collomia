// Package index maintains an incremental, ignore-aware symbol index of the
// workspace so the agent can jump to definitions in large repositories
// without re-scanning every file on every query. Extraction is line-regex
// based per language — deliberately simple and dependency-free; it indexes
// definitions (functions, types, classes, methods, constants), not every
// reference.
package index

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Symbol is one extracted definition.
type Symbol struct {
	Name string
	Kind string // func, method, type, class, struct, interface, const, var, enum
	Path string // workspace-relative, forward slashes
	Line int    // 1-based
}

// fileEntry caches one file's symbols keyed by its modification identity.
type fileEntry struct {
	modTime int64
	size    int64
	symbols []Symbol
}

// Index is safe for concurrent use. Refresh walks the workspace and
// re-extracts only files whose size or mtime changed.
type Index struct {
	workspace string
	mu        sync.Mutex
	files     map[string]*fileEntry // keyed by relative path
}

func New(workspace string) *Index {
	return &Index{workspace: workspace, files: map[string]*fileEntry{}}
}

// languagePatterns maps file extensions to definition-extraction rules.
// Each pattern's first capture group is the symbol name.
type rule struct {
	kind string
	re   *regexp.Regexp
}

var languageRules = map[string][]rule{
	".go": {
		{"func", regexp.MustCompile(`^func\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)},
		{"method", regexp.MustCompile(`^func\s+\([^)]+\)\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)},
		{"type", regexp.MustCompile(`^type\s+([A-Za-z_][A-Za-z0-9_]*)\s`)},
		{"const", regexp.MustCompile(`^const\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{"var", regexp.MustCompile(`^var\s+([A-Za-z_][A-Za-z0-9_]*)`)},
	},
	".py": {
		{"func", regexp.MustCompile(`^\s*def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)},
		{"class", regexp.MustCompile(`^\s*class\s+([A-Za-z_][A-Za-z0-9_]*)\s*[(:]`)},
	},
	".js": jsRules, ".jsx": jsRules, ".ts": tsRules, ".tsx": tsRules,
	".rs": {
		{"func", regexp.MustCompile(`^\s*(?:pub\s+)?(?:async\s+)?fn\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{"struct", regexp.MustCompile(`^\s*(?:pub\s+)?struct\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{"enum", regexp.MustCompile(`^\s*(?:pub\s+)?enum\s+([A-Za-z_][A-Za-z0-9_]*)`)},
		{"trait", regexp.MustCompile(`^\s*(?:pub\s+)?trait\s+([A-Za-z_][A-Za-z0-9_]*)`)},
	},
}

var jsRules = []rule{
	{"func", regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][A-Za-z0-9_$]*)`)},
	{"class", regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?class\s+([A-Za-z_$][A-Za-z0-9_$]*)`)},
	{"const", regexp.MustCompile(`^\s*(?:export\s+)?const\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(?:async\s*)?(?:\(|function)`)},
}

var tsRules = append(append([]rule{}, jsRules...),
	rule{"interface", regexp.MustCompile(`^\s*(?:export\s+)?interface\s+([A-Za-z_$][A-Za-z0-9_$]*)`)},
	rule{"type", regexp.MustCompile(`^\s*(?:export\s+)?type\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=`)},
	rule{"enum", regexp.MustCompile(`^\s*(?:export\s+)?enum\s+([A-Za-z_$][A-Za-z0-9_$]*)`)},
)

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, "target": true, "__pycache__": true, ".venv": true,
	"venv": true, ".idea": true, ".vscode": true,
}

const maxIndexFileSize = 1 << 20 // 1 MiB; larger files are skipped

// Refresh re-walks the workspace, updating only changed files. It returns
// how many files were (re)parsed.
func (ix *Index) Refresh() (parsed int, err error) {
	seen := map[string]bool{}
	walkErr := filepath.WalkDir(ix.workspace, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if d.IsDir() {
			if skipDirs[d.Name()] || (strings.HasPrefix(d.Name(), ".") && d.Name() != "." && path != ix.workspace) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if _, ok := languageRules[ext]; !ok {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || info.Size() > maxIndexFileSize {
			return nil
		}
		rel, relErr := filepath.Rel(ix.workspace, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		seen[rel] = true
		ix.mu.Lock()
		entry, cached := ix.files[rel]
		fresh := cached && entry.modTime == info.ModTime().UnixNano() && entry.size == info.Size()
		ix.mu.Unlock()
		if fresh {
			return nil
		}
		symbols := extract(path, rel, languageRules[ext])
		ix.mu.Lock()
		ix.files[rel] = &fileEntry{modTime: info.ModTime().UnixNano(), size: info.Size(), symbols: symbols}
		ix.mu.Unlock()
		parsed++
		return nil
	})
	// Drop entries for files that no longer exist.
	ix.mu.Lock()
	for rel := range ix.files {
		if !seen[rel] {
			delete(ix.files, rel)
		}
	}
	ix.mu.Unlock()
	return parsed, walkErr
}

func extract(path, rel string, rules []rule) []Symbol {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var symbols []Symbol
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		for _, r := range rules {
			if m := r.re.FindStringSubmatch(text); m != nil {
				symbols = append(symbols, Symbol{Name: m[1], Kind: r.kind, Path: rel, Line: line})
				break
			}
		}
	}
	return symbols
}

// Query finds symbols whose name contains the query (case-insensitive);
// exact matches rank first, then prefix matches, then substrings. kind,
// when non-empty, filters by symbol kind.
func (ix *Index) Query(query, kind string, limit int) []Symbol {
	if limit <= 0 {
		limit = 50
	}
	q := strings.ToLower(query)
	type scored struct {
		s     Symbol
		score int
	}
	var matches []scored
	ix.mu.Lock()
	for _, entry := range ix.files {
		for _, s := range entry.symbols {
			if kind != "" && s.Kind != kind {
				continue
			}
			name := strings.ToLower(s.Name)
			score := -1
			switch {
			case name == q:
				score = 0
			case strings.HasPrefix(name, q):
				score = 1
			case strings.Contains(name, q):
				score = 2
			}
			if score >= 0 {
				matches = append(matches, scored{s, score})
			}
		}
	}
	ix.mu.Unlock()
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		if matches[i].s.Path != matches[j].s.Path {
			return matches[i].s.Path < matches[j].s.Path
		}
		return matches[i].s.Line < matches[j].s.Line
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]Symbol, len(matches))
	for i, m := range matches {
		out[i] = m.s
	}
	return out
}
