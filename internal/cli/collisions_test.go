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

func TestCaseEquivalentDestinationCollisionsBlockBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name      string
		overrides map[string]profiles.Override
		aliases   [2]string
	}{
		{
			name: "filenames",
			overrides: map[string]profiles.Override{
				"rule.testing":   {Filename: "Scope.md"},
				"rule.testing-2": {Filename: "scope.md"},
			},
			aliases: [2]string{".claude/rules/Scope.md", ".claude/rules/scope.md"},
		},
		{
			name: "file and equivalent ancestor",
			overrides: map[string]profiles.Override{
				"rule.testing":   {Directory: ".claude/rules", Filename: "Scope"},
				"rule.testing-2": {Directory: ".CLAUDE/RULES/scope", Filename: "child.md"},
			},
			aliases: [2]string{".claude/rules/Scope", ".claude/rules/scope"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.write(".claude/rules/first.md", "---\npaths: [api/**]\ndescription: Testing\n---\nAPI body.\n")
			h.write(".claude/rules/second.md", "---\npaths: [web/**]\ndescription: Testing\n---\nWeb body.\n")
			if r := h.run("import", "--from", "claude", "--targets", "claude"); r.code != cli.ExitOK {
				t.Fatalf("import: %+v", r)
			}
			profile := profiles.Default(canonical.TargetClaude)
			profile.Overrides = tc.overrides
			data, err := profiles.Marshal(profile)
			if err != nil {
				t.Fatal(err)
			}
			h.write(".stemma/profiles/claude.json", string(data))
			h.write(tc.aliases[0], "Keep this user file.\n")
			// On a case-insensitive filesystem this confirms the fixture exercises
			// the same physical alias that originally caused the silent overwrite.
			if h.exists(tc.aliases[1]) && h.read(tc.aliases[1]) != "Keep this user file.\n" {
				t.Fatalf("physical alias %s did not resolve to the fixture", tc.aliases[1])
			}

			before := h.snapshot()
			for _, command := range []string{"plan", "apply", "check"} {
				args := []string{command, "--target", "claude", "--json"}
				if command == "apply" {
					args = append(args, "--yes")
				}
				r := h.run(args...)
				if r.code != cli.ExitInternal {
					t.Fatalf("%s: expected exit 6, got %+v", command, r)
				}
				var report struct {
					Diagnostics []diagnostics.Diagnostic
				}
				if err := json.Unmarshal([]byte(r.stdout), &report); err != nil {
					t.Fatal(err)
				}
				if len(report.Diagnostics) != 2 {
					t.Fatalf("%s: expected one diagnostic per conflicting path: %s", command, r.stdout)
				}
				for _, d := range report.Diagnostics {
					if d.Code != diagnostics.InternalInvariant || !d.Blocking {
						t.Fatalf("%s: unexpected diagnostic: %+v", command, d)
					}
				}
				assertSameTree(t, before, h.snapshot(), command)
			}
		})
	}
}
