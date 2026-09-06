package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
)

func TestHandwrittenBraceScopeSurvivesApplyAndReimport(t *testing.T) {
	h := newHarness(t)
	h.write(".claude/rules/api.md", "---\npaths:\n  - src/**/*.ts\n---\nUse explicit types.\n")
	if r := h.run("import", "--from", "claude", "--targets", "github-copilot"); r.code != 0 {
		t.Fatalf("import: %+v", r)
	}
	h.write(".stemma/rules/api.md", strings.ReplaceAll(h.read(".stemma/rules/api.md"), "src/**/*.ts", "src/**/*.{ts,tsx}"))
	before := h.snapshot()
	r := h.run("plan", "--target", "github-copilot", "--json")
	var envelope struct {
		Data compiler.Plan `json:"data"`
	}
	if r.code != 0 || json.Unmarshal([]byte(r.stdout), &envelope) != nil {
		t.Fatalf("plan: %+v", r)
	}
	plan := envelope.Data
	if len(plan.Mappings) != 1 || plan.Mappings[0].Outcome != adapters.OutcomeExact || len(plan.Diagnostics) != 0 {
		t.Fatalf("expected one exact mapping without diagnostics: %+v", plan)
	}
	assertSameTree(t, before, h.snapshot(), "plan")
	if r := h.run("apply", "--target", "github-copilot", "--yes"); r.code != 0 {
		t.Fatalf("apply: %+v", r)
	}
	content := h.read(".github/instructions/api.instructions.md")
	if !strings.Contains(content, "applyTo: src/**/*.ts,src/**/*.tsx\n") {
		t.Fatalf("scope not expanded: %s", content)
	}
	if r := h.run("check", "--target", "github-copilot"); r.code != 0 {
		t.Fatalf("check: %+v", r)
	}
	// A fresh workspace proves the generated provider file carries the scope.
	back := newHarness(t)
	back.write(".github/instructions/api.instructions.md", content)
	if r := back.run("import", "--from", "github-copilot", "--targets", "claude"); r.code != 0 {
		t.Fatalf("reimport: %+v", r)
	}
	if r := back.run("apply", "--target", "claude", "--yes"); r.code != 0 {
		t.Fatalf("apply Claude: %+v", r)
	}
	content = back.read(".claude/rules/api.md")
	if !strings.Contains(content, "src/**/*.ts\n") || !strings.Contains(content, "src/**/*.tsx\n") || strings.Contains(content, "{ts,tsx}") {
		t.Fatalf("round trip changed the scope: %s", content)
	}
}

func TestOversizedBraceImportDoesNotWrite(t *testing.T) {
	for _, provider := range []struct{ name, path, header string }{
		{"claude", ".claude/rules/api.md", "paths"},
		{"github-copilot", ".github/instructions/api.instructions.md", "applyTo"},
		{"kiro", ".kiro/steering/api.md", "inclusion: fileMatch\nfileMatchPattern"},
	} {
		t.Run(provider.name, func(t *testing.T) {
			h := newHarness(t)
			pattern := "src/" + strings.Repeat("{a,b}", 11) + "/{ok,..}/**"
			h.write(provider.path, "---\n"+provider.header+": \""+pattern+"\"\n---\nUse explicit types.\n")
			before := h.snapshot()
			r := h.run("import", "--from", provider.name, "--json")
			var envelope struct {
				Diagnostics []diagnostics.Diagnostic `json:"diagnostics"`
			}
			if r.code != 1 || json.Unmarshal([]byte(r.stdout), &envelope) != nil {
				t.Fatalf("expected rejection: %+v", r)
			}
			found := false
			for _, d := range envelope.Diagnostics {
				if d.Code == diagnostics.GlobExpansionLimit && d.Severity == diagnostics.SeverityError && d.Path == provider.path {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing blocking expansion diagnostic: %s", r.stdout)
			}
			assertSameTree(t, before, h.snapshot(), "rejected import")
		})
	}
}
