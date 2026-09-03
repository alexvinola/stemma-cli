package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RecoveryDir is where rollback data is written when a rollback itself fails.
const RecoveryDir = ".stemma/recovery"

// tempPrefix marks Stemma's temporary files. They are always created next to
// their destination so that the final rename stays on one filesystem.
const tempPrefix = ".stemma-tmp-"

// WriteOp is a single pending file write.
type WriteOp struct {
	// Path is the repository-relative destination.
	Path string
	// Content is the exact bytes to write.
	Content []byte
	// Mode is the permission bits for newly created files.
	Mode fs.FileMode
}

// RollbackError reports that a transaction failed and could not be fully
// undone. It carries recovery instructions for the user.
type RollbackError struct {
	Cause          error
	RecoveryPath   string
	Unrestored     []string
	RecoveryReport string
}

// Error implements error.
func (e *RollbackError) Error() string {
	return fmt.Sprintf("write failed and rollback was incomplete: %v", e.Cause)
}

// Unwrap implements errors.Unwrap.
func (e *RollbackError) Unwrap() error { return e.Cause }

// Transaction applies a set of writes atomically enough for a working tree:
// every file is written to a temporary sibling and renamed into place, and any
// file already replaced is restored if a later write fails.
type Transaction struct {
	ws       *Workspace
	ops      []WriteOp
	backups  map[string][]byte // path -> previous content (nil means "did not exist")
	existed  map[string]bool
	modes    map[string]fs.FileMode
	written  []string
	tempPath map[string]string
}

// Begin starts a transaction.
func (w *Workspace) Begin() *Transaction {
	return &Transaction{
		ws:       w,
		backups:  map[string][]byte{},
		existed:  map[string]bool{},
		modes:    map[string]fs.FileMode{},
		tempPath: map[string]string{},
	}
}

// Add queues a write. Paths are validated immediately so that an unsafe path
// can never reach the filesystem.
func (t *Transaction) Add(op WriteOp) error {
	clean, err := NormalizeRel(op.Path)
	if err != nil {
		return err
	}
	if err := t.ws.CheckNoSymlink(clean); err != nil {
		return err
	}
	if op.Mode == 0 {
		op.Mode = 0o644
	}
	op.Path = clean
	t.ops = append(t.ops, op)
	return nil
}

// Commit performs every queued write, rolling back on the first failure.
//
// The commit order is sorted by path so that behaviour is deterministic and
// reproducible in tests.
func (t *Transaction) Commit() error {
	ops := append([]WriteOp{}, t.ops...)
	sort.Slice(ops, func(i, j int) bool { return ops[i].Path < ops[j].Path })

	for _, op := range ops {
		if err := t.snapshot(op.Path); err != nil {
			return t.rollback(err)
		}
		if err := t.writeOne(op); err != nil {
			return t.rollback(err)
		}
		t.written = append(t.written, op.Path)
	}
	return nil
}

// snapshot records the previous state of a destination file.
func (t *Transaction) snapshot(rel string) error {
	if _, ok := t.existed[rel]; ok {
		return nil
	}
	native, err := t.ws.Native(rel)
	if err != nil {
		return err
	}
	info, err := os.Lstat(native)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			t.existed[rel] = false
			return nil
		}
		return fmt.Errorf("inspect %q: %w", rel, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%w: %q", ErrSymlink, rel)
	}
	data, err := os.ReadFile(native)
	if err != nil {
		return fmt.Errorf("back up %q: %w", rel, err)
	}
	t.existed[rel] = true
	t.backups[rel] = data
	t.modes[rel] = info.Mode().Perm()
	return nil
}

func (t *Transaction) writeOne(op WriteOp) error {
	native, err := t.ws.Native(op.Path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(native)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory for %q: %w", op.Path, err)
	}
	mode := op.Mode
	if prev, ok := t.modes[op.Path]; ok {
		mode = prev // preserve existing permissions
	}
	tmp, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return fmt.Errorf("create temporary file for %q: %w", op.Path, err)
	}
	tmpName := tmp.Name()
	t.tempPath[op.Path] = tmpName
	cleanupTemp := func() {
		_ = os.Remove(tmpName)
		delete(t.tempPath, op.Path)
	}
	if _, err := tmp.Write(op.Content); err != nil {
		tmp.Close()
		cleanupTemp()
		return fmt.Errorf("write temporary file for %q: %w", op.Path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanupTemp()
		return fmt.Errorf("sync temporary file for %q: %w", op.Path, err)
	}
	if err := tmp.Close(); err != nil {
		cleanupTemp()
		return fmt.Errorf("close temporary file for %q: %w", op.Path, err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		cleanupTemp()
		return fmt.Errorf("set permissions for %q: %w", op.Path, err)
	}
	if err := os.Rename(tmpName, native); err != nil {
		cleanupTemp()
		return fmt.Errorf("replace %q: %w", op.Path, err)
	}
	delete(t.tempPath, op.Path)
	return nil
}

// rollback restores every file this transaction already replaced.
func (t *Transaction) rollback(cause error) error {
	var unrestored []string
	for _, rel := range t.written {
		native, err := t.ws.Native(rel)
		if err != nil {
			unrestored = append(unrestored, rel)
			continue
		}
		if t.existed[rel] {
			if err := os.WriteFile(native, t.backups[rel], t.modes[rel]); err != nil {
				unrestored = append(unrestored, rel)
			}
			continue
		}
		if err := os.Remove(native); err != nil && !errors.Is(err, fs.ErrNotExist) {
			unrestored = append(unrestored, rel)
		}
	}
	// Remove any stranded temporary files.
	for _, tmp := range t.tempPath {
		_ = os.Remove(tmp)
	}
	if len(unrestored) == 0 {
		return fmt.Errorf("transaction rolled back: %w", cause)
	}
	sort.Strings(unrestored)
	recoveryPath, report, rerr := t.writeRecovery(unrestored, cause)
	if rerr != nil {
		report = fmt.Sprintf("recovery data could not be written: %v", rerr)
	}
	return &RollbackError{
		Cause:          cause,
		RecoveryPath:   recoveryPath,
		Unrestored:     unrestored,
		RecoveryReport: report,
	}
}

// writeRecovery persists the original content of files that could not be
// restored, under .stemma/recovery, with restrictive permissions.
func (t *Transaction) writeRecovery(unrestored []string, cause error) (string, string, error) {
	root := filepath.Join(t.ws.Root(), filepath.FromSlash(RecoveryDir))
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", "", err
	}
	var report strings.Builder
	fmt.Fprintf(&report, "Stemma could not restore %d file(s) after a failed write.\n", len(unrestored))
	fmt.Fprintf(&report, "Cause: %v\n\n", cause)
	for _, rel := range unrestored {
		safeName := strings.ReplaceAll(rel, "/", "__")
		dest := filepath.Join(root, safeName)
		if t.existed[rel] {
			if err := os.WriteFile(dest, t.backups[rel], 0o600); err != nil {
				return RecoveryDir, report.String(), err
			}
			fmt.Fprintf(&report, "  %s\n    original content saved to %s/%s\n", rel, RecoveryDir, safeName)
			continue
		}
		fmt.Fprintf(&report, "  %s\n    did not exist before this run; delete it to restore the previous state\n", rel)
	}
	reportPath := filepath.Join(root, "RECOVERY.txt")
	if err := os.WriteFile(reportPath, []byte(report.String()), 0o600); err != nil {
		return RecoveryDir, report.String(), err
	}
	return RecoveryDir + "/RECOVERY.txt", report.String(), nil
}

// WrittenPaths returns the paths committed so far, sorted.
func (t *Transaction) WrittenPaths() []string {
	out := append([]string{}, t.written...)
	sort.Strings(out)
	return out
}
