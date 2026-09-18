package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/cli"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/manifest"
)

func matchingClaudeOutputHarness(t *testing.T, manifestExists bool) *harness {
	t.Helper()
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	if res := h.run("import", "--from", "github-copilot"); res.code != cli.ExitOK {
		t.Fatalf("import: %s\n%s", res.stdout, res.stderr)
	}
	if res := h.run("apply", "--target", "claude", "--yes"); res.code != cli.ExitOK {
		t.Fatalf("initial apply: %s\n%s", res.stdout, res.stderr)
	}

	manifestPath := filepath.Join(h.root, ".stemma", "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	delete(m.Targets, "claude")
	data, err = manifest.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if !manifestExists {
		if err := os.Remove(manifestPath); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func replaceClaudeDirWithSymlink(t *testing.T, h *harness) string {
	t.Helper()
	inside := filepath.Join(h.root, ".claude")
	outside := filepath.Join(t.TempDir(), "claude")
	if err := os.Rename(inside, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, inside); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return outside
}

func snapshotRegularTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertSymlinkDiagnosticJSON(t *testing.T, res result, wantExit int) {
	t.Helper()
	if res.code != wantExit {
		t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", res.code, wantExit, res.stdout, res.stderr)
	}
	var doc cli.Envelope
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("decode output: %v\n%s", err, res.stdout)
	}
	if doc.ExitCode != wantExit {
		t.Fatalf("reported exit = %d, want %d", doc.ExitCode, wantExit)
	}
	found := false
	for _, d := range doc.Diagnostics {
		if d.Code == diagnostics.SymlinkRejected && strings.HasPrefix(d.Path, ".claude/") &&
			d.Target == "claude" && d.Blocking {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing %s diagnostic: %s", diagnostics.SymlinkRejected, res.stdout)
	}
}

func assertAncestorSymlinkState(t *testing.T, h *harness, outside string,
	wantCanonical, wantOutside map[string]string, wantRootOutput string,
) {
	t.Helper()
	if got := snapshotRegularTree(t, filepath.Join(h.root, ".stemma")); !reflect.DeepEqual(got, wantCanonical) {
		t.Fatalf("canonical state changed:\n got: %#v\nwant: %#v", got, wantCanonical)
	}
	if got := snapshotRegularTree(t, outside); !reflect.DeepEqual(got, wantOutside) {
		t.Fatalf("external state changed:\n got: %#v\nwant: %#v", got, wantOutside)
	}
	if got := h.read("CLAUDE.md"); got != wantRootOutput {
		t.Fatal("regular generated output changed")
	}
	info, err := os.Lstat(filepath.Join(h.root, ".claude"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("ancestor symlink changed: %v, %v", info, err)
	}
}

func TestAncestorSymlinkBlocksPlanCheckAndApply(t *testing.T) {
	for _, manifestExists := range []bool{false, true} {
		name := "manifest absent"
		if manifestExists {
			name = "manifest exists"
		}
		t.Run(name, func(t *testing.T) {
			h := matchingClaudeOutputHarness(t, manifestExists)
			outside := replaceClaudeDirWithSymlink(t, h)
			canonicalBefore := snapshotRegularTree(t, filepath.Join(h.root, ".stemma"))
			outsideBefore := snapshotRegularTree(t, outside)
			rootBefore := h.read("CLAUDE.md")

			commands := [][]string{
				{"plan", "--target", "claude", "--json"},
				{"check", "--target", "claude", "--json"},
				{"apply", "--target", "claude", "--yes", "--json"},
			}
			for _, args := range commands {
				res := h.run(args...)
				assertSymlinkDiagnosticJSON(t, res, cli.ExitDiagnostics)
				switch args[0] {
				case "plan":
					var doc struct {
						Data struct {
							Changes []struct {
								Path string `json:"path"`
								Kind string `json:"kind"`
							} `json:"changes"`
						} `json:"data"`
					}
					if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
						t.Fatal(err)
					}
					foundConflict := false
					for _, change := range doc.Data.Changes {
						if !strings.HasPrefix(change.Path, ".claude/") {
							continue
						}
						if change.Kind == "unchanged" {
							t.Fatalf("ancestor symlink classified as unchanged: %+v", change)
						}
						foundConflict = foundConflict || change.Kind == "conflict"
					}
					if !foundConflict {
						t.Fatalf("plan lacks a conflict beneath the ancestor symlink: %s", res.stdout)
					}
				case "check":
					var doc struct {
						Data struct {
							UpToDate bool `json:"upToDate"`
							Targets  []struct {
								UpToDate bool `json:"upToDate"`
							} `json:"targets"`
						} `json:"data"`
					}
					if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
						t.Fatal(err)
					}
					if doc.Data.UpToDate || len(doc.Data.Targets) != 1 || doc.Data.Targets[0].UpToDate {
						t.Fatalf("check reported symlinked output up to date: %s", res.stdout)
					}
				}
				assertAncestorSymlinkState(t, h, outside, canonicalBefore, outsideBefore, rootBefore)
			}

			manifestPath := filepath.Join(h.root, ".stemma", "manifest.json")
			data, err := os.ReadFile(manifestPath)
			if manifestExists {
				if err != nil {
					t.Fatal(err)
				}
				m, err := manifest.Unmarshal(data)
				if err != nil {
					t.Fatal(err)
				}
				if len(m.Targets["claude"].GeneratedFiles) != 0 {
					t.Fatal("blocked commands recorded ownership")
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("blocked commands created a manifest: %v", err)
			}
		})
	}
}

func TestSavedPlanReplayReportsAncestorSymlink(t *testing.T) {
	h := matchingClaudeOutputHarness(t, true)
	if res := h.run("plan", "--target", "claude", "--output-plan", "plan.json"); res.code != cli.ExitOK {
		t.Fatalf("save plan: %s\n%s", res.stdout, res.stderr)
	}
	outside := replaceClaudeDirWithSymlink(t, h)
	canonicalBefore := snapshotRegularTree(t, filepath.Join(h.root, ".stemma"))
	outsideBefore := snapshotRegularTree(t, outside)
	rootBefore := h.read("CLAUDE.md")
	planBefore := h.read("plan.json")

	res := h.run("apply", "--plan", "plan.json", "--yes", "--json")
	assertSymlinkDiagnosticJSON(t, res, cli.ExitStalePlan)
	if !strings.Contains(res.stdout, "saved plan rejected") {
		t.Fatalf("replay did not retain the stale-plan explanation: %s", res.stdout)
	}
	assertAncestorSymlinkState(t, h, outside, canonicalBefore, outsideBefore, rootBefore)

	human := h.run("apply", "--plan", "plan.json", "--yes")
	if human.code != cli.ExitStalePlan {
		t.Fatalf("human replay exit = %d, want %d\nstdout: %s\nstderr: %s",
			human.code, cli.ExitStalePlan, human.stdout, human.stderr)
	}
	if !strings.Contains(human.stderr, string(diagnostics.SymlinkRejected)) ||
		!strings.Contains(human.stderr, "parent directory") {
		t.Fatalf("human replay lacks readable ancestor-symlink diagnostic: %s", human.stderr)
	}
	assertAncestorSymlinkState(t, h, outside, canonicalBefore, outsideBefore, rootBefore)
	if got := h.read("plan.json"); got != planBefore {
		t.Fatal("rejected replay changed the saved plan")
	}
}
