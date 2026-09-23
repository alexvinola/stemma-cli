package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestHashFileRejectsSymlinksInEveryPathComponent(t *testing.T) {
	ws := newTestWorkspace(t)
	wantHash := "sha256:5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"

	t.Run("regular control", func(t *testing.T) {
		write(t, ws, "regular/file.md", "hello\n")
		hash, ok, err := ws.HashFile("regular/file.md")
		if err != nil || !ok || hash != wantHash {
			t.Fatalf("HashFile = (%q, %t, %v), want (%q, true, nil)", hash, ok, err, wantHash)
		}
	})

	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "nested", "matching.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "nested", "different.md"), []byte("different\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws.Root(), "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "nested", "matching.md"), filepath.Join(ws.Root(), "leaf.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	for _, rel := range []string{
		"linked/nested/matching.md",
		"linked/nested/different.md",
		"linked/nested/missing.md",
		"leaf.md",
	} {
		t.Run(rel, func(t *testing.T) {
			hash, ok, err := ws.HashFile(rel)
			if !errors.Is(err, ErrSymlink) {
				t.Fatalf("HashFile = (%q, %t, %v), want ErrSymlink", hash, ok, err)
			}
			if hash != "" || ok {
				t.Fatalf("HashFile returned a result through a symlink: (%q, %t)", hash, ok)
			}
		})
	}
}

func TestHashFileRejectsCaseAliasedAncestorSymlinkWhenSupported(t *testing.T) {
	ws := newTestWorkspace(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "file.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws.Root(), "Linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(ws.Root(), "linked")); err != nil {
		t.Skip("filesystem is case-sensitive")
	}
	if _, _, err := ws.HashFile("linked/file.md"); !errors.Is(err, ErrSymlink) {
		t.Fatalf("HashFile err = %v, want ErrSymlink", err)
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
	ws, err := Open(root, Limits{MaxDepth: 5, MaxFiles: 10, MaxEntries: 100, MaxFileBytes: 8, MaxTotalBytes: 100})
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
	ws, err := Open(root, Limits{MaxDepth: 5, MaxFiles: 10, MaxEntries: 100, MaxFileBytes: 100, MaxTotalBytes: 12})
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
	ws, err := Open(root, Limits{MaxDepth: 2, MaxFiles: 100, MaxEntries: 100, MaxFileBytes: 100, MaxTotalBytes: 1000})
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

func TestFilteredWalkCountsOnlyCandidatesAgainstMaxFiles(t *testing.T) {
	root := t.TempDir()
	ws, err := Open(root, Limits{MaxDepth: 5, MaxFiles: 2, MaxEntries: 100, MaxFileBytes: 100, MaxTotalBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	// Ten non-candidates sort before the two candidates; an unfiltered walk
	// with MaxFiles 2 would stop long before reaching them.
	for i := 0; i < 10; i++ {
		write(t, ws, "a/"+string(rune('a'+i))+".go", "x")
	}
	write(t, ws, "z/one.md", "x")
	write(t, ws, "z/two.md", "x")
	keep := func(rel string) bool { return strings.HasSuffix(rel, ".md") }

	res, err := ws.WalkFiltered(context.Background(), "", keep)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Complete() || len(res.LimitsReached) != 0 {
		t.Fatalf("limits reached = %v, want none", res.LimitsReached)
	}
	if len(res.Files) != 2 || res.Files[0] != "z/one.md" || res.Files[1] != "z/two.md" {
		t.Fatalf("files = %v", res.Files)
	}
	if res.FilesVisited != 12 {
		t.Errorf("FilesVisited = %d, want 12", res.FilesVisited)
	}
	// Two directories plus twelve files.
	if res.EntriesVisited != 14 {
		t.Errorf("EntriesVisited = %d, want 14", res.EntriesVisited)
	}

	write(t, ws, "z/three.md", "x")
	res, err = ws.WalkFiltered(context.Background(), "", keep)
	if err != nil {
		t.Fatal(err)
	}
	if res.Complete() || len(res.LimitsReached) != 1 || res.LimitsReached[0] != LimitMaxFiles {
		t.Fatalf("limits reached = %v, want [%s]", res.LimitsReached, LimitMaxFiles)
	}
}

func TestWalkEntryLimitIsReported(t *testing.T) {
	root := t.TempDir()
	ws, err := Open(root, Limits{MaxDepth: 5, MaxFiles: 100, MaxEntries: 3, MaxFileBytes: 100, MaxTotalBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a.go", "b.go", "c.go", "d.md"} {
		write(t, ws, p, "x")
	}
	res, err := ws.WalkFiltered(context.Background(), "", func(rel string) bool { return rel == "d.md" })
	if err != nil {
		t.Fatal(err)
	}
	if res.Complete() || len(res.LimitsReached) != 1 || res.LimitsReached[0] != LimitMaxEntries {
		t.Fatalf("limits reached = %v, want [%s]", res.LimitsReached, LimitMaxEntries)
	}
	if len(res.Files) != 0 || res.EntriesVisited != 3 {
		t.Fatalf("files = %v, entries = %d", res.Files, res.EntriesVisited)
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

// makeUnreadable removes every permission from dir for the rest of the test
// and restores it before cleanup. It skips when permissions are not enforced
// (for example when running as root, or on Windows).
func makeUnreadable(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("directory permissions are not enforced for this user")
	}
}

func TestWalkReportsUnreadableDirectories(t *testing.T) {
	ws := newTestWorkspace(t)
	write(t, ws, "CLAUDE.md", "root")
	write(t, ws, "hidden/CLAUDE.md", "hidden")
	write(t, ws, "open/CLAUDE.md", "open")
	native, err := ws.Native("hidden")
	if err != nil {
		t.Fatal(err)
	}
	makeUnreadable(t, native)

	res, err := ws.WalkFiltered(context.Background(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Complete() {
		t.Fatal("a walk that could not read a directory must not be complete")
	}
	if res.UnreadableCount != 1 || len(res.UnreadableDirs) != 1 || res.UnreadableDirs[0] != "hidden" {
		t.Fatalf("unreadable = %d %v, want [hidden]", res.UnreadableCount, res.UnreadableDirs)
	}
	if len(res.LimitsReached) != 0 {
		t.Errorf("limits reached = %v, want none", res.LimitsReached)
	}
	if strings.Join(res.Files, ",") != "CLAUDE.md,open/CLAUDE.md" {
		t.Errorf("files = %v", res.Files)
	}
}
