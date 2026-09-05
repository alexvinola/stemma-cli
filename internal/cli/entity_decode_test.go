package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/cli"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
)

func TestMalformedCanonicalEntitiesReturnDiagnosticsWithoutWriting(t *testing.T) {
	for _, file := range []struct{ dir, id string }{
		{"context", "context.broken"}, {"rules", "rule.broken"},
		{"procedures", "procedure.broken"}, {"skills", "skill.broken"},
		{"agents", "agent.broken"}, {"decisions", "decision.broken"},
	} {
		for _, malformed := range []struct{ name, body string }{
			{"missing-header", "Plain text, no front matter.\n"},
			{"unterminated-header", "---\nname: broken\n"},
			{"wrong-type", "---\nname: broken\ntitle: Broken\nactivation:\n  type: always\nextensions: [invalid]\n---\nBody.\n"},
		} {
			t.Run(file.dir+"/"+malformed.name, func(t *testing.T) {
				h := newHarness(t)
				h.write(".stemma/project.json", `{"schemaVersion":2,"id":"p","name":"p","targets":["claude"]}`)
				path := ".stemma/" + file.dir + "/broken.md"
				h.write(path, malformed.body)
				// Another bad file verifies that loading preserves all per-file errors.
				h.write(".stemma/skills/other.md", "Also missing its header.\n")
				h.write("CLAUDE.md", "Existing instructions must stay untouched.\n")
				before := h.snapshot()
				commands := [][]string{
					{"validate"}, {"plan", "--target", "claude"}, {"apply", "--target", "claude", "--yes"},
					{"check", "--all"}, {"explain", file.id, "--target", "claude"},
				}
				for _, args := range commands {
					for _, jsonOut := range []bool{false, true} {
						invocation := append([]string{}, args...)
						if jsonOut {
							invocation = append(invocation, "--json")
						}
						r := h.run(invocation...)
						if r.code != cli.ExitDiagnostics {
							t.Fatalf("%v: exit %d: %s %s", invocation, r.code, r.stdout, r.stderr)
						}
						if jsonOut {
							var report struct {
								Command     string
								Status      string
								ExitCode    int
								Diagnostics []diagnostics.Diagnostic
							}
							if !json.Valid([]byte(r.stdout)) {
								t.Fatalf("%v: stdout is not one JSON document: %s", invocation, r.stdout)
							}
							if err := json.Unmarshal([]byte(r.stdout), &report); err != nil {
								t.Fatal(err)
							}
							if report.Command != args[0] || report.Status != "error" || report.ExitCode != cli.ExitDiagnostics || r.stderr != "" {
								t.Fatalf("invalid error envelope: %+v; stderr=%s", report, r.stderr)
							}
							for _, wantPath := range []string{path, ".stemma/skills/other.md"} {
								found := false
								for _, d := range report.Diagnostics {
									if d.Path == wantPath && d.Blocking && d.Severity == diagnostics.SeverityError {
										found = true
									}
								}
								if !found {
									t.Fatalf("%v: missing blocking diagnostic for %s: %+v", invocation, wantPath, report.Diagnostics)
								}
							}
						} else if !strings.Contains(r.stderr, path) || !strings.Contains(r.stderr, "STEMMA") {
							t.Fatalf("%v: no useful file diagnostic: %s", invocation, r.stderr)
						}
						assertSameTree(t, before, h.snapshot(), strings.Join(invocation, " "))
					}
				}
			})
		}
	}
}
