package compiler_test

import (
	"context"
	"path"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/manifest"
	"github.com/alexvinola/stemma-cli/internal/profiles"
	"github.com/alexvinola/stemma-cli/internal/store"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// Instruction files with nothing to model: no body text, only headings
// without content, or front matter and such headings. Codex still reads them
// (they are not empty), so each must come back byte for byte as a whole file.
var unmodelledInstructions = []string{
	"# Title\n",
	"# Title\n\n## First\n\n## Second\n",
	"---\ncustom: value\n---\n\n# Title\n\n## First\n",
	"# Title\r\n\r\n## First\r\n",
}

const inactiveBase = "# Base\n\nINACTIVE BASE\n"

// TestUnmodelledInstructionsRoundTripAsWholeFiles is the regression for
// override files that produced only opaque fragments: the fragments were
// written back as if they were the file (losing the H1, front matter and
// final newline), or, with several of them, no override was written at all.
func TestUnmodelledInstructionsRoundTripAsWholeFiles(t *testing.T) {
	type layoutCase struct {
		name    string
		file    string // path of the file with nothing to model
		sibling string // shadowed AGENTS.md, or "" for none
	}
	layouts := []layoutCase{
		{"root override with base", "AGENTS.override.md", "AGENTS.md"},
		{"nested override with base", "svc/AGENTS.override.md", "svc/AGENTS.md"},
		{"root override alone", "AGENTS.override.md", ""},
		{"nested override alone", "svc/AGENTS.override.md", ""},
		{"root AGENTS.md alone", "AGENTS.md", ""},
		{"nested AGENTS.md alone", "svc/AGENTS.md", ""},
	}
	for _, lc := range layouts {
		for _, text := range unmodelledInstructions {
			t.Run(lc.name+"/"+strings.ReplaceAll(text, "\n", "_"), func(t *testing.T) {
				ctx := context.Background()
				files := map[string]string{lc.file: text}
				if lc.sibling != "" {
					files[lc.sibling] = inactiveBase
				}
				ws, res := importWorkspace(t, canonical.TargetCodex, files)

				// Import: one whole-file block, no entities, no duplicated
				// front matter, and verified ownership of every source.
				assertWholeFilePreserved(t, res.Project, lc.file, text)
				verified := map[string]bool{}
				for _, g := range res.VerifiedTarget.GeneratedFiles {
					verified[g.Path] = true
				}
				for p := range files {
					if !verified[p] {
						t.Errorf("import did not verify %s: %+v", p, res.Diagnostics)
					}
				}

				m := manifest.New()
				m.RecordImport(string(canonical.TargetCodex), res.Sources, res.VerifiedTarget)
				planNoChanges(t, ctx, ws, res.Project, m, files, "after import")

				// Save and load the canonical project, then plan again.
				if _, err := store.SaveProject(ctx, ws, res.Project, true); err != nil {
					t.Fatal(err)
				}
				loaded, err := store.LoadProject(ctx, ws)
				if err != nil {
					t.Fatal(err)
				}
				plan := planNoChanges(t, ctx, ws, loaded, m, files, "after save and load")

				// Apply, then re-plan: nothing is pending.
				applied, err := compiler.Apply(ctx, ws, plan, compiler.ApplyOptions{Manifest: m})
				if err != nil {
					t.Fatalf("apply: %v", err)
				}
				planNoChanges(t, ctx, ws, loaded, applied.Manifest, files, "after apply")

				// A fresh export (no original bytes) still writes every file.
				out := compileTarget(t, loaded, canonical.TargetCodex)
				for p, want := range files {
					if got, ok := fileText(out, p); !ok || got != want {
						t.Errorf("fresh export of %s = %q, %v; want %q", p, got, ok, want)
					}
				}
				for _, target := range []canonical.TargetFormat{
					canonical.TargetClaude, canonical.TargetCopilot, canonical.TargetKiro,
				} {
					for _, f := range compileTarget(t, loaded, target).Files {
						if strings.Contains(f.Text, "INACTIVE BASE") {
							t.Errorf("%s: shadowed content in %s", target, f.Path)
						}
					}
				}
			})
		}
	}
}

func assertWholeFilePreserved(t *testing.T, p canonical.Project, file, text string) {
	t.Helper()
	if len(p.ContextDocuments) != 0 {
		t.Errorf("entities imported from a file with nothing to model: %+v", p.ContextDocuments)
	}
	var blocks []canonical.OpaqueBlock
	for _, blk := range p.OpaqueBlocks {
		if blk.SourcePath == file {
			blocks = append(blocks, blk)
		}
	}
	if len(blocks) != 1 || blocks[0].Content != text {
		t.Fatalf("%s preserved as %+v, want one block with the whole file", file, blocks)
	}
	if id, _ := p.Extensions.GetString(string(canonical.TargetCodex), "stemma.preservedFile."+file); id != blocks[0].ID {
		t.Errorf("preserved-file marker = %q, want %q", id, blocks[0].ID)
	}
	for key := range p.Extensions[string(canonical.TargetCodex)] {
		if strings.HasPrefix(key, "frontMatter."+file+".") {
			t.Errorf("front matter duplicated as extension %s", key)
		}
	}
}

func planNoChanges(
	t *testing.T, ctx context.Context, ws *workspace.Workspace, project canonical.Project,
	m manifest.Manifest, files map[string]string, when string,
) compiler.Plan {
	t.Helper()
	plan, err := compiler.BuildPlan(ctx, ws, project, compiler.PlanOptions{
		Target: canonical.TargetCodex, Profile: profiles.Default(canonical.TargetCodex), Manifest: m,
	})
	if err != nil {
		t.Fatalf("plan %s: %v", when, err)
	}
	seen := map[string]bool{}
	for _, c := range plan.Changes {
		p := string(c.Path)
		seen[p] = true
		if c.Kind != compiler.ChangeUnchanged {
			t.Errorf("%s: %s is %s", when, p, c.Kind)
		}
		if want, ok := files[p]; ok && c.Content != want {
			t.Errorf("%s: %s bytes = %q, want %q", when, p, c.Content, want)
		}
	}
	for p := range files {
		if !seen[p] {
			t.Errorf("%s: %s missing from the plan", when, p)
		}
	}
	for _, d := range plan.Diagnostics {
		if d.Code == diagnostics.ShadowingFileNotGenerated || d.Code == diagnostics.OpaqueNotReemitted {
			t.Errorf("%s: unexpected %s at %s", when, d.Code, d.Path)
		}
	}
	return plan
}

// A heading without content inside a file that also has guidance is a
// fragment. If every entity of that file is removed, the fragment is not
// written as if it were the whole file: its loss is reported instead.
func TestOpaqueFragmentIsNeverWrittenAsAWholeFile(t *testing.T) {
	for _, file := range []string{"AGENTS.override.md", "AGENTS.md"} {
		t.Run(file, func(t *testing.T) {
			files := map[string]string{file: "# Title\n\n## Rules\n\nDo things.\n\n## Empty\n"}
			if file == "AGENTS.override.md" {
				files["AGENTS.md"] = inactiveBase
			}
			ws, res := importWorkspace(t, canonical.TargetCodex, files)
			m := manifest.New()
			m.RecordImport(string(canonical.TargetCodex), res.Sources, res.VerifiedTarget)
			planNoChanges(t, context.Background(), ws, res.Project, m, files, "unchanged")

			project := res.Project
			project.ContextDocuments = nil
			out := compileTarget(t, project, canonical.TargetCodex)
			if text, ok := fileText(out, file); ok {
				t.Errorf("a fragment was written as %s: %q", file, text)
			}
			var fragment string
			for _, blk := range project.OpaqueBlocks {
				if blk.SourcePath == file {
					fragment = blk.ID
				}
			}
			m2 := mappingOf(t, out, fragment)
			if m2.Outcome != adapters.OutcomeLossy {
				t.Errorf("fragment mapping = %+v", m2)
			}
			if _, ok := findDiagnostic(out.Diagnostics, diagnostics.OpaqueNotReemitted); !ok {
				t.Errorf("missing STEMMA3501: %+v", out.Diagnostics)
			}
			if path.Base(file) == "AGENTS.override.md" {
				if _, ok := findDiagnostic(out.Diagnostics, diagnostics.ShadowingFileNotGenerated); !ok {
					t.Errorf("missing STEMMA1205: %+v", out.Diagnostics)
				}
			}
		})
	}
}
