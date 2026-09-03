package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/cli"
)

// harness runs CLI commands against a temporary repository.
type harness struct {
	t    *testing.T
	root string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return &harness{t: t, root: t.TempDir()}
}

// fromFixture copies a testdata input tree into the harness root.
func (h *harness) fromFixture(name string) {
	h.t.Helper()
	src := filepath.Join("../../testdata", name, "input")
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return err
		}
		dest := filepath.Join(h.root, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
	if err != nil {
		h.t.Fatalf("copy fixture %s: %v", name, err)
	}
}

func (h *harness) write(rel, content string) {
	h.t.Helper()
	dest := filepath.Join(h.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) read(rel string) string {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join(h.root, filepath.FromSlash(rel)))
	if err != nil {
		h.t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func (h *harness) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(h.root, filepath.FromSlash(rel)))
	return err == nil
}

type result struct {
	code   int
	stdout string
	stderr string
}

// run executes a command with a non-TTY stdin, as CI would.
func (h *harness) run(args ...string) result {
	h.t.Helper()
	return h.runWith("", false, args...)
}

func (h *harness) runWith(stdin string, tty bool, args ...string) result {
	h.t.Helper()
	var out, errOut strings.Builder
	env := cli.Env{
		Stdout: &out, Stderr: &errOut, Stdin: strings.NewReader(stdin),
		StdinIsTTY: tty, WorkingDir: h.root,
	}
	code := cli.Run(context.Background(), env, args)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

// snapshot records the content of every file in the repository.
func (h *harness) snapshot() map[string]string {
	h.t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(h.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(h.root, path)
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
		h.t.Fatal(err)
	}
	return out
}

func assertSameTree(t *testing.T, before, after map[string]string, what string) {
	t.Helper()
	for path, content := range before {
		got, ok := after[path]
		if !ok {
			t.Errorf("%s deleted %s", what, path)
			continue
		}
		if got != content {
			t.Errorf("%s modified %s", what, path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("%s created %s", what, path)
		}
	}
}

func TestScanIsReadOnly(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	before := h.snapshot()
	res := h.run("scan")
	if res.code != cli.ExitOK {
		t.Fatalf("exit = %d, stderr = %s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "github-copilot") {
		t.Errorf("stdout did not report the detected format:\n%s", res.stdout)
	}
	assertSameTree(t, before, h.snapshot(), "scan")
}

func TestPlanIsReadOnly(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	h.run("import", "--from", "github-copilot")
	before := h.snapshot()
	res := h.run("plan", "--target", "claude")
	if res.code != cli.ExitOK {
		t.Fatalf("exit = %d, stderr = %s", res.code, res.stderr)
	}
	assertSameTree(t, before, h.snapshot(), "plan")
}

func TestCheckIsReadOnly(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	h.run("import", "--from", "github-copilot")
	before := h.snapshot()
	res := h.run("check", "--target", "claude")
	if res.code != cli.ExitDiagnostics {
		t.Fatalf("check on a stale repository should fail; exit = %d", res.code)
	}
	assertSameTree(t, before, h.snapshot(), "check")
}

func TestApplyNeedsConfirmationWithoutTTY(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	h.run("import", "--from", "github-copilot")
	before := h.snapshot()
	res := h.run("apply", "--target", "claude")
	if res.code != cli.ExitUsage {
		t.Fatalf("exit = %d, want %d (stderr: %s)", res.code, cli.ExitUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "--yes") {
		t.Errorf("stderr should explain how to authorize: %s", res.stderr)
	}
	assertSameTree(t, before, h.snapshot(), "refused apply")
}

func TestApplyWithYesWritesFiles(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	h.run("import", "--from", "github-copilot")
	res := h.run("apply", "--target", "claude", "--yes")
	if res.code != cli.ExitOK {
		t.Fatalf("exit = %d, stderr = %s", res.code, res.stderr)
	}
	if !h.exists("CLAUDE.md") {
		t.Fatal("CLAUDE.md was not written")
	}
	if !h.exists(".stemma/manifest.json") {
		t.Fatal("the manifest was not written")
	}
	// Applying twice must be a no-op.
	second := h.run("plan", "--target", "claude")
	if strings.Contains(second.stdout, "create") || strings.Contains(second.stdout, "update") {
		t.Errorf("apply is not idempotent:\n%s", second.stdout)
	}
}

func TestApplyIsCancelledInteractively(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	h.run("import", "--from", "github-copilot")
	before := h.snapshot()
	res := h.runWith("n\n", true, "apply", "--target", "claude")
	if res.code != cli.ExitOK {
		t.Fatalf("exit = %d", res.code)
	}
	if !strings.Contains(res.stdout, "cancelled") {
		t.Errorf("stdout = %s", res.stdout)
	}
	assertSameTree(t, before, h.snapshot(), "cancelled apply")
}

func TestUnchangedFilesAreNotRewritten(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	h.run("import", "--from", "github-copilot")
	h.run("apply", "--target", "claude", "--yes")

	path := filepath.Join(h.root, "CLAUDE.md")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Make the file read-only: a rewrite would fail, an unchanged file will not
	// be touched at all.
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, info.Mode())

	res := h.run("apply", "--target", "claude", "--yes")
	if res.code != cli.ExitOK {
		t.Fatalf("re-apply exit = %d, stderr = %s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "up to date") && strings.Contains(res.stdout, "wrote      CLAUDE.md") {
		t.Errorf("an unchanged file was rewritten:\n%s", res.stdout)
	}
}

func TestStalePlanIsRejected(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	h.run("import", "--from", "github-copilot")
	if res := h.run("plan", "--target", "claude", "--output-plan", "plan.json"); res.code != cli.ExitOK {
		t.Fatalf("plan exit = %d, stderr = %s", res.code, res.stderr)
	}
	// Someone else writes the destination between plan and apply.
	h.write("CLAUDE.md", "hand-written content\n")

	res := h.run("apply", "--plan", "plan.json", "--yes")
	if res.code != cli.ExitStalePlan {
		t.Fatalf("exit = %d, want %d (stderr: %s)", res.code, cli.ExitStalePlan, res.stderr)
	}
	if h.read("CLAUDE.md") != "hand-written content\n" {
		t.Fatal("a stale apply overwrote the file")
	}
}

func TestUntrackedDestinationIsAConflict(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.write("CLAUDE.md", "hand-written\n")
	h.run("init")
	h.run("import", "--from", "github-copilot")

	res := h.run("plan", "--target", "claude")
	if res.code != cli.ExitDiagnostics {
		t.Fatalf("exit = %d, want %d", res.code, cli.ExitDiagnostics)
	}
	if !strings.Contains(res.stdout, "conflict") {
		t.Errorf("stdout should report a conflict:\n%s", res.stdout)
	}
	apply := h.run("apply", "--target", "claude", "--yes")
	if apply.code != cli.ExitDiagnostics {
		t.Fatalf("apply exit = %d", apply.code)
	}
	if h.read("CLAUDE.md") != "hand-written\n" {
		t.Fatal("a conflicting file was overwritten")
	}
	// With explicit adoption it goes through.
	adopt := h.run("apply", "--target", "claude", "--yes", "--adopt-untracked")
	if adopt.code != cli.ExitOK {
		t.Fatalf("adopting exit = %d, stderr = %s", adopt.code, adopt.stderr)
	}
	if h.read("CLAUDE.md") == "hand-written\n" {
		t.Fatal("--adopt-untracked did not take ownership of the file")
	}
}

func TestModifiedGeneratedFileIsAConflict(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	h.run("import", "--from", "github-copilot")
	h.run("apply", "--target", "claude", "--yes")
	h.write("CLAUDE.md", "edited by a person\n")

	res := h.run("plan", "--target", "claude")
	if res.code != cli.ExitDiagnostics {
		t.Fatalf("exit = %d, want %d", res.code, cli.ExitDiagnostics)
	}
	if !strings.Contains(res.stdout, "conflict") {
		t.Errorf("expected a conflict:\n%s", res.stdout)
	}
	if h.read("CLAUDE.md") != "edited by a person\n" {
		t.Fatal("user edits were discarded")
	}
}

func TestDeleteProposalsAreNeverExecuted(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	h.run("import", "--from", "github-copilot")
	h.run("apply", "--target", "claude", "--yes")

	// Remove a rule from the canonical project so its file is no longer produced.
	project := h.read(".stemma/project.json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(project), &doc); err != nil {
		t.Fatal(err)
	}
	docs, _ := doc["contextDocuments"].([]any)
	var kept []any
	for _, d := range docs {
		m := d.(map[string]any)
		if m["id"] == "context.api-layer-conventions" {
			continue
		}
		kept = append(kept, d)
	}
	doc["contextDocuments"] = kept
	updated, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	h.write(".stemma/project.json", string(updated)+"\n")

	res := h.run("plan", "--target", "claude")
	if !strings.Contains(res.stdout, "delete proposed") {
		t.Errorf("expected a delete proposal:\n%s", res.stdout)
	}
	h.run("apply", "--target", "claude", "--yes")
	if !h.exists(".claude/rules/api-layer-conventions.md") {
		t.Fatal("apply deleted a file; deletions must never be executed")
	}
}

func TestJSONOutputIsPureJSON(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	for _, args := range [][]string{
		{"scan", "--json"},
		{"init", "--json"},
		{"import", "--from", "github-copilot", "--json"},
		{"validate", "--json"},
		{"plan", "--target", "claude", "--json"},
		{"check", "--target", "claude", "--json"},
		{"version", "--json"},
		{"explain", "context.architecture", "--target", "claude", "--json"},
	} {
		res := h.run(args...)
		var doc map[string]any
		if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
			t.Fatalf("stdout for %v is not a single JSON document: %v\n%s", args, err, res.stdout)
		}
		for _, key := range []string{"schemaVersion", "command", "status", "exitCode", "diagnostics"} {
			if _, ok := doc[key]; !ok {
				t.Errorf("%v: JSON envelope is missing %q", args, key)
			}
		}
		if got := int(doc["exitCode"].(float64)); got != res.code {
			t.Errorf("%v: envelope exitCode %d != process exit code %d", args, got, res.code)
		}
	}
}

func TestJSONErrorsAlsoUseTheEnvelope(t *testing.T) {
	h := newHarness(t)
	res := h.run("validate", "--json")
	if res.code == cli.ExitOK {
		t.Fatal("validate without a project should fail")
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("error output is not JSON: %v\n%s", err, res.stdout)
	}
	if doc["status"] != "error" || doc["error"] == nil {
		t.Errorf("error envelope = %v", doc)
	}
}

func TestUnsupportedTargetExitCode(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	h.run("import", "--from", "github-copilot")
	res := h.run("plan", "--target", "cursor")
	if res.code != cli.ExitUnsupportedTarget {
		t.Fatalf("exit = %d, want %d (stderr: %s)", res.code, cli.ExitUnsupportedTarget, res.stderr)
	}
	if !strings.Contains(res.stderr, "not implemented") {
		t.Errorf("stderr should say the target is not implemented: %s", res.stderr)
	}
}

func TestUsageErrors(t *testing.T) {
	h := newHarness(t)
	cases := [][]string{
		{},
		{"nonsense"},
		{"plan"},
		{"check"},
		{"explain"},
	}
	for _, args := range cases {
		if res := h.run(args...); res.code != cli.ExitUsage {
			t.Errorf("%v exit = %d, want %d", args, res.code, cli.ExitUsage)
		}
	}
}

func TestInitDoesNotOverwrite(t *testing.T) {
	h := newHarness(t)
	if res := h.run("init"); res.code != cli.ExitOK {
		t.Fatalf("first init failed: %s", res.stderr)
	}
	before := h.read(".stemma/project.json")
	res := h.run("init")
	if res.code == cli.ExitOK {
		t.Fatal("a second init must fail")
	}
	if h.read(".stemma/project.json") != before {
		t.Fatal("init overwrote an existing project")
	}
}

func TestImportRefusesToOverwriteWithoutFlag(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	if res := h.run("import", "--from", "github-copilot"); res.code != cli.ExitOK {
		t.Fatalf("first import failed: %s", res.stderr)
	}
	before := h.read(".stemma/project.json")
	res := h.run("import", "--from", "github-copilot")
	if res.code == cli.ExitOK {
		t.Fatal("import must refuse to replace a project without --overwrite in a non-TTY session")
	}
	if h.read(".stemma/project.json") != before {
		t.Fatal("import replaced the project without authorization")
	}
}

func TestAmbiguousSourceIsRefused(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.write("CLAUDE.md", "# Project\n\n## Style\n\nUse tabs.\n")
	h.run("init")
	res := h.run("import")
	if res.code == cli.ExitOK {
		t.Fatal("import must refuse to merge several sources silently")
	}
	if !strings.Contains(res.stderr, "--from") && !strings.Contains(res.stderr, "several") {
		t.Errorf("stderr should explain the ambiguity: %s", res.stderr)
	}
}

func TestSymlinkEscapeIsRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	h.run("import", "--from", "github-copilot")

	outside := filepath.Join(t.TempDir(), "target.md")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(h.root, "CLAUDE.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	res := h.run("apply", "--target", "claude", "--yes")
	if res.code == cli.ExitOK {
		t.Fatal("writing through a symlink must be refused")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "outside\n" {
		t.Fatalf("the symlink target was modified: %q (%v)", data, err)
	}
}

func TestExplainDescribesMapping(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	h.run("import", "--from", "github-copilot")
	res := h.run("explain", "context.api-layer-conventions", "--target", "codex")
	if res.code != cli.ExitOK {
		t.Fatalf("exit = %d, stderr = %s", res.code, res.stderr)
	}
	for _, want := range []string{"outcome:", "destination:", "reason:", "Capability decisions", "activation:"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("explain output is missing %q:\n%s", want, res.stdout)
		}
	}
}

func TestExplainUnknownEntity(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	h.run("import", "--from", "github-copilot")
	if res := h.run("explain", "rule.nope", "--target", "claude"); res.code != cli.ExitUsage {
		t.Fatalf("exit = %d, want %d", res.code, cli.ExitUsage)
	}
}

func TestCheckPassesAfterApply(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	h.run("import", "--from", "github-copilot")
	h.run("apply", "--target", "claude", "--yes")
	res := h.run("check", "--target", "claude")
	if res.code != cli.ExitOK {
		t.Fatalf("check after apply exit = %d\n%s\n%s", res.code, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "up to date") {
		t.Errorf("stdout = %s", res.stdout)
	}
}

func TestVersionReportsCursorAsUnimplemented(t *testing.T) {
	h := newHarness(t)
	res := h.run("version")
	if res.code != cli.ExitOK {
		t.Fatal(res.stderr)
	}
	if !strings.Contains(res.stdout, "cursor (declared, not implemented)") {
		t.Errorf("version output must not present cursor as supported:\n%s", res.stdout)
	}
}

func TestImportWorksWithoutInit(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	if res := h.run("import", "--from", "github-copilot"); res.code != cli.ExitOK {
		t.Fatalf("exit = %d, stderr = %s", res.code, res.stderr)
	}
	if !h.exists(".stemma/project.json") {
		t.Fatal("import must create the canonical project on its own")
	}
}

func TestImportTargetsFlag(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	res := h.run("import", "--from", "github-copilot", "--targets", "claude,github-copilot")
	if res.code != cli.ExitOK {
		t.Fatalf("exit = %d, stderr = %s", res.code, res.stderr)
	}
	var project map[string]any
	if err := json.Unmarshal([]byte(h.read(".stemma/project.json")), &project); err != nil {
		t.Fatal(err)
	}
	targets, _ := project["targets"].([]any)
	if len(targets) != 2 || targets[0] != "claude" || targets[1] != "github-copilot" {
		t.Fatalf("targets = %v", targets)
	}
	// Unknown and unimplemented targets are refused.
	if res := h.run("import", "--from", "github-copilot", "--overwrite", "--targets", "nonsense"); res.code != cli.ExitUsage {
		t.Errorf("unknown target exit = %d, want %d", res.code, cli.ExitUsage)
	}
	if res := h.run("import", "--from", "github-copilot", "--overwrite", "--targets", "cursor"); res.code != cli.ExitUnsupportedTarget {
		t.Errorf("unimplemented target exit = %d, want %d", res.code, cli.ExitUnsupportedTarget)
	}
}

func TestApplyAllAppliesEveryTarget(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("import", "--from", "github-copilot", "--targets", "claude,codex,github-copilot")
	res := h.run("apply", "--all", "--yes")
	if res.code != cli.ExitOK {
		t.Fatalf("exit = %d, stderr = %s", res.code, res.stderr)
	}
	for _, path := range []string{"CLAUDE.md", "AGENTS.md", ".claude/rules/api-layer-conventions.md"} {
		if !h.exists(path) {
			t.Errorf("%s was not written", path)
		}
	}
	if res := h.run("check", "--all"); res.code != cli.ExitOK {
		t.Errorf("check after apply --all exit = %d\n%s", res.code, res.stdout)
	}
}

func TestPlanAllCoversEveryTarget(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("import", "--from", "github-copilot", "--targets", "claude,kiro")
	before := h.snapshot()
	res := h.run("plan", "--all")
	if res.code != cli.ExitOK {
		t.Fatalf("exit = %d, stderr = %s", res.code, res.stderr)
	}
	for _, want := range []string{"Plan for target claude", "Plan for target kiro"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("stdout is missing %q", want)
		}
	}
	assertSameTree(t, before, h.snapshot(), "plan --all")

	// JSON with several targets is wrapped so consumers can tell the shapes apart.
	jsonRes := h.run("plan", "--all", "--json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(jsonRes.stdout), &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	data, _ := doc["data"].(map[string]any)
	plans, _ := data["targets"].([]any)
	if len(plans) != 2 {
		t.Fatalf("expected two plans in the payload, got %v", data)
	}
}

func TestRepeatableTargetFlag(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("import", "--from", "github-copilot")
	res := h.run("plan", "--target", "claude", "--target", "codex")
	if res.code != cli.ExitOK {
		t.Fatalf("exit = %d, stderr = %s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "Plan for target claude") ||
		!strings.Contains(res.stdout, "Plan for target codex") {
		t.Errorf("both targets should be planned:\n%s", res.stdout)
	}
}

func TestAllAndTargetAreMutuallyExclusive(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("import", "--from", "github-copilot")
	if res := h.run("plan", "--all", "--target", "claude"); res.code != cli.ExitUsage {
		t.Errorf("exit = %d, want %d", res.code, cli.ExitUsage)
	}
	if res := h.run("apply", "--all", "--target", "claude", "--yes"); res.code != cli.ExitUsage {
		t.Errorf("exit = %d, want %d", res.code, cli.ExitUsage)
	}
}

func TestAllWithoutTargetsInProject(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	// A freshly initialised project enables no targets.
	res := h.run("plan", "--all")
	if res.code != cli.ExitUsage {
		t.Fatalf("exit = %d, want %d (stderr: %s)", res.code, cli.ExitUsage, res.stderr)
	}
	if !strings.Contains(res.stderr, "targets") {
		t.Errorf("stderr should explain how to fix it: %s", res.stderr)
	}
}

// TestNoOpApplyClaimsOwnership covers the case that used to leave the format
// you imported from untracked: applying it changes no file, but Stemma must
// still record that it owns those files, or the next real change is reported
// as a conflict.
func TestNoOpApplyClaimsOwnership(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("import", "--from", "github-copilot")

	original := h.read(".github/copilot-instructions.md")
	if res := h.run("apply", "--target", "github-copilot", "--yes"); res.code != cli.ExitOK {
		t.Fatalf("no-op apply exit = %d, stderr = %s", res.code, res.stderr)
	}
	if h.read(".github/copilot-instructions.md") != original {
		t.Fatal("a no-op apply rewrote a file it should have left alone")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(h.read(".stemma/manifest.json")), &m); err != nil {
		t.Fatal(err)
	}
	targets, _ := m["targets"].(map[string]any)
	if _, ok := targets["github-copilot"]; !ok {
		t.Fatalf("the manifest does not record ownership: %v", targets)
	}

	// A later real change must apply cleanly, with no conflict.
	project := h.read(".stemma/project.json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(project), &doc); err != nil {
		t.Fatal(err)
	}
	docs, _ := doc["contextDocuments"].([]any)
	for _, d := range docs {
		m := d.(map[string]any)
		if m["id"] == "context.testing" {
			m["content"] = m["content"].(string) + "\n\nAlso run the linter."
		}
	}
	updated, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	h.write(".stemma/project.json", string(updated)+"\n")

	res := h.run("apply", "--target", "github-copilot", "--yes")
	if res.code != cli.ExitOK {
		t.Fatalf("apply after a real edit exit = %d\n%s\n%s", res.code, res.stdout, res.stderr)
	}
	if !strings.Contains(h.read(".github/copilot-instructions.md"), "Also run the linter.") {
		t.Fatal("the edit did not reach the generated file")
	}
}
