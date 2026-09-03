package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTransactionWritesAtomically(t *testing.T) {
	ws := newTestWorkspace(t)
	tx := ws.Begin()
	if err := tx.Add(WriteOp{Path: "a/b.md", Content: []byte("hello"), Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Add(WriteOp{Path: "c.md", Content: []byte("world"), Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	for path, want := range map[string]string{"a/b.md": "hello", "c.md": "world"} {
		native, _ := ws.Native(path)
		got, err := os.ReadFile(native)
		if err != nil || string(got) != want {
			t.Errorf("%s = %q (%v)", path, got, err)
		}
	}
	// No temporary files may survive a successful commit.
	entries, _ := os.ReadDir(ws.Root())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tempPrefix) {
			t.Errorf("temporary file survived commit: %s", e.Name())
		}
	}
}

func TestTransactionRollsBackOnFailure(t *testing.T) {
	ws := newTestWorkspace(t)
	write(t, ws, "existing.md", "original")
	// A directory where a file is expected makes the second write fail.
	native, _ := ws.Native("blocked.md")
	if err := os.MkdirAll(native, 0o755); err != nil {
		t.Fatal(err)
	}

	tx := ws.Begin()
	if err := tx.Add(WriteOp{Path: "existing.md", Content: []byte("replaced")}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Add(WriteOp{Path: "blocked.md", Content: []byte("nope")}); err != nil {
		t.Fatal(err)
	}
	err := tx.Commit()
	if err == nil {
		t.Fatal("Commit must fail when a destination is a directory")
	}
	existing, _ := ws.Native("existing.md")
	got, rerr := os.ReadFile(existing)
	if rerr != nil {
		t.Fatalf("reading the rolled-back file: %v", rerr)
	}
	if string(got) != "original" {
		t.Fatalf("rollback did not restore the original content: %q", got)
	}
	entries, _ := os.ReadDir(ws.Root())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tempPrefix) {
			t.Errorf("temporary file survived a failed commit: %s", e.Name())
		}
	}
}

func TestTransactionRemovesCreatedFilesOnRollback(t *testing.T) {
	ws := newTestWorkspace(t)
	native, _ := ws.Native("blocked.md")
	if err := os.MkdirAll(native, 0o755); err != nil {
		t.Fatal(err)
	}
	tx := ws.Begin()
	if err := tx.Add(WriteOp{Path: "created.md", Content: []byte("new")}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Add(WriteOp{Path: "blocked.md", Content: []byte("nope")}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err == nil {
		t.Fatal("expected the commit to fail")
	}
	created, _ := ws.Native("created.md")
	if _, err := os.Stat(created); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a file created during a failed transaction was not removed (%v)", err)
	}
}

func TestTransactionRejectsUnsafePaths(t *testing.T) {
	ws := newTestWorkspace(t)
	tx := ws.Begin()
	if err := tx.Add(WriteOp{Path: "../escape.md", Content: []byte("x")}); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("err = %v, want ErrPathEscape", err)
	}
	if err := tx.Add(WriteOp{Path: "/abs.md", Content: []byte("x")}); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("err = %v, want ErrPathEscape", err)
	}
}

func TestTransactionPreservesPermissions(t *testing.T) {
	ws := newTestWorkspace(t)
	write(t, ws, "script.md", "old")
	native, _ := ws.Native("script.md")
	if err := os.Chmod(native, 0o600); err != nil {
		t.Fatal(err)
	}
	tx := ws.Begin()
	if err := tx.Add(WriteOp{Path: "script.md", Content: []byte("new"), Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(native)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// Windows has no Unix permission bits: os.Chmod only toggles the
		// read-only attribute, so there is nothing meaningful to assert.
		return
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("permissions = %v, want 0600 (existing permissions must be preserved)", info.Mode().Perm())
	}
}

func TestCommitOrderIsDeterministic(t *testing.T) {
	ws := newTestWorkspace(t)
	tx := ws.Begin()
	for _, p := range []string{"z.md", "a.md", "m.md"} {
		if err := tx.Add(WriteOp{Path: p, Content: []byte(p)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got := tx.WrittenPaths()
	want := []string{"a.md", "m.md", "z.md"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("written = %v, want %v", got, want)
		}
	}
}

func TestRecoveryDirectoryConstant(t *testing.T) {
	if filepath.ToSlash(RecoveryDir) != ".stemma/recovery" {
		t.Errorf("RecoveryDir = %q", RecoveryDir)
	}
}

// TestRecoveryDataIsWrittenWhenRollbackFails covers the recovery writer
// directly. The failure it reports (a rollback that cannot restore a file) is
// hard to provoke portably, so the writer is exercised on its own: it must
// produce readable instructions and restrictive permissions.
func TestRecoveryDataIsWrittenWhenRollbackFails(t *testing.T) {
	ws := newTestWorkspace(t)
	write(t, ws, "kept.md", "original content")

	tx := ws.Begin()
	if err := tx.snapshot("kept.md"); err != nil {
		t.Fatal(err)
	}
	if err := tx.snapshot("created.md"); err != nil {
		t.Fatal(err)
	}
	path, report, err := tx.writeRecovery([]string{"kept.md", "created.md"}, errors.New("disk on fire"))
	if err != nil {
		t.Fatalf("writeRecovery: %v", err)
	}
	if path != RecoveryDir+"/RECOVERY.txt" {
		t.Errorf("recovery path = %q", path)
	}
	for _, want := range []string{"disk on fire", "kept.md", "created.md", "delete it to restore"} {
		if !strings.Contains(report, want) {
			t.Errorf("recovery report is missing %q:\n%s", want, report)
		}
	}
	saved, err := os.ReadFile(filepath.Join(ws.Root(), ".stemma", "recovery", "kept.md"))
	if err != nil {
		t.Fatalf("the original content was not saved: %v", err)
	}
	if string(saved) != "original content" {
		t.Errorf("saved content = %q", saved)
	}
	info, err := os.Stat(filepath.Join(ws.Root(), ".stemma", "recovery", "kept.md"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return // no Unix permission bits to assert
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("recovery data permissions = %v, want 0600", info.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Join(ws.Root(), ".stemma", "recovery"))
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("recovery directory permissions = %v, want 0700", dir.Mode().Perm())
	}
}
