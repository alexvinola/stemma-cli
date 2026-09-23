package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/cli"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/manifest"
)

// TestCodexOverrideRoundTripThroughTheCLI imports a repository where
// AGENTS.override.md shadows AGENTS.md, edits the effective guidance, and
// applies it: the edit goes back to the override, the shadowed file is never
// touched, and a re-plan is a no-op.
func TestCodexOverrideRoundTripThroughTheCLI(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("codex/override")
	before := h.snapshot()
	if res := h.run("import", "--from", "codex", "--targets", "codex"); res.code != cli.ExitOK {
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
	for path := range before {
		if hash, ok := m.Tracked("codex", path); !ok || hash != hashOf(before[path]) {
			t.Errorf("import did not verify %s", path)
		}
	}
	if res := h.run("check", "--all"); res.code != cli.ExitOK {
		t.Fatalf("check right after import: %+v", res)
	}

	h.write(".stemma/context/testing.md", h.read(".stemma/context/testing.md")+"\nCANONICAL-EDIT: Run the linter.\n")
	if res := h.run("plan", "--target", "codex", "--output-plan", "plan.json"); res.code != cli.ExitOK {
		t.Fatalf("plan: %+v", res)
	}
	plan, err := compiler.UnmarshalPlan([]byte(h.read("plan.json")))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]compiler.ChangeKind{}
	for _, c := range plan.Changes {
		kinds[string(c.Path)] = c.Kind
	}
	if kinds["AGENTS.override.md"] != compiler.ChangeUpdate {
		t.Fatalf("the edit must update AGENTS.override.md: %+v", kinds)
	}
	for path := range before {
		if path != "AGENTS.override.md" && kinds[path] != compiler.ChangeUnchanged {
			t.Errorf("%s: %s, want unchanged", path, kinds[path])
		}
	}
	if res := h.run("apply", "--plan", "plan.json", "--yes"); res.code != cli.ExitOK {
		t.Fatalf("apply: %+v", res)
	}
	if !strings.Contains(h.read("AGENTS.override.md"), "CANONICAL-EDIT") {
		t.Fatal("the edit did not reach AGENTS.override.md")
	}
	for path, body := range before {
		if path != "AGENTS.override.md" && h.read(path) != body {
			t.Errorf("%s changed", path)
		}
	}
	if strings.Contains(h.read("AGENTS.override.md"), "make test-legacy") {
		t.Error("shadowed guidance leaked into the effective file")
	}
	if res := h.run("check", "--all"); res.code != cli.ExitOK {
		t.Fatalf("check after apply: %+v", res)
	}
}

// An override with nothing to model (front matter and headings only) is kept
// as the whole file: import, check and apply leave both files untouched.
func TestCodexOverrideWithoutGuidanceThroughTheCLI(t *testing.T) {
	h := newHarness(t)
	h.write("svc/AGENTS.md", "# Base\n\nINACTIVE BASE\n")
	h.write("svc/AGENTS.override.md", "---\ncustom: value\n---\n\n# Title\n\n## First\n")
	before := h.snapshot()
	if res := h.run("import", "--from", "codex", "--targets", "codex"); res.code != cli.ExitOK {
		t.Fatalf("import: %+v", res)
	}
	if res := h.run("check", "--all"); res.code != cli.ExitOK {
		t.Fatalf("check after import: %+v", res)
	}
	if res := h.run("apply", "--all", "--yes"); res.code != cli.ExitOK {
		t.Fatalf("apply: %+v", res)
	}
	for path, body := range before {
		if h.read(path) != body {
			t.Errorf("%s changed", path)
		}
	}
	if res := h.run("check", "--all"); res.code != cli.ExitOK {
		t.Fatalf("check after apply: %+v", res)
	}
}
