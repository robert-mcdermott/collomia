package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/robert-mcdermott/collomia/internal/safefile"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// Directory scopes describe project inputs. Dependency, cache, and generated
// output directories are excluded by name, not by a model-supplied pattern.
// Explicitly scope an excluded output separately when it is a deliverable.
func projectExcluded(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", ".collomia", ".collomia-tmp", "node_modules", ".venv", "venv", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".uv-cache", ".npm-cache", ".npm", ".cache", "dist", "build", "target", "coverage", ".next", ".nuxt":
		return true
	}
	return false
}

// snapshotProject uses a rooted filesystem; nested symlinks are fingerprinted
// but never followed or counted as coverage. New/deleted files and directory
// membership participate in the digest. Limits bound native hashing work.
func snapshotProject(ctx context.Context, path, target string, identity safefile.RootIdentity, authorize func([]string) error) (string, map[string]bool, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != target {
		return "", nil, fmt.Errorf("project scope changed its canonical path")
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	if !identity.MatchesOpenRoot(root) {
		return "", nil, fmt.Errorf("project scope directory was replaced or became unavailable")
	}
	hash := sha256.New()
	members := map[string]bool{}
	var size int64
	entries := 0
	var files []string
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name != "." && projectExcluded(entry.Name()) && entry.IsDir() {
			return fs.SkipDir
		}
		entries++
		if entries > 10000 {
			return fmt.Errorf("project scope exceeds 10000 entries; select narrower source directories")
		}
		fmt.Fprintf(hash, "%q %d\n", name, entry.Type())
		if entry.Type()&os.ModeSymlink != 0 {
			link, err := root.Readlink(name)
			if err != nil {
				return err
			}
			fmt.Fprintf(hash, "%q\n", link)
			return nil
		}
		if entry.IsDir() {
			members[filepath.Join(target, filepath.FromSlash(name))] = true
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("project scope contains a special file %q", name)
		}
		files = append(files, name)
		members[filepath.Join(target, filepath.FromSlash(name))] = true
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	paths := make([]string, 0, len(files))
	for _, name := range files {
		paths = append(paths, filepath.Join(target, filepath.FromSlash(name)))
	}
	if err := authorize(paths); err != nil {
		return "", nil, err
	}
	readFile := func(name string) error {
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("project input %q is not a regular file", name)
		}
		if info.Size() > 64<<20 || size+info.Size() > 256<<20 {
			return fmt.Errorf("project scope exceeds 64 MiB per file or 256 MiB total; select narrower source directories")
		}
		fileHash := sha256.New()
		n, err := io.Copy(fileHash, io.LimitReader(file, min(int64(64<<20), int64(256<<20)-size)+1))
		if err != nil {
			return err
		}
		size += n
		if n > 64<<20 || size > 256<<20 {
			return fmt.Errorf("project scope grew beyond its byte limit")
		}
		fmt.Fprintf(hash, "%d %x\n", info.Mode().Perm(), fileHash.Sum(nil))
		members[filepath.Join(target, filepath.FromSlash(name))] = true
		return nil
	}
	for _, name := range files {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		fmt.Fprintf(hash, "%q ", name)
		if err := readFile(name); err != nil {
			return "", nil, err
		}
	}
	if err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(hash.Sum(nil)), members, nil
}

func recheckReceipt(ctx context.Context, receipt artifactReceipt) (string, error) {
	if receipt.tree {
		digest, _, err := snapshotProject(ctx, receipt.path, receipt.target, receipt.root, func(paths []string) error {
			for _, path := range paths {
				if !receipt.members[path] {
					return fmt.Errorf("project inputs changed since verification; rerun the project check")
				}
			}
			return nil
		})
		return digest, err
	}
	return tools.RecheckArtifactDigest(ctx, receipt.path, receipt.target, receipt.root)
}

func (r artifactReceipt) covers(path string) bool {
	return path == r.target || r.tree && r.members[path]
}

func (r artifactReceipt) description() string {
	if !r.tree {
		return r.path
	}
	return fmt.Sprintf("%s (project inputs; %d files/directories; dependency, cache and build outputs excluded; nested symlinks not followed)", r.path, len(r.members))
}
