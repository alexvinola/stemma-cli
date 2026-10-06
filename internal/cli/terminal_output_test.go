package cli_test

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"unicode"

	"github.com/alexvinola/stemma-cli/internal/cli"
)

func assertTerminalSafe(t *testing.T, output string) {
	t.Helper()
	for _, r := range output {
		if (unicode.IsControl(r) && r != '\n' && r != '\t') || unicode.In(r, unicode.Cf) {
			t.Fatalf("raw terminal control %U in %q", r, output)
		}
	}
}

func TestSanitizeLineEscapesControls(t *testing.T) {
	input := "name\x1b[31m\r\x7f\u0085\u202e\n\tfile.md"
	want := "name\\e[31m\\x0d\\x7f\\x85\\u202e  file.md"
	if got := cli.SanitizeLine(input); got != want {
		t.Fatalf("sanitized line = %q, want %q", got, want)
	}
}

func TestProfilePathsAreSanitizedOnlyForHumanOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses filenames containing ASCII control characters")
	}
	h := sourceNamesHarness(t)
	filename := "safe\x1b[31m\r\nFORGED\u0085\u202e.md"
	destination := ".claude/rules/" + filename
	profile := map[string]any{"schemaVersion": 1, "target": "claude",
		"overrides": map[string]any{"context.python-conventions-for-every-service": map[string]any{
			"directory": ".claude/rules", "filename": filename}}, "acceptedDiagnostics": []string{}}
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	h.write(".stemma/profiles/claude.json", string(data))
	report := h.planJSON(t)
	if report.ExitCode != cli.ExitOK {
		t.Fatalf("plan: %+v", report.Diagnostics)
	}
	if _, ok := changeKinds(report.Data)[destination]; !ok {
		t.Fatalf("JSON changed the destination: %+v", report.Data.Changes)
	}
	sanitized := cli.SanitizeLine(destination)
	check := func(res result, code int) {
		t.Helper()
		if res.code != code {
			t.Fatalf("exit = %d, want %d; stdout=%q stderr=%q", res.code, code, res.stdout, res.stderr)
		}
		assertTerminalSafe(t, res.stdout+res.stderr)
		if !strings.Contains(res.stdout, sanitized) {
			t.Fatalf("sanitized destination missing: %q", res.stdout)
		}
	}
	check(h.run("plan", "--target", "claude", "--explain"), cli.ExitOK)
	check(h.run("explain", "context.python-conventions-for-every-service", "--target", "claude"), cli.ExitOK)
	check(h.run("check", "--target", "claude"), cli.ExitDiagnostics)
	// Exercise the confirmation list without accepting its filesystem writes.
	before := h.snapshot()
	check(h.runWith("n\n", true, "apply", "--target", "claude"), cli.ExitOK)
	assertSameTree(t, before, h.snapshot(), "cancelled apply")
	check(h.run("apply", "--target", "claude", "--yes"), cli.ExitOK)
	if !h.exists(destination) {
		t.Fatal("apply sanitized the actual filesystem path")
	}
	check(h.run("plan", "--target", "claude", "--show-unchanged", "--explain"), cli.ExitOK)
	check(h.run("apply", "--target", "claude", "--yes"), cli.ExitOK)
	if res := h.run("check", "--target", "claude"); res.code != cli.ExitOK {
		t.Fatalf("check after apply: %q %q", res.stdout, res.stderr)
	}
}
