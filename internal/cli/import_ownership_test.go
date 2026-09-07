package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/cli"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/manifest"
)

func TestImportThenEditCanApplyToSourceProvider(t *testing.T) {
	for _, tc := range []struct{ fixture, target, source, entity string }{
		{"copilot/basic", "github-copilot", ".github/copilot-instructions.md", ".stemma/context/architecture.md"},
		{"claude/basic", "claude", "CLAUDE.md", ".stemma/context/architecture.md"},
		{"codex/nested", "codex", "AGENTS.md", ".stemma/context/architecture.md"},
		{"kiro/steering", "kiro", ".kiro/steering/product.md", ".stemma/context/product.md"},
	} {
		for _, modifiedSource := range []bool{false, true} {
			name := tc.target + "/canonical-edit"
			if modifiedSource {
				name = tc.target + "/source-edit"
			}
			t.Run(name, func(t *testing.T) {
				h := newHarness(t)
				h.fromFixture(tc.fixture)
				before := h.snapshot()
				res := h.run("import", "--from", tc.target, "--targets", tc.target)
				if res.code != cli.ExitOK {
					t.Fatalf("import: %+v", res)
				}
				for path, body := range before {
					if h.read(path) != body {
						t.Fatalf("import rewrote %s", path)
					}
				}
				var m manifest.Manifest
				if err := json.Unmarshal([]byte(h.read(".stemma/manifest.json")), &m); err != nil {
					t.Fatal(err)
				}
				if hash, ok := m.Tracked(tc.target, tc.source); !ok || hash != hashOf(h.read(tc.source)) {
					t.Fatalf("import did not record verified ownership of %s", tc.source)
				}
				h.write(tc.entity, h.read(tc.entity)+"\nCANONICAL-EDIT: Run the linter.\n")
				if modifiedSource {
					h.write(tc.source, h.read(tc.source)+"\nSOURCE-EDIT: Keep this user edit.\n")
					before := h.snapshot()
					res := h.run("apply", "--all", "--yes")
					if res.code != cli.ExitDiagnostics || strings.Contains(res.stderr, "--adopt-untracked") {
						t.Fatalf("modified source must remain a conflict: %+v", res)
					}
					assertSameTree(t, before, h.snapshot(), "modified imported source")
					return
				}
				if res := h.run("plan", "--target", tc.target, "--output-plan", "plan.json"); res.code != cli.ExitOK {
					t.Fatalf("plan after canonical edit: %+v", res)
				}
				plan, err := compiler.UnmarshalPlan([]byte(h.read("plan.json")))
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, c := range plan.Changes {
					if c.Path == tc.source {
						found = c.Kind == compiler.ChangeUpdate
					}
				}
				if !found {
					t.Fatalf("source must be an update: %+v", plan.Changes)
				}
				res = h.run("apply", "--plan", "plan.json", "--yes")
				if res.code != cli.ExitOK {
					t.Fatalf("first apply after edit: %+v", res)
				}
				if !strings.Contains(h.read(tc.source), "CANONICAL-EDIT") {
					t.Fatal("canonical edit missing from output")
				}
				if res := h.run("check", "--all"); res.code != cli.ExitOK {
					t.Fatalf("check: %+v", res)
				}
			})
		}
	}
}

func TestImportReportsUnverifiedSourceBeforeApply(t *testing.T) {
	h := newHarness(t)
	// An empty instruction file is valid input but produces no native output.
	h.write("CLAUDE.md", "\n\n")
	res := h.run("import", "--from", "claude", "--json")
	if res.code != cli.ExitOK {
		t.Fatalf("import: %+v", res)
	}
	if !strings.Contains(res.stdout, "STEMMA4302_IMPORT_ROUND_TRIP_UNVERIFIED") {
		t.Fatalf("import must report the unverified source immediately: %+v", res)
	}
	if strings.Contains(res.stdout, "--adopt-untracked") {
		t.Fatal("import must not recommend bypassing fidelity")
	}
	var m manifest.Manifest
	if err := json.Unmarshal([]byte(h.read(".stemma/manifest.json")), &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Tracked("claude", "CLAUDE.md"); ok {
		t.Fatal("unverified source was claimed")
	}
	h.write(".stemma/context/guide.md", "---\ntitle: Guide\nkind: other\naudience: agent\nactivation:\n  type: always\n---\n\nNew instructions.\n")
	before := h.snapshot()
	for _, args := range [][]string{
		{"apply", "--target", "claude", "--yes"},
		{"apply", "--target", "claude", "--yes", "--adopt-untracked"},
	} {
		res := h.run(args...)
		if res.code != cli.ExitDiagnostics || strings.Contains(res.stderr, "--adopt-untracked") {
			t.Fatalf("unverified imported source must remain a conflict: %+v", res)
		}
		assertSameTree(t, before, h.snapshot(), "unverified imported source")
	}
}
