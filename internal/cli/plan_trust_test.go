package cli_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/cli"
)

// A saved plan is a file in the repository. It exists to be committed,
// reviewed and replayed in CI, which is exactly the workflow where a pull
// request can rewrite it. These tests assert that editing one can never turn
// `apply --plan` into a write primitive, and that every refusal leaves the
// repository byte-identical.

// planHarness sets up a repository with a canonical project and a saved plan.
func planHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.run("init")
	h.run("import", "--from", "github-copilot")
	if res := h.run("plan", "--target", "claude", "--output-plan", "plan.json"); res.code != cli.ExitOK {
		t.Fatalf("plan exit = %d, stderr = %s", res.code, res.stderr)
	}
	return h
}

// editPlan rewrites the saved plan through fn.
func editPlan(t *testing.T, h *harness, fn func(plan map[string]any)) {
	t.Helper()
	var plan map[string]any
	if err := json.Unmarshal([]byte(h.read("plan.json")), &plan); err != nil {
		t.Fatal(err)
	}
	fn(plan)
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	h.write("plan.json", string(data))
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// change builds a single hand-written plan entry whose hash is internally
// consistent, so the rejection cannot come from the cheap hash check alone.
func change(path, kind, content string) map[string]any {
	return map[string]any{
		"path": path, "kind": kind,
		"newHash": hashOf(content), "content": content,
		"entities": []any{},
	}
}

// assertRejectedWithoutWriting runs apply and requires it to refuse without
// touching a single byte of the repository.
func assertRejectedWithoutWriting(t *testing.T, h *harness, want string) {
	t.Helper()
	before := h.snapshot()
	res := h.run("apply", "--plan", "plan.json", "--yes")
	if res.code == cli.ExitOK {
		t.Fatalf("apply accepted a tampered plan (stdout: %s)", res.stdout)
	}
	if want != "" && !strings.Contains(res.stderr, want) {
		t.Errorf("stderr should mention %q, got:\n%s", want, res.stderr)
	}
	assertSameTree(t, before, h.snapshot(), "a rejected plan")
}

func TestSavedPlanCannotOverwriteAnUntrackedFile(t *testing.T) {
	// The reported P0: a hand-edited plan naming any path in the workspace.
	// The declared hash does not match the content, which is the cheapest
	// signal that the document was edited after Stemma wrote it.
	h := planHarness(t)
	h.write("IMPORTANTE.md", "important user content\n")
	editPlan(t, h, func(plan map[string]any) {
		c := change("IMPORTANTE.md", "update", "PWNED\n")
		c["newHash"] = "sha256:" + strings.Repeat("0", 64)
		c["existingHash"] = hashOf("important user content\n")
		plan["changes"] = []any{c}
	})

	assertRejectedWithoutWriting(t, h, "newHash")
	if h.read("IMPORTANTE.md") != "important user content\n" {
		t.Fatal("a hand-edited plan overwrote an untracked file")
	}
}

func TestSavedPlanWithConsistentHashesIsStillRejected(t *testing.T) {
	// The same attack from someone who bothers to recompute the hash. The
	// structural checks pass; the rebuild is what refuses, because the current
	// project does not produce this file.
	h := planHarness(t)
	h.write("IMPORTANTE.md", "important user content\n")
	editPlan(t, h, func(plan map[string]any) {
		c := change("IMPORTANTE.md", "update", "PWNED\n")
		c["existingHash"] = hashOf("important user content\n")
		plan["changes"] = []any{c}
	})

	assertRejectedWithoutWriting(t, h, "saved plan rejected")
	if h.read("IMPORTANTE.md") != "important user content\n" {
		t.Fatal("a consistently hashed plan overwrote an untracked file")
	}
}

func TestSavedPlanWithInvalidPathsIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name    string
		changes []any
		want    string
	}{
		{
			name:    "traversal above the root",
			changes: []any{change("../outside.md", "create", "x\n")},
			want:    "not a valid repository path",
		},
		{
			name:    "absolute path",
			changes: []any{change("/etc/passwd", "create", "x\n")},
			want:    "not a valid repository path",
		},
		{
			name:    "not in normalized form",
			changes: []any{change("./CLAUDE.md", "update", "x\n")},
			want:    "normalized form",
		},
		{
			name: "the same path twice",
			changes: []any{
				change("CLAUDE.md", "update", "x\n"),
				change("CLAUDE.md", "update", "y\n"),
			},
			want: "more than one change",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := planHarness(t)
			editPlan(t, h, func(plan map[string]any) { plan["changes"] = tc.changes })
			assertRejectedWithoutWriting(t, h, tc.want)
		})
	}
}

func TestSavedPlanWithUnknownChangeKindIsRejected(t *testing.T) {
	h := planHarness(t)
	editPlan(t, h, func(plan map[string]any) {
		plan["changes"] = []any{change("CLAUDE.md", "obliterate", "x\n")}
	})
	assertRejectedWithoutWriting(t, h, "unknown change kind")
}

func TestSavedPlanWithTrailingDocumentIsRejected(t *testing.T) {
	// Appending a second document after the first is a way to smuggle content
	// past a reviewer who reads the top of the file.
	h := planHarness(t)
	saved := h.read("plan.json")
	h.write("plan.json", saved+"\n"+saved)
	assertRejectedWithoutWriting(t, h, "more than one JSON document")
}

func TestSavedPlanIsRejectedAfterCanonicalDrift(t *testing.T) {
	// The plan was reviewed against a project that no longer exists. Applying
	// it would write content nobody reviewed.
	h := planHarness(t)
	h.write(".stemma/context/architecture.md",
		h.read(".stemma/context/architecture.md")+"\nEdited after the plan was saved.\n")

	assertRejectedWithoutWriting(t, h, "canonical project changed")
	if h.exists("CLAUDE.md") {
		t.Fatal("a drifted plan wrote its target file")
	}
}

func TestSavedPlanIsRejectedAfterProfileDrift(t *testing.T) {
	h := planHarness(t)
	h.write("profile.json", `{
  "schemaVersion": 1,
  "target": "claude",
  "overrides": { "context.architecture": { "include": false } },
  "acceptedDiagnostics": []
}
`)
	before := h.snapshot()
	res := h.run("apply", "--plan", "plan.json", "--profile", "profile.json", "--yes")
	if res.code == cli.ExitOK {
		t.Fatalf("apply accepted a plan built under a different profile (stdout: %s)", res.stdout)
	}
	if !strings.Contains(res.stderr, "saved plan rejected") {
		t.Errorf("stderr should report the rejection, got:\n%s", res.stderr)
	}
	assertSameTree(t, before, h.snapshot(), "a plan applied under a different profile")
}

func TestSavedPlanCannotBypassAdoption(t *testing.T) {
	// A plan built with --adopt-untracked carries updates for files Stemma has
	// never written. Replaying it without the flag must not silently adopt
	// them: the rebuild classifies them as conflicts and the plans disagree.
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	h.write("CLAUDE.md", "hand-written\n")
	h.run("init")
	h.run("import", "--from", "github-copilot")
	if res := h.run("plan", "--target", "claude", "--adopt-untracked",
		"--output-plan", "plan.json"); res.code != cli.ExitOK {
		t.Fatalf("plan exit = %d, stderr = %s", res.code, res.stderr)
	}

	assertRejectedWithoutWriting(t, h, "saved plan rejected")
	if h.read("CLAUDE.md") != "hand-written\n" {
		t.Fatal("replaying an adopt-plan without --adopt-untracked took ownership anyway")
	}

	// With the flag the two plans agree again and the apply goes through.
	if res := h.run("apply", "--plan", "plan.json", "--adopt-untracked", "--yes"); res.code != cli.ExitOK {
		t.Fatalf("apply with --adopt-untracked exit = %d, stderr = %s", res.code, res.stderr)
	}
	if h.read("CLAUDE.md") == "hand-written\n" {
		t.Fatal("--adopt-untracked did not take ownership")
	}
}

func TestUntamperedSavedPlanStillApplies(t *testing.T) {
	// The control: hardening must not break the feature the plan file exists
	// for. Without this, every test above passes if apply --plan simply stops
	// working.
	h := planHarness(t)
	res := h.run("apply", "--plan", "plan.json", "--yes")
	if res.code != cli.ExitOK {
		t.Fatalf("replaying an untouched plan exit = %d, stderr = %s", res.code, res.stderr)
	}
	if !h.exists("CLAUDE.md") {
		t.Fatal("a valid saved plan wrote nothing")
	}
}
