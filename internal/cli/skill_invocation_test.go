package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/cli"
)

func TestRenamedSkillReportsInvocationThroughApply(t *testing.T) {
	for _, target := range []struct{ name, prefix string }{
		{"claude", ".claude"}, {"codex", ".agents"},
		{"github-copilot", ".github"}, {"kiro", ".kiro"},
	} {
		t.Run(target.name, func(t *testing.T) {
			h := newHarness(t)
			h.write(".github/skills/review/SKILL.md", "---\nname: review\ndescription: Review a change\n---\n\nCheck tests.\n")
			if res := h.run("import", "--from", "github-copilot", "--targets", target.name); res.code != cli.ExitOK {
				t.Fatalf("import: %q %q", res.stdout, res.stderr)
			}
			original := h.read(".stemma/skills/review.md")
			directory := target.prefix + "/skills/renamed"
			profile := `{"schemaVersion":1,"target":"` + target.name + `","overrides":{"skill.review":{"directory":"` +
				directory + `","filename":"SKILL.md"}},"acceptedDiagnostics":[]}`
			h.write(".stemma/profiles/"+target.name+".json", profile)
			res := h.run("plan", "--target", target.name, "--output-plan", "reviewed-plan.json", "--json")
			var report planReport
			if err := json.Unmarshal([]byte(res.stdout), &report); err != nil || res.code != cli.ExitOK {
				t.Fatalf("plan: %v %q %q", err, res.stdout, res.stderr)
			}
			if len(report.Data.Mappings) != 1 || report.Data.Mappings[0].Activation.InvocationName != "renamed" ||
				report.Data.Mappings[0].Outcome != "adapted" {
				t.Fatalf("mapping = %+v", report.Data.Mappings)
			}
			if res := h.run("explain", "skill.review", "--target", target.name); res.code != cli.ExitOK ||
				!strings.Contains(res.stdout, "renamed") {
				t.Fatalf("explain: %q %q", res.stdout, res.stderr)
			}
			if res := h.run("apply", "--plan", "reviewed-plan.json", "--yes"); res.code != cli.ExitOK {
				t.Fatalf("apply: %q %q", res.stdout, res.stderr)
			}
			if !strings.Contains(h.read(directory+"/SKILL.md"), "\nname: renamed\n") {
				t.Fatal("rendered skill does not use its projected name")
			}
			if h.read(".stemma/skills/review.md") != original {
				t.Fatal("projection changed canonical identity or activation")
			}
			wantCheck := cli.ExitOK
			if target.name == "github-copilot" {
				// Renaming same-provider output retires the original. Stemma
				// proposes its deletion, but leaves the user's source intact.
				wantCheck = cli.ExitDiagnostics
				var again planReport
				res := h.run("plan", "--target", target.name, "--json")
				if err := json.Unmarshal([]byte(res.stdout), &again); err != nil {
					t.Fatal(err)
				}
				kinds := changeKinds(again.Data)
				if kinds[".github/skills/review/SKILL.md"] != "delete-proposed" || kinds[directory+"/SKILL.md"] != "unchanged" {
					t.Fatalf("re-plan after same-provider rename: %+v", again.Data.Changes)
				}
			}
			if res := h.run("check", "--target", target.name); res.code != wantCheck {
				t.Fatalf("check: %q %q", res.stdout, res.stderr)
			}
		})
	}
}
