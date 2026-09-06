package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/cli"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/profiles"
)

func TestDestinationCollisionReturnsInternalDiagnosticWithoutWriting(t *testing.T) {
	h := newHarness(t)
	h.write(".claude/rules/first.md", "---\npaths: [api/**]\ndescription: Testing\n---\nAPI body.\n")
	h.write(".claude/rules/second.md", "---\npaths: [web/**]\ndescription: Testing\n---\nWeb body.\n")
	if r := h.run("import", "--from", "claude", "--targets", "github-copilot"); r.code != cli.ExitOK {
		t.Fatalf("import: %+v", r)
	}
	profile := profiles.Default(canonical.TargetCopilot)
	for _, id := range []string{"rule.testing", "rule.testing-2"} {
		profile.Overrides[id] = profiles.Override{Filename: "shared.instructions.md"}
	}
	data, err := profiles.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	h.write(".stemma/profiles/github-copilot.json", string(data))
	h.write(".github/instructions/shared.instructions.md", "Keep this user file.\n")
	before := h.snapshot()
	for _, command := range []string{"plan", "apply", "check"} {
		for _, jsonOut := range []bool{false, true} {
			args := []string{command, "--target", "github-copilot"}
			if command == "apply" {
				args = append(args, "--yes")
			}
			if jsonOut {
				args = append(args, "--json")
			}
			r := h.run(args...)
			if r.code != cli.ExitInternal {
				t.Fatalf("%s: expected exit 6, got %+v", command, r)
			}
			if jsonOut {
				var report struct{ Diagnostics []diagnostics.Diagnostic }
				if err := json.Unmarshal([]byte(r.stdout), &report); err != nil {
					t.Fatal(err)
				}
				if len(report.Diagnostics) != 1 || report.Diagnostics[0].Code != diagnostics.InternalInvariant || !report.Diagnostics[0].Blocking {
					t.Fatalf("missing collision diagnostic: %s", r.stdout)
				}
			} else if !strings.Contains(r.stderr, string(diagnostics.InternalInvariant)) || !strings.Contains(r.stderr, "shared.instructions.md") {
				t.Fatalf("missing conflict path: %+v", r)
			}
			assertSameTree(t, before, h.snapshot(), command)
		}
	}
}
