package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/alexvinola/stemma-cli/internal/provenance"
)

// Limits bound how much of a repository Stemma is willing to inspect.
type Limits struct {
	// MaxDepth is the deepest directory level below the root that is scanned.
	MaxDepth int
	// MaxFiles is the maximum number of candidate files visited.
	MaxFiles int
	// MaxFileBytes is the maximum size of a single configuration file.
	MaxFileBytes int64
	// MaxTotalBytes is the maximum total size of all configuration read.
	MaxTotalBytes int64
}

// DefaultLimits returns conservative limits suitable for real repositories.
func DefaultLimits() Limits {
	return Limits{
		MaxDepth:      12,
		MaxFiles:      20000,
		MaxFileBytes:  2 << 20,  // 2 MiB per configuration file
		MaxTotalBytes: 64 << 20, // 64 MiB in total
	}
}

// SkippedDirectories are never descended into. Stemma looks for configuration
// directories, never for source code.
var SkippedDirectories = []string{
	".bzr", ".git", ".hg", ".idea", ".next", ".nuxt", ".svn", ".terraform",
	".tox", ".venv", ".vscode", "__pycache__", "bin", "bower_components",
	"build", "coverage", "dist", "node_modules", "obj", "out", "target",
	"tmp", "vendor", "venv",
}

// IsSkippedDir reports whether a directory name is always skipped.
func IsSkippedDir(name string) bool {
	for _, d := range SkippedDirectories {
		if name == d {
			return true
		}
	}
	return false
}

// ErrLimitExceeded reports that a bounded resource limit was hit.
var ErrLimitExceeded = errors.New("resource limit exceeded")

// ErrNotFound reports a missing file.
var ErrNotFound = fs.ErrNotExist

// Workspace is a validated repository root plus its resource limits.
type Workspace struct {
	root   string // absolute, symlink-resolved, native separators
	limits Limits
	read   int64
}

// Open validates and resolves a workspace root.
func Open(root string, limits Limits) (*Workspace, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root %q: %w", root, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root %q: %w", root, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("stat workspace root %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root %q is not a directory", root)
	}
	return &Workspace{root: resolved, limits: limits}, nil
}

// Root returns the absolute, symlink-resolved workspace root.
func (w *Workspace) Root() string { return w.root }

// Limits returns the configured limits.
func (w *Workspace) Limits() Limits { return w.limits }

// Native converts a validated repository-relative path into a native path
// confined to the workspace root.
func (w *Workspace) Native(rel string) (string, error) {
	clean, err := NormalizeRel(rel)
	if err != nil {
		return "", err
	}
	native := filepath.Join(w.root, filepath.FromSlash(clean))
	// Defence in depth: the join must remain under the root.
	if !isUnder(w.root, native) {
		return "", fmt.Errorf("%w: %q resolves outside the workspace", ErrPathEscape, rel)
	}
	return native, nil
}

func isUnder(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// RelFromNative converts a native absolute path under the root into a
// repository-relative slash path.
func (w *Workspace) RelFromNative(native string) (string, error) {
	rel, err := filepath.Rel(w.root, native)
	if err != nil {
		return "", fmt.Errorf("%w: %q", ErrPathEscape, native)
	}
	return NormalizeRel(filepath.ToSlash(rel))
}

// CheckNoSymlink verifies that neither the path nor any of its ancestors
// inside the workspace is a symbolic link.
func (w *Workspace) CheckNoSymlink(rel string) error {
	clean, err := NormalizeRel(rel)
	if err != nil {
		return err
	}
	segments := strings.Split(clean, "/")
	current := w.root
	for _, seg := range segments {
		current = filepath.Join(current, seg)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // the remaining components do not exist yet
			}
			return fmt.Errorf("inspect %q: %w", rel, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: %q is a symbolic link", ErrSymlink, rel)
		}
	}
	return nil
}

// ErrSymlink reports that a symbolic link was encountered where Stemma
// requires a regular file or directory.
var ErrSymlink = errors.New("symbolic link rejected")

// Exists reports whether a regular file exists at rel.
func (w *Workspace) Exists(rel string) (bool, error) {
	native, err := w.Native(rel)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(native)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return false, fmt.Errorf("%w: %q", ErrSymlink, rel)
	}
	return info.Mode().IsRegular(), nil
}

// File is a configuration file read from the workspace.
type File struct {
	// Path is the repository-relative slash path.
	Path string
	// Data is the raw file content.
	Data []byte
	// Hash is the "sha256:<hex>" digest of Data.
	Hash string
	// Mode is the file mode as stored on disk.
	Mode fs.FileMode
}

// ReadFile reads a registered configuration file, enforcing limits and
// rejecting symlinks.
func (w *Workspace) ReadFile(ctx context.Context, rel string) (File, error) {
	if err := ctx.Err(); err != nil {
		return File{}, err
	}
	if err := w.CheckNoSymlink(rel); err != nil {
		return File{}, err
	}
	native, err := w.Native(rel)
	if err != nil {
		return File{}, err
	}
	info, err := os.Lstat(native)
	if err != nil {
		return File{}, fmt.Errorf("read %q: %w", rel, err)
	}
	if !info.Mode().IsRegular() {
		return File{}, fmt.Errorf("read %q: not a regular file", rel)
	}
	if info.Size() > w.limits.MaxFileBytes {
		return File{}, fmt.Errorf("%w: %q is %d bytes (limit %d)",
			ErrLimitExceeded, rel, info.Size(), w.limits.MaxFileBytes)
	}
	if w.read+info.Size() > w.limits.MaxTotalBytes {
		return File{}, fmt.Errorf("%w: total configuration size limit %d bytes reached at %q",
			ErrLimitExceeded, w.limits.MaxTotalBytes, rel)
	}
	f, err := os.Open(native)
	if err != nil {
		return File{}, fmt.Errorf("read %q: %w", rel, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, w.limits.MaxFileBytes+1))
	if err != nil {
		return File{}, fmt.Errorf("read %q: %w", rel, err)
	}
	if int64(len(data)) > w.limits.MaxFileBytes {
		return File{}, fmt.Errorf("%w: %q exceeds %d bytes", ErrLimitExceeded, rel, w.limits.MaxFileBytes)
	}
	w.read += int64(len(data))
	clean, _ := NormalizeRel(rel)
	return File{Path: clean, Data: data, Hash: provenance.HashBytes(data), Mode: info.Mode().Perm()}, nil
}

// HashFile streams the digest of an existing file. It returns ok=false when
// the file does not exist.
func (w *Workspace) HashFile(rel string) (hash string, ok bool, err error) {
	native, err := w.Native(rel)
	if err != nil {
		return "", false, err
	}
	info, err := os.Lstat(native)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return "", false, fmt.Errorf("%w: %q", ErrSymlink, rel)
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("hash %q: not a regular file", rel)
	}
	f, err := os.Open(native)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	h := provenance.NewHasher()
	if _, err := io.Copy(h, f); err != nil {
		return "", false, err
	}
	return h.Sum(), true, nil
}

// WalkResult reports what a directory walk observed.
type WalkResult struct {
	// Files is the sorted list of repository-relative candidate files.
	Files []string
	// SkippedDirs lists directories that were not descended into.
	SkippedDirs []string
	// LimitsReached lists the limits that stopped the walk.
	LimitsReached []string
}

// Walk visits every non-skipped directory under sub (relative to the root, ""
// for the whole workspace) and reports candidate files.
//
// Symlinked directories are never followed and symlinked files are never
// reported. Results are sorted, so iteration order never depends on the
// filesystem.
func (w *Workspace) Walk(ctx context.Context, sub string) (WalkResult, error) {
	var res WalkResult
	start := w.root
	if sub != "" {
		native, err := w.Native(sub)
		if err != nil {
			return res, err
		}
		start = native
	}
	seenSkipped := map[string]struct{}{}
	limitHit := map[string]struct{}{}

	err := filepath.WalkDir(start, func(native string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil // unreadable entries are reported by the caller, not fatal
		}
		rel, relErr := w.RelFromNative(native)
		if relErr != nil {
			if native == w.root {
				return nil
			}
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		depth := strings.Count(rel, "/") + 1
		if d.IsDir() {
			if IsSkippedDir(d.Name()) {
				if _, ok := seenSkipped[rel]; !ok {
					seenSkipped[rel] = struct{}{}
					res.SkippedDirs = append(res.SkippedDirs, rel)
				}
				return fs.SkipDir
			}
			if depth > w.limits.MaxDepth {
				limitHit["max-depth"] = struct{}{}
				if _, ok := seenSkipped[rel]; !ok {
					seenSkipped[rel] = struct{}{}
					res.SkippedDirs = append(res.SkippedDirs, rel)
				}
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // never follow or report symlinks
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if len(res.Files) >= w.limits.MaxFiles {
			limitHit["max-files"] = struct{}{}
			return fs.SkipAll
		}
		res.Files = append(res.Files, rel)
		return nil
	})
	if err != nil {
		return res, err
	}
	sort.Strings(res.Files)
	sort.Strings(res.SkippedDirs)
	for k := range limitHit {
		res.LimitsReached = append(res.LimitsReached, k)
	}
	sort.Strings(res.LimitsReached)
	if res.Files == nil {
		res.Files = []string{}
	}
	if res.SkippedDirs == nil {
		res.SkippedDirs = []string{}
	}
	if res.LimitsReached == nil {
		res.LimitsReached = []string{}
	}
	return res, nil
}

// ValidUTF8 reports whether data is well-formed UTF-8.
func ValidUTF8(data []byte) bool { return utf8.Valid(data) }
