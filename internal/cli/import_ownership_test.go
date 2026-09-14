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

func TestReplacingImportRevokesPreviousProjectOwnership(t *testing.T) {
	for _, applied := range []bool{false, true} {
		name := "without-apply"
		if applied {
			name = "after-apply"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.fromFixture("copilot/basic")
			if res := h.run("import", "--from", "github-copilot"); res.code != cli.ExitOK {
				t.Fatalf("first import: %+v", res)
			}
			if applied {
				if res := h.run("apply", "--all", "--yes"); res.code != cli.ExitOK {
					t.Fatalf("first apply: %+v", res)
				}
			}
			source := ".github/copilot-instructions.md"
			original := h.read(source)
			h.write("AGENTS.md", "# Codex\n\n## Agents\n\nReview changes before merge.\n")
			res := h.run("import", "--from", "codex", "--overwrite", "--targets", "github-copilot,codex", "--json")
			if res.code != cli.ExitOK || !strings.Contains(res.stdout, "STEMMA4303_IMPORT_OWNERSHIP_REVOKED") || !strings.Contains(res.stdout, source) {
				t.Fatalf("replacement must report revoked ownership: %+v", res)
			}
			if h.read(source) != original {
				t.Fatal("replacement import modified the previous provider source")
			}
			var m manifest.Manifest
			if err := json.Unmarshal([]byte(h.read(".stemma/manifest.json")), &m); err != nil {
				t.Fatal(err)
			}
			if _, ok := m.Tracked("github-copilot", source); ok {
				t.Fatal("ownership survived project replacement")
			}
			if _, ok := m.Tracked("codex", "AGENTS.md"); !ok {
				t.Fatal("new import did not establish verified ownership")
			}
			// Even applying the new source target first cannot restore authority
			// over the previous target via the manifest's project hash.
			if res := h.run("apply", "--target", "codex", "--yes"); res.code != cli.ExitOK {
				t.Fatalf("new source target: %+v", res)
			}
			before := h.snapshot()
			for _, args := range [][]string{
				{"plan", "--target", "github-copilot"},
				{"apply", "--all", "--yes"},
			} {
				res := h.run(args...)
				if res.code != cli.ExitDiagnostics || !strings.Contains(res.stderr+res.stdout, "STEMMA4301_UNTRACKED_DESTINATION") {
					t.Fatalf("old destination must be a conflict: %+v", res)
				}
				assertSameTree(t, before, h.snapshot(), "revoked destination")
			}
			// Repeating the new import must not revive revoked records.
			if res := h.run("import", "--from", "codex", "--overwrite", "--targets", "github-copilot,codex"); res.code != cli.ExitOK {
				t.Fatalf("repeat import: %+v", res)
			}
			if res := h.run("apply", "--target", "github-copilot", "--yes"); res.code != cli.ExitDiagnostics {
				t.Fatalf("repeat import revived ownership: %+v", res)
			}
			if res := h.run("apply", "--all", "--yes", "--adopt-untracked"); res.code != cli.ExitOK {
				t.Fatalf("explicit adoption: %+v", res)
			}
			if !strings.Contains(h.read(source), "Review changes before merge.") {
				t.Fatal("explicit adoption did not apply the replacement project")
			}
			if res := h.run("check", "--all"); res.code != cli.ExitOK {
				t.Fatalf("check after adoption: %+v", res)
			}
		})
	}
}

func TestIdenticalReimportPreservesOtherTargetsOwnership(t *testing.T) {
	h := newHarness(t)
	h.fromFixture("copilot/basic")
	args := []string{"import", "--from", "github-copilot", "--targets", "github-copilot,codex"}
	if res := h.run(args...); res.code != cli.ExitOK {
		t.Fatalf("import: %+v", res)
	}
	if res := h.run("apply", "--all", "--yes"); res.code != cli.ExitOK {
		t.Fatalf("apply: %+v", res)
	}
	res := h.run(append(args, "--overwrite")...)
	if res.code != cli.ExitOK || strings.Contains(res.stdout, "STEMMA4303") {
		t.Fatalf("identical reimport revoked ownership: %+v", res)
	}
	var m manifest.Manifest
	if err := json.Unmarshal([]byte(h.read(".stemma/manifest.json")), &m); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ target, path string }{
		{"github-copilot", ".github/copilot-instructions.md"}, {"codex", "AGENTS.md"},
	} {
		if _, ok := m.Tracked(tc.target, tc.path); !ok {
			t.Fatalf("identical reimport lost %s ownership", tc.path)
		}
	}
	h.write(".stemma/context/architecture.md", h.read(".stemma/context/architecture.md")+"\nCANONICAL-EDIT after reimport.\n")
	if res := h.run("apply", "--all", "--yes"); res.code != cli.ExitOK {
		t.Fatalf("canonical edit after reimport: %+v", res)
	}
	for _, path := range []string{".github/copilot-instructions.md", "AGENTS.md"} {
		if !strings.Contains(h.read(path), "CANONICAL-EDIT after reimport.") {
			t.Fatalf("canonical edit missing from %s", path)
		}
	}
}

func TestSameProviderReplacementRevokesOtherTargetsOwnership(t *testing.T) {
	for _, changed := range []string{"source", "canonical", "unreadable-project"} {
		t.Run(changed, func(t *testing.T) {
			h := newHarness(t)
			h.fromFixture("copilot/basic")
			args := []string{"import", "--from", "github-copilot", "--targets", "github-copilot,codex"}
			if res := h.run(args...); res.code != cli.ExitOK {
				t.Fatalf("import: %+v", res)
			}
			if res := h.run("apply", "--all", "--yes"); res.code != cli.ExitOK {
				t.Fatalf("apply: %+v", res)
			}
			switch changed {
			case "source":
				path := ".github/copilot-instructions.md"
				h.write(path, h.read(path)+"\nNew source instructions.\n")
			case "canonical":
				// The manifest still describes the original import. Comparing
				// against it would miss replacement of these unapplied edits.
				path := ".stemma/context/architecture.md"
				h.write(path, h.read(path)+"\nCanonical instructions to be replaced.\n")
			case "unreadable-project":
				h.write(".stemma/project.json", "{")
			}
			before := h.snapshot()
			res := h.run(append(args, "--overwrite")...)
			if res.code != cli.ExitOK || !strings.Contains(res.stdout, "STEMMA4303_IMPORT_OWNERSHIP_REVOKED") || !strings.Contains(res.stdout, "AGENTS.md") {
				t.Fatalf("same-provider replacement must report revoked ownership: %+v", res)
			}
			var m manifest.Manifest
			if err := json.Unmarshal([]byte(h.read(".stemma/manifest.json")), &m); err != nil {
				t.Fatal(err)
			}
			if _, ok := m.Tracked("codex", "AGENTS.md"); ok {
				t.Fatal("replacement retained the other target's ownership")
			}
			if _, ok := m.Tracked("github-copilot", ".github/copilot-instructions.md"); !ok {
				t.Fatal("replacement did not re-verify its own source")
			}
			for path, content := range before {
				if !strings.HasPrefix(path, ".stemma/") && h.read(path) != content {
					t.Fatalf("replacement modified provider file %s", path)
				}
			}
		})
	}
}
