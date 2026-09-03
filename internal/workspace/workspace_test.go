package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func newTestWorkspace(t *testing.T) *Workspace {
	t.Helper()
	root := t.TempDir()
	ws, err := Open(root, DefaultLimits())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return ws
}

func write(t *testing.T, ws *Workspace, rel, content string) {
	t.Helper()
	native, err := ws.Native(rel)
	if err != nil {
		t.Fatalf("Native(%q): %v", rel, err)
	}
	if err := os.MkdirAll(filepath.Dir(native), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(native, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadFileAndHash(t *testing.T) {
	ws := newTestWorkspace(t)
	write(t, ws, "CLAUDE.md", "hello\n")
	f, err := ws.ReadFile(context.Background(), "CLAUDE.md")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(f.Data) != "hello\n" {
		t.Errorf("content = %q", f.Data)
	}
	hash, ok, err := ws.HashFile("CLAUDE.md")
	if err != nil || !ok {
		t.Fatalf("HashFile: %v %v", ok, err)
	}
	if hash != f.Hash {
		t.Errorf("streamed hash %q != read hash %q", hash, f.Hash)
	}
	if _, ok, _ := ws.HashFile("missing.md"); ok {
		t.Error("HashFile reported a missing file as present")
	}
}

func TestReadFileRejectsEscape(t *testing.T) {
	ws := newTestWorkspace(t)
	if _, err := ws.ReadFile(context.Background(), "../outside.md"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("err = %v, want ErrPathEscape", err)
	}
}

func TestSymlinkRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	ws := newTestWorkspace(t)
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ws.Root(), "CLAUDE.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ws.ReadFile(context.Background(), "CLAUDE.md"); !errors.Is(err, ErrSymlink) {
		t.Fatalf("ReadFile err = %v, want ErrSymlink", err)
	}
	if err := ws.CheckNoSymlink("CLAUDE.md"); !errors.Is(err, ErrSymlink) {
		t.Fatalf("CheckNoSymlink err = %v, want ErrSymlink", err)
	}
}

func TestSymlinkedDirectoryRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	ws := newTestWorkspace(t)
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "x.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(ws.Root(), "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := ws.CheckNoSymlink("linked/x.md"); !errors.Is(err, ErrSymlink) {
		t.Fatalf("CheckNoSymlink err = %v, want ErrSymlink", err)
	}
}

func TestWalkSkipsHeavyDirectoriesAndSymlinks(t *testing.T) {
	ws := newTestWorkspace(t)
	write(t, ws, "AGENTS.md", "root")
	write(t, ws, "node_modules/pkg/AGENTS.md", "junk")
	write(t, ws, ".git/config", "junk")
	write(t, ws, "src/api/AGENTS.md", "scoped")

	res, err := ws.Walk(context.Background(), "")
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	for _, f := range res.Files {
		if f == "node_modules/pkg/AGENTS.md" || f == ".git/config" {
			t.Errorf("walk visited skipped directory: %s", f)
		}
	}
	found := map[string]bool{}
	for _, f := range res.Files {
		found[f] = true
	}
	if !found["AGENTS.md"] || !found["src/api/AGENTS.md"] {
		t.Errorf("walk missed files: %v", res.Files)
	}
	if len(res.SkippedDirs) == 0 {
		t.Error("expected skipped directories to be reported")
	}
}

func TestWalkIsSorted(t *testing.T) {
	ws := newTestWorkspace(t)
	for _, p := range []string{"z.md", "a.md", "m/n.md", "b/c.md"} {
		write(t, ws, p, "x")
	}
	res, err := ws.Walk(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(res.Files); i++ {
		if res.Files[i-1] > res.Files[i] {
			t.Fatalf("walk results are not sorted: %v", res.Files)
		}
	}
}

func TestFileSizeLimit(t *testing.T) {
	root := t.TempDir()
	ws, err := Open(root, Limits{MaxDepth: 5, MaxFiles: 10, MaxFileBytes: 8, MaxTotalBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	write(t, ws, "big.md", "0123456789")
	if _, err := ws.ReadFile(context.Background(), "big.md"); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("err = %v, want ErrLimitExceeded", err)
	}
}

func TestTotalSizeLimit(t *testing.T) {
	root := t.TempDir()
	ws, err := Open(root, Limits{MaxDepth: 5, MaxFiles: 10, MaxFileBytes: 100, MaxTotalBytes: 12})
	if err != nil {
		t.Fatal(err)
	}
	write(t, ws, "a.md", "0123456789")
	write(t, ws, "b.md", "0123456789")
	if _, err := ws.ReadFile(context.Background(), "a.md"); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if _, err := ws.ReadFile(context.Background(), "b.md"); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("second read err = %v, want ErrLimitExceeded", err)
	}
}

func TestWalkDepthLimit(t *testing.T) {
	root := t.TempDir()
	ws, err := Open(root, Limits{MaxDepth: 2, MaxFiles: 100, MaxFileBytes: 100, MaxTotalBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	write(t, ws, "a/b/c/deep.md", "x")
	write(t, ws, "top.md", "x")
	res, err := ws.Walk(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Files {
		if f == "a/b/c/deep.md" {
			t.Error("walk exceeded the depth limit")
		}
	}
	if len(res.LimitsReached) == 0 {
		t.Error("expected the depth limit to be reported")
	}
}

func TestContextCancellation(t *testing.T) {
	ws := newTestWorkspace(t)
	write(t, ws, "a.md", "x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ws.ReadFile(ctx, "a.md"); err == nil {
		t.Fatal("expected a cancellation error")
	}
}
