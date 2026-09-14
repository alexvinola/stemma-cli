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

// One scoped entity produces one independently removable output file.
func retiredOutputHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.write(".github/instructions/api.instructions.md", "---\napplyTo: src/api/**\n---\n\n# API\n\nValidate requests.\n")
	for _, args := range [][]string{
		{"import", "--from", "github-copilot", "--targets", "claude"},
		{"apply", "--target", "claude", "--yes"},
	} {
		if res := h.run(args...); res.code != cli.ExitOK {
			t.Fatalf("%v: exit=%d\n%s\n%s", args, res.code, res.stdout, res.stderr)
		}
	}
	if err := os.Remove(filepath.Join(h.root, ".stemma/context/api.md")); err != nil {
		t.Fatal(err)
	}
	return h
}

const retiredAPIPath = ".claude/rules/context-api.md"

func TestRetiredOutputInspectionErrorsBlockCheckAndApply(t *testing.T) {
	for _, tc := range []struct {
		name string
		code diagnostics.Code
	}{
		{"unreadable", diagnostics.FileUnreadable},
		{"symlink", diagnostics.SymlinkRejected},
		{"directory", diagnostics.FileUnreadable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := retiredOutputHarness(t)
			path := filepath.Join(h.root, filepath.FromSlash(retiredAPIPath))
			original := h.read(retiredAPIPath)
			before := h.snapshot()
			switch tc.name {
			case "unreadable":
				if err := os.Chmod(path, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
				if _, err := os.ReadFile(path); err == nil {
					t.Skip("this environment can read files without permission bits")
				}
			case "symlink", "directory":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if tc.name == "symlink" {
					outside := filepath.Join(t.TempDir(), "retired.md")
					if err := os.WriteFile(outside, []byte(original), 0o644); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, path); err != nil {
						t.Skipf("symlink unavailable: %v", err)
					}
					t.Cleanup(func() {
						data, err := os.ReadFile(outside)
						if err != nil || string(data) != original {
							t.Errorf("symlink target changed: %q, %v", data, err)
						}
					})
				} else if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}

			for _, command := range []string{"check", "apply"} {
				args := []string{command, "--target", "claude", "--json"}
				if command == "apply" {
					args = append(args, "--yes")
				}
				res := h.run(args...)
				var report struct {
					ExitCode    int                      `json:"exitCode"`
					Diagnostics []diagnostics.Diagnostic `json:"diagnostics"`
					Data        struct {
						UpToDate bool `json:"upToDate"`
						Targets  []struct {
							UpToDate  bool     `json:"upToDate"`
							Conflicts []string `json:"conflicts"`
						} `json:"targets"`
					} `json:"data"`
				}
				if err := json.Unmarshal([]byte(res.stdout), &report); err != nil {
					t.Fatal(err)
				}
				if res.code != cli.ExitDiagnostics || report.ExitCode != cli.ExitDiagnostics {
					t.Fatalf("%s did not block: %s\n%s", command, res.stdout, res.stderr)
				}
				found := false
				for _, d := range report.Diagnostics {
					if d.Code == tc.code && d.Path == retiredAPIPath && d.Target == "claude" &&
						d.Severity == diagnostics.SeverityError && d.Blocking {
						found = true
					}
				}
				if !found {
					t.Fatalf("%s lacks blocking inspection diagnostic: %s", command, res.stdout)
				}
				if command == "check" && (report.Data.UpToDate || len(report.Data.Targets) != 1 ||
					report.Data.Targets[0].UpToDate || !reflect.DeepEqual(report.Data.Targets[0].Conflicts, []string{retiredAPIPath})) {
					t.Fatalf("inconsistent check status: %s", res.stdout)
				}
				if got := h.read(".stemma/manifest.json"); got != before[".stemma/manifest.json"] {
					t.Fatalf("%s changed ownership after an inspection error", command)
				}
			}
			// Restore only the test's obstruction, then verify commands wrote nothing.
			if tc.name == "unreadable" {
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				h.write(retiredAPIPath, original)
			}
			assertSameTree(t, before, h.snapshot(), "blocked retired output")
		})
	}
}

func TestEmptyTargetReconcilesRetiredOutputAfterManualCleanup(t *testing.T) {
	for _, saved := range []bool{false, true} {
		name := "direct"
		if saved {
			name = "saved-plan"
		}
		t.Run(name, func(t *testing.T) {
			h := retiredOutputHarness(t)
			apply := func() result {
				t.Helper()
				if saved {
					if res := h.run("plan", "--target", "claude", "--output-plan", "plan.json"); res.code != cli.ExitOK {
						t.Fatalf("save plan: %s\n%s", res.stdout, res.stderr)
					}
					return h.run("apply", "--plan", "plan.json")
				}
				return h.run("apply", "--target", "claude")
			}
			loadManifest := func() manifest.Manifest {
				t.Helper()
				m, err := manifest.Unmarshal([]byte(h.read(".stemma/manifest.json")))
				if err != nil {
					t.Fatal(err)
				}
				return m
			}
			before := loadManifest()
			original := h.read(retiredAPIPath)
			for i := 0; i < 2; i++ {
				res := apply()
				if res.code != cli.ExitOK || strings.Contains(res.stdout, "up to date") ||
					!strings.Contains(res.stdout, string(diagnostics.DeleteProposed)) {
					t.Fatalf("pending-only apply: %s\n%s", res.stdout, res.stderr)
				}
				if !reflect.DeepEqual(loadManifest().Targets["claude"].GeneratedFiles, before.Targets["claude"].GeneratedFiles) ||
					h.read(retiredAPIPath) != original {
					t.Fatal("apply forgot or changed a pending deletion")
				}
				if res := h.run("check", "--target", "claude"); res.code != cli.ExitDiagnostics {
					t.Fatalf("check passed before cleanup: %s", res.stdout)
				}
			}
			if err := os.Remove(filepath.Join(h.root, filepath.FromSlash(retiredAPIPath))); err != nil {
				t.Fatal(err)
			}
			if res := apply(); res.code != cli.ExitOK {
				t.Fatalf("cleanup apply: %s\n%s", res.stdout, res.stderr)
			}
			if got := loadManifest().Targets["claude"].GeneratedFiles; len(got) != 0 {
				t.Fatalf("absent retired files remain tracked: %+v", got)
			}
			if !reflect.DeepEqual(loadManifest().Targets["github-copilot"], before.Targets["github-copilot"]) {
				t.Fatal("cleanup changed another target's ownership")
			}
			h.write(retiredAPIPath, "New user-owned instructions.\n")
			res := h.run("check", "--target", "claude", "--json")
			if res.code != cli.ExitOK || strings.Contains(res.stdout, string(diagnostics.DeleteProposed)) {
				t.Fatalf("new user file mistaken for retired output: %s", res.stdout)
			}
			if res := apply(); res.code != cli.ExitOK || h.read(retiredAPIPath) != "New user-owned instructions.\n" {
				t.Fatalf("apply changed new user file: %s\n%s", res.stdout, res.stderr)
			}
		})
	}
}
