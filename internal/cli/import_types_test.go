package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/cli"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
)

func TestWrongTypeImportBlocksWithoutWriting(t *testing.T) {
	for _, tc := range []struct{ format, path, field, content string }{
		{"github-copilot", ".github/instructions/scoped.instructions.md", "applyTo", "---\napplyTo: [\"src/**\"]\n---\nScoped only.\n"},
		{"claude", ".claude/rules/scoped.md", "enabled", "---\npaths: [\"src/**\"]\nenabled: []\n---\nKeep disabled.\n"},
		{"kiro", ".kiro/steering/scoped.md", "inclusion", "---\ninclusion: {}\n---\nScoped only.\n"},
		{"kiro", ".kiro/agents/reviewer.json", "tools", `{"prompt":"Review only.","tools":null}`},
		{"codex", ".agents/skills/review/SKILL.md", "name", "---\nname: []\n---\nReview only.\n"},
	} {
		for _, existing := range []bool{false, true} {
			t.Run(tc.path+"/existing="+map[bool]string{false: "false", true: "true"}[existing], func(t *testing.T) {
				h := newHarness(t)
				h.write(tc.path, tc.content)
				if existing {
					h.write(".stemma/project.json", `{"schemaVersion":2,"id":"p","name":"p","targets":["claude"]}`)
					h.write(".stemma/context/keep.md", "---\ntitle: Keep\nactivation: {type: always}\n---\nKeep this entity.\n")
				}
				before := h.snapshot()
				for _, jsonOut := range []bool{false, true} {
					args := []string{"import", "--from", tc.format, "--overwrite"}
					if jsonOut {
						args = append(args, "--json")
					}
					r := h.run(args...)
					if r.code != cli.ExitDiagnostics {
						t.Fatalf("exit=%d stdout=%s stderr=%s", r.code, r.stdout, r.stderr)
					}
					if jsonOut {
						var report struct{ Diagnostics []diagnostics.Diagnostic }
						if err := json.Unmarshal([]byte(r.stdout), &report); err != nil {
							t.Fatal(err)
						}
						matched := false
						for _, d := range report.Diagnostics {
							if d.Blocking && d.Path == tc.path && strings.Contains(d.Summary, tc.field) && strings.Contains(d.Summary, "found ") {
								matched = true
							}
						}
						if !matched {
							t.Fatalf("missing type diagnostic: %s", r.stdout)
						}
					} else if !strings.Contains(r.stderr, tc.path) || !strings.Contains(r.stderr, tc.field) || !strings.Contains(r.stderr, "found ") {
						t.Fatalf("missing type diagnostic: %s", r.stderr)
					}
					assertSameTree(t, before, h.snapshot(), "blocked import")
				}
			})
		}
	}
}
