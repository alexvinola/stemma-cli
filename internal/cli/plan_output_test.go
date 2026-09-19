package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/cli"
)

func newImportedPlanHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	if res := h.run("init"); res.code != cli.ExitOK {
		t.Fatalf("init: %+v", res)
	}
	if res := h.run("import", "--from", "github-copilot"); res.code != cli.ExitOK {
		t.Fatalf("import: %+v", res)
	}
	return h
}

func TestPlanHumanOutputDistinguishesWrites(t *testing.T) {
	t.Run("read only", func(t *testing.T) {
		h := newImportedPlanHarness(t)
		before := h.snapshot()
		res := h.run("plan", "--target", "claude")
		if res.code != cli.ExitOK {
			t.Fatalf("exit = %d, stderr = %s", res.code, res.stderr)
		}
		if !strings.Contains(res.stdout, "Nothing was modified.") ||
			strings.Contains(res.stdout, "Plan file written") ||
			strings.Contains(res.stdout, "No generated target changes were applied") {
			t.Fatalf("plan did not report its read-only result clearly:\n%s", res.stdout)
		}
		assertSameTree(t, before, h.snapshot(), "plan without --output-plan")
	})

	t.Run("saved plan", func(t *testing.T) {
		h := newImportedPlanHarness(t)
		path := "./review//plan.json"
		res := h.run("plan", "--target", "claude", "--output-plan", path)
		if res.code != cli.ExitOK {
			t.Fatalf("exit = %d, stderr = %s", res.code, res.stderr)
		}
		for _, want := range []string{
			"Plan file written transactionally: review/plan.json",
			"No generated target changes were applied.",
			"stemma apply --plan <path>",
		} {
			if !strings.Contains(res.stdout, want) {
				t.Fatalf("saved-plan output lacks %q:\n%s", want, res.stdout)
			}
		}
		if strings.Contains(res.stdout, "Nothing was modified") {
			t.Fatalf("saved-plan output claims that nothing was written:\n%s", res.stdout)
		}
		if !h.exists("review/plan.json") {
			t.Fatal("--output-plan did not write the plan file")
		}
		if h.exists("CLAUDE.md") {
			t.Fatal("plan wrote a generated target file")
		}
	})

	t.Run("rejected write", func(t *testing.T) {
		h := newImportedPlanHarness(t)
		h.write("blocked", "not a directory\n")
		before := h.snapshot()
		path := "blocked/\x1b[31mplan.json"
		res := h.run("plan", "--target", "claude", "--output-plan", path)
		if res.code != cli.ExitWriteFailed {
			t.Fatalf("exit = %d, want %d: %+v", res.code, cli.ExitWriteFailed, res)
		}
		if strings.Contains(res.stderr, "\x1b") {
			t.Fatalf("stderr contains an unsanitized escape: %q", res.stderr)
		}
		// OS errors may expose a raw path (escaped by SanitizeLine as \\e)
		// or quote it with %q first (escaping ESC as \\x1b). Both are safe.
		if !strings.Contains(res.stderr, `blocked/\e[31mplan.json`) &&
			!strings.Contains(res.stderr, `blocked/\x1b[31mplan.json`) {
			t.Fatalf("failed-save output lacks the sanitized path:\n%s", res.stderr)
		}
		if strings.Contains(res.stdout+res.stderr, "Plan file written transactionally") {
			t.Fatalf("failed save printed a success banner: %+v", res)
		}
		assertSameTree(t, before, h.snapshot(), "rejected plan-file write")
	})

	t.Run("blocked without saved plan", func(t *testing.T) {
		h := newImportedPlanHarness(t)
		h.write("CLAUDE.md", "hand-written instructions\n")
		before := h.snapshot()
		res := h.run("plan", "--target", "claude")
		if res.code != cli.ExitDiagnostics {
			t.Fatalf("exit = %d, want %d: %+v", res.code, cli.ExitDiagnostics, res)
		}
		if !strings.Contains(res.stdout, "Nothing was modified. Blocking diagnostics prevent this plan from being applied.") {
			t.Fatalf("blocked plan lacks an accurate final status:\n%s", res.stdout)
		}
		if strings.Contains(res.stdout, "Run `stemma apply") {
			t.Fatalf("blocked plan suggested applying it:\n%s", res.stdout)
		}
		assertSameTree(t, before, h.snapshot(), "blocked plan without --output-plan")
	})

	t.Run("saved but blocked", func(t *testing.T) {
		h := newImportedPlanHarness(t)
		h.write("CLAUDE.md", "hand-written instructions\n")
		res := h.run("plan", "--target", "claude", "--output-plan", "blocked-plan.json", "--explain")
		if res.code != cli.ExitDiagnostics {
			t.Fatalf("exit = %d, want %d: %+v", res.code, cli.ExitDiagnostics, res)
		}
		for _, want := range []string{
			"Plan file written transactionally: blocked-plan.json",
			"No generated target changes were applied.",
			"Blocking diagnostics prevent the saved plan from being applied.",
			"This plan cannot be applied until the errors above are resolved.",
		} {
			if !strings.Contains(res.stdout, want) {
				t.Fatalf("blocked-plan output lacks %q:\n%s", want, res.stdout)
			}
		}
		if strings.Contains(res.stdout, "Review it, then apply it") {
			t.Fatalf("blocked plan was presented as applicable:\n%s", res.stdout)
		}
		if !h.exists("blocked-plan.json") {
			t.Fatal("blocking diagnostics unexpectedly prevented saving the plan")
		}
		if got := h.read("CLAUDE.md"); got != "hand-written instructions\n" {
			t.Fatalf("plan modified the target file: %q", got)
		}
	})
}

func TestPlanOutputPlanJSONEnvelopeIsUnchanged(t *testing.T) {
	h := newImportedPlanHarness(t)
	res := h.run("plan", "--target", "claude", "--output-plan", "plan.json", "--json")
	if res.code != cli.ExitOK || res.stderr != "" {
		t.Fatalf("unexpected result: %+v", res)
	}
	var envelope struct {
		Command  string          `json:"command"`
		Status   string          `json:"status"`
		ExitCode int             `json:"exitCode"`
		Data     json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &envelope); err != nil {
		t.Fatalf("stdout is not one JSON envelope: %v\n%s", err, res.stdout)
	}
	if envelope.Command != "plan" || envelope.Status != "ok" || envelope.ExitCode != cli.ExitOK || len(envelope.Data) == 0 {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	for _, human := range []string{"Plan file written", "No generated target changes", "Nothing was modified"} {
		if strings.Contains(res.stdout, human) {
			t.Fatalf("JSON output contains human status %q: %s", human, res.stdout)
		}
	}
}

func TestHelpDocumentsOutputPlanWrite(t *testing.T) {
	h := newHarness(t)
	res := h.run("help")
	if res.code != cli.ExitOK || res.stderr != "" {
		t.Fatalf("unexpected help result: %+v", res)
	}
	if !strings.Contains(res.stdout,
		"plan        Compile a target and show what would change (read-only unless --output-plan)") {
		t.Fatalf("help does not document the plan-file write:\n%s", res.stdout)
	}
}
