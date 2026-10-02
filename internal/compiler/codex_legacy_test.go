package compiler_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/manifest"
	"github.com/alexvinola/stemma-cli/internal/profiles"
	"github.com/alexvinola/stemma-cli/internal/provenance"
	"github.com/alexvinola/stemma-cli/internal/store"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// legacyReason is the exact reason Stemma recorded for AGENTS.override.md
// before override precedence was modelled. Tests spell it out independently
// of the production constant.
const legacyReason = "AGENTS.override.md semantics are not modelled by Stemma; the file is preserved verbatim"

const legacyOverrideText = "# Local overrides\n\nPreserve this legacy guidance.\n"

// legacyBlock builds the one opaque block a pre-precedence importer wrote for
// an override: the whole file, the legacy reason, a full span.
func legacyBlock(path, content string) canonical.OpaqueBlock {
	return canonical.OpaqueBlock{
		ID: "opaque." + canonical.Slug(path), Provider: string(canonical.TargetCodex),
		SourcePath: path, Content: content,
		Span:   provenance.Span{ByteEnd: len(content), LineStart: 1, LineEnd: strings.Count(content, "\n") + 1},
		Reason: legacyReason, Hash: provenance.HashString(content), ReemitForRoundTrip: true,
	}
}

// legacyWorkspace writes files, builds the project a pre-precedence import
// produced (AGENTS.md files imported as active entities, each override kept
// as one legacy block, no new markers), and records the ownership that import
// verified: every source reproduced byte for byte.
func legacyWorkspace(t *testing.T, bases, overrides map[string]string) (*workspace.Workspace, canonical.Project, manifest.Manifest) {
	t.Helper()
	ctx := context.Background()
	var ws *workspace.Workspace
	project := canonical.NewProject("prj_legacy", "Legacy")
	project.Targets = []canonical.TargetFormat{canonical.TargetCodex}
	record := manifest.TargetRecord{}
	if len(bases) > 0 {
		// AGENTS.md alone imports exactly as it did before overrides were
		// modelled, including the ownership verified at import.
		var res compiler.ImportResult
		ws, res = importWorkspace(t, canonical.TargetCodex, bases)
		project = res.Project
		record = res.VerifiedTarget
	} else {
		ws = workspaceWith(t, workspace.DefaultLimits(), nil)
	}
	for p, content := range overrides {
		native, err := ws.Native(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(native), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(native, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		project.OpaqueBlocks = append(project.OpaqueBlocks, legacyBlock(p, content))
	}
	project.Sort()
	for key := range project.Extensions[string(canonical.TargetCodex)] {
		if strings.HasPrefix(key, "stemma.") {
			t.Fatalf("a legacy project has no %s marker", key)
		}
	}

	// The legacy import also verified each override, which it wrote back
	// verbatim.
	out := compileTarget(t, project, canonical.TargetCodex)
	for _, f := range out.Files {
		if want, ok := overrides[f.Path]; ok && want == f.Text {
			record.GeneratedFiles = append(record.GeneratedFiles, manifest.GeneratedRecord{
				Path: f.Path, Hash: provenance.HashString(want), Entities: f.Entities,
			})
		}
	}
	m := manifest.New()
	m.RecordImport(string(canonical.TargetCodex), nil, record)
	if _, err := store.SaveProject(ctx, ws, project, true); err != nil {
		t.Fatal(err)
	}
	return ws, project, m
}

// legacyPlanUnchanged asserts the plan of a legacy project is exactly what the
// pre-precedence release produced: the overrides written back verbatim, the
// instructions in AGENTS.md, and nothing to change, delete or report as lost.
func legacyPlanUnchanged(t *testing.T, ws *workspace.Workspace, project canonical.Project,
	m manifest.Manifest, generated map[string]string, when string) compiler.Plan {
	t.Helper()
	plan, err := compiler.BuildPlan(context.Background(), ws, project, compiler.PlanOptions{
		Target: canonical.TargetCodex, Profile: profiles.Default(canonical.TargetCodex), Manifest: m,
	})
	if err != nil {
		t.Fatalf("plan %s: %v", when, err)
	}
	seen := map[string]bool{}
	for _, c := range plan.Changes {
		seen[string(c.Path)] = true
		if c.Kind != compiler.ChangeUnchanged {
			t.Errorf("%s: %s is %s", when, c.Path, c.Kind)
		}
		if want, ok := generated[string(c.Path)]; ok && c.Content != want {
			t.Errorf("%s: %s = %q, want %q", when, c.Path, c.Content, want)
		}
	}
	for p := range generated {
		if !seen[p] {
			t.Errorf("%s: %s is not generated", when, p)
		}
	}
	for _, d := range plan.Diagnostics {
		switch d.Code {
		case diagnostics.OpaqueNotReemitted, diagnostics.DeleteProposed, diagnostics.ShadowingFileNotGenerated:
			t.Errorf("%s: unexpected %s at %s", when, d.Code, d.Path)
		}
	}
	for _, mp := range plan.Mappings {
		if mp.Outcome == adapters.OutcomeLossy || mp.Outcome == adapters.OutcomeBlocked {
			t.Errorf("%s: %s is %s: %s", when, mp.EntityID, mp.Outcome, mp.Explanation)
		}
		if mp.EntityType == canonical.EntityContext {
			for _, f := range mp.Files {
				if strings.HasSuffix(f, "AGENTS.override.md") {
					t.Errorf("%s: legacy entity %s moved to %s", when, mp.EntityID, f)
				}
			}
		}
	}
	return plan
}

func TestLegacyOverrideProjectsPlanAsBefore(t *testing.T) {
	const base = "# Project\n\n## Style\n\nUse tabs.\n"
	cases := []struct {
		name      string
		bases     map[string]string
		overrides map[string]string
		generated map[string]string
	}{
		{name: "root override only",
			overrides: map[string]string{"AGENTS.override.md": legacyOverrideText},
			generated: map[string]string{"AGENTS.override.md": legacyOverrideText}},
		{name: "nested override only",
			overrides: map[string]string{"svc/AGENTS.override.md": legacyOverrideText},
			generated: map[string]string{"svc/AGENTS.override.md": legacyOverrideText}},
		{name: "override and an empty base",
			bases:     map[string]string{"AGENTS.md": ""},
			overrides: map[string]string{"AGENTS.override.md": legacyOverrideText},
			generated: map[string]string{"AGENTS.override.md": legacyOverrideText}},
		{name: "nested override and a whitespace base without entities",
			bases:     map[string]string{"svc/AGENTS.md": "\n  \n"},
			overrides: map[string]string{"svc/AGENTS.override.md": legacyOverrideText},
			generated: map[string]string{"svc/AGENTS.override.md": legacyOverrideText}},
		{name: "override and a base with entities",
			bases:     map[string]string{"AGENTS.md": base},
			overrides: map[string]string{"AGENTS.override.md": legacyOverrideText},
			generated: map[string]string{"AGENTS.md": base, "AGENTS.override.md": legacyOverrideText}},
		{name: "nested override and a base with entities",
			bases:     map[string]string{"svc/AGENTS.md": base},
			overrides: map[string]string{"svc/AGENTS.override.md": legacyOverrideText},
			generated: map[string]string{"svc/AGENTS.md": base, "svc/AGENTS.override.md": legacyOverrideText}},
		{name: "empty legacy override",
			overrides: map[string]string{"AGENTS.override.md": "\n"},
			generated: map[string]string{"AGENTS.override.md": "\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ws, project, m := legacyWorkspace(t, tc.bases, tc.overrides)
			legacyPlanUnchanged(t, ws, project, m, tc.generated, "legacy project")

			loaded, err := store.LoadProject(ctx, ws)
			if err != nil {
				t.Fatal(err)
			}
			plan := legacyPlanUnchanged(t, ws, loaded, m, tc.generated, "after save and load")
			applied, err := compiler.Apply(ctx, ws, plan, compiler.ApplyOptions{Manifest: m})
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			legacyPlanUnchanged(t, ws, loaded, applied.Manifest, tc.generated, "after apply")

			// Without the original bytes, the same files are produced: each
			// override verbatim, and each AGENTS.md regenerated in place.
			out := compileTarget(t, loaded, canonical.TargetCodex)
			for p, want := range tc.generated {
				got, ok := fileText(out, p)
				switch {
				case !ok:
					t.Errorf("fresh export lacks %s", p)
				case strings.HasSuffix(p, "AGENTS.override.md") && got != want:
					t.Errorf("fresh export of %s = %q, want %q", p, got, want)
				case !strings.HasSuffix(p, "AGENTS.override.md") && !strings.Contains(got, "Use tabs."):
					t.Errorf("fresh export of %s lost its guidance: %q", p, got)
				}
			}
			if len(out.Files) != len(tc.generated) {
				t.Errorf("fresh export files = %d, want %d", len(out.Files), len(tc.generated))
			}
		})
	}
}

// Only the exact legacy shape is a whole file. A block with the legacy reason
// but a partial span, one that must not be re-emitted, one on an AGENTS.md
// path, or a current-format fragment is never written as a file.
func TestNonLegacyOverrideBlocksAreNotWholeFiles(t *testing.T) {
	partial := legacyBlock("AGENTS.override.md", legacyOverrideText)
	partial.Span = provenance.Span{ByteStart: 0, ByteEnd: 10, LineStart: 1, LineEnd: 1}
	notReemitted := legacyBlock("AGENTS.override.md", legacyOverrideText)
	notReemitted.ReemitForRoundTrip = false
	offset := legacyBlock("AGENTS.override.md", legacyOverrideText)
	offset.Span = provenance.Span{ByteStart: 3, ByteEnd: len(legacyOverrideText), LineStart: 1, LineEnd: 3}
	onBase := legacyBlock("AGENTS.md", legacyOverrideText)
	fragment := canonical.OpaqueBlock{
		ID: "opaque.agents-override-md", Provider: string(canonical.TargetCodex),
		SourcePath: "AGENTS.override.md", Content: "## First",
		Span:   provenance.Span{ByteStart: 10, ByteEnd: 19, LineStart: 3, LineEnd: 3},
		Reason: "heading with no content", Hash: provenance.HashString("## First"), ReemitForRoundTrip: true,
	}
	for name, blk := range map[string]canonical.OpaqueBlock{
		"legacy reason with a partial span":  partial,
		"legacy reason with an offset span":  offset,
		"legacy block not to be re-emitted":  notReemitted,
		"legacy reason on an AGENTS.md path": onBase,
		"current-format fragment":            fragment,
	} {
		t.Run(name, func(t *testing.T) {
			project := canonical.NewProject("prj_legacy", "Legacy")
			project.Targets = []canonical.TargetFormat{canonical.TargetCodex}
			project.OpaqueBlocks = []canonical.OpaqueBlock{blk}
			out := compileTarget(t, project, canonical.TargetCodex)
			if text, ok := fileText(out, blk.SourcePath); ok {
				t.Errorf("%s was written as a file: %q", blk.SourcePath, text)
			}
			if m := mappingOf(t, out, blk.ID); m.Outcome != adapters.OutcomeLossy {
				t.Errorf("mapping = %+v", m)
			}
		})
	}
}
