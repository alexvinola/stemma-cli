package compiler_test

import (
	"context"
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
)

// Text that only occurs in the AGENTS.md files of testdata/codex/override that
// an AGENTS.override.md shadows, and text only its effective files contain.
var (
	shadowedText = map[string]string{
		"AGENTS.md":                   "make test-legacy",
		"services/payments/AGENTS.md": "shared `npm test` runner",
		"docs/AGENTS.md":              "British English",
	}
	effectiveText = []string{"go test ./...", "make test-payments", "make index"}
)

func mappingOf(t *testing.T, out compiler.CompileResult, id string) adapters.ProjectionMapping {
	t.Helper()
	for _, m := range out.Mappings {
		if m.EntityID == id {
			return m
		}
	}
	t.Fatalf("no mapping for %s", id)
	return adapters.ProjectionMapping{}
}

func TestShadowedAgentsMDIsNeverActiveGuidance(t *testing.T) {
	_, project := importFixture(t, "codex/override", canonical.TargetCodex)

	// Exactly one inactive opaque block per shadowed file, nothing else.
	for path, marker := range shadowedText {
		count := 0
		for _, blk := range project.OpaqueBlocks {
			if strings.Contains(blk.Content, marker) {
				count++
				if blk.SourcePath != path || !strings.HasPrefix(blk.Reason, "inactive under Codex precedence") {
					t.Errorf("%s preserved as %+v", path, blk)
				}
			}
		}
		if count != 1 {
			t.Errorf("%s preserved %d times, want exactly once", path, count)
		}
		if strings.Contains(allText(canonical.Project{
			ContextDocuments: project.ContextDocuments, Rules: project.Rules, Skills: project.Skills,
			Procedures: project.Procedures, Agents: project.Agents,
		}), marker) {
			t.Errorf("shadowed %s became a canonical entity", path)
		}
	}

	for _, target := range []canonical.TargetFormat{
		canonical.TargetClaude, canonical.TargetCopilot, canonical.TargetKiro, canonical.TargetCodex,
	} {
		out := compileTarget(t, project, target)
		all := ""
		for _, f := range out.Files {
			all += f.Text
			for path, marker := range shadowedText {
				if strings.Contains(f.Text, marker) && !(target == canonical.TargetCodex && f.Path == path) {
					t.Errorf("%s: shadowed %s content appears in %s", target, path, f.Path)
				}
			}
		}
		for _, text := range effectiveText {
			if !strings.Contains(all, text) {
				t.Errorf("%s: effective guidance %q is missing", target, text)
			}
		}
		for _, blk := range project.OpaqueBlocks {
			m := mappingOf(t, out, blk.ID)
			if target != canonical.TargetCodex && (m.Outcome != adapters.OutcomeSkipped || len(m.Files) != 0) {
				t.Errorf("%s: %s mapped as %+v", target, blk.ID, m)
			}
		}
	}
}

// A canonical project that was not imported from Codex has no overrides, so
// only AGENTS.md files are written, as before.
func TestCanonicalProjectExportsOnlyAgentsMD(t *testing.T) {
	project := mustReadFixtureProject(t, "canonical/exclude-and-disabled")
	out := compileTarget(t, project, canonical.TargetCodex)
	for _, f := range out.Files {
		if strings.HasSuffix(f.Path, "AGENTS.override.md") {
			t.Errorf("unexpected override file %s", f.Path)
		}
	}
	if _, ok := fileText(out, "AGENTS.md"); !ok {
		t.Error("the root AGENTS.md is missing")
	}
}

// New content for a directory whose override is empty is written to the
// override, replacing the empty file; the shadowed AGENTS.md stays verbatim.
func TestNewContentReplacesAnEmptyOverride(t *testing.T) {
	_, project := importFixture(t, "codex/override", canonical.TargetCodex)
	project.ContextDocuments = append(project.ContextDocuments, canonical.ContextDocument{
		ID: "context.docs-style", Title: "Docs style", Kind: canonical.KindOther,
		Audience: canonical.AudienceAgent, Content: "Use sentence case.",
		Activation: canonical.PathScoped([]string{"docs/**"}, nil),
	})
	project.Sort()
	out := compileTarget(t, project, canonical.TargetCodex)
	text, ok := fileText(out, "docs/AGENTS.override.md")
	if !ok || !strings.Contains(text, "Use sentence case.") {
		t.Fatalf("docs/AGENTS.override.md = %q, %v", text, ok)
	}
	if text, _ := fileText(out, "docs/AGENTS.md"); text != "# Documentation\n\nWrite in British English.\n" {
		t.Errorf("shadowed docs/AGENTS.md = %q", text)
	}
	if m := mappingOf(t, out, "opaque.docs-agents-override-md"); m.Outcome != adapters.OutcomeAdapted ||
		strings.Join(m.Files, ",") != "docs/AGENTS.override.md" {
		t.Errorf("empty override mapping = %+v", m)
	}
	if m := mappingOf(t, out, "context.docs-style"); m.Outcome != adapters.OutcomeExact {
		t.Errorf("new content mapping = %+v", m)
	}
}

// A profile pin into a directory whose AGENTS.md is shadowed must write to
// the override, whatever spelling the pin uses; otherwise the content would
// either collide with the preserved file or be inactive.
func TestPinnedDirectoryUsesTheEffectiveFile(t *testing.T) {
	_, project := importFixture(t, "codex/override", canonical.TargetCodex)
	project.ContextDocuments = append(project.ContextDocuments, canonical.ContextDocument{
		ID: "context.pinned", Title: "Pinned", Kind: canonical.KindOther,
		Audience: canonical.AudienceAgent, Content: "Pinned guidance.",
		Activation: canonical.PathScoped([]string{"docs/**/*.md"}, nil),
	})
	project.Sort()
	profile := profiles.Default(canonical.TargetCodex)
	profile.Overrides["context.pinned"] = profiles.Override{Directory: "./docs/"}
	out, err := compiler.Compile(context.Background(), project, compiler.CompileOptions{
		Target: canonical.TargetCodex, Profile: profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.HasBlocking(out.Diagnostics) {
		t.Fatalf("blocking diagnostics: %+v", out.Diagnostics)
	}
	if m := mappingOf(t, out, "context.pinned"); strings.Join(m.Files, ",") != "docs/AGENTS.override.md" {
		t.Errorf("pinned content went to %v", m.Files)
	}
	if text, _ := fileText(out, "docs/AGENTS.md"); text != "# Documentation\n\nWrite in British English.\n" {
		t.Errorf("shadowed docs/AGENTS.md = %q", text)
	}
}

// When nothing is generated at the override path any more, the preserved
// shadowed file is still written back verbatim, but the loss of the file that
// kept it inactive is reported, never silent.
func TestShadowedFileWithoutGeneratedOverrideIsReported(t *testing.T) {
	_, project := importFixture(t, "codex/override", canonical.TargetCodex)
	var kept []canonical.ContextDocument
	for _, d := range project.ContextDocuments {
		if d.Provenance.SourcePath != "AGENTS.override.md" {
			kept = append(kept, d)
		}
	}
	project.ContextDocuments = kept
	out := compileTarget(t, project, canonical.TargetCodex)
	if _, ok := fileText(out, "AGENTS.override.md"); ok {
		t.Fatal("no root override should be generated")
	}
	if text, _ := fileText(out, "AGENTS.md"); !strings.Contains(text, "make test-legacy") {
		t.Errorf("shadowed AGENTS.md was not written back verbatim: %q", text)
	}
	d, ok := findDiagnostic(out.Diagnostics, diagnostics.ShadowingFileNotGenerated)
	if !ok || d.Path != "AGENTS.md" || d.Severity != diagnostics.SeverityWarning {
		t.Fatalf("missing STEMMA1205 for AGENTS.md: %+v", out.Diagnostics)
	}
	m := mappingOf(t, out, "opaque.agents-md")
	if m.Outcome != adapters.OutcomeLossy || len(m.Diagnostics) != 1 || m.Diagnostics[0] != d.Fingerprint {
		t.Errorf("shadowed mapping = %+v", m)
	}
	// Directories whose override is still generated are unaffected.
	if m := mappingOf(t, out, "opaque.services-payments-agents-md"); m.Outcome != adapters.OutcomeExact {
		t.Errorf("payments shadowed mapping = %+v", m)
	}
}

// Import, apply an edit made to the effective content, then re-plan: the edit
// lands in the override, the shadowed file keeps its bytes, and nothing is
// left to change.
func TestOverrideEditApplyThenReplanIsNoOp(t *testing.T) {
	ctx := context.Background()
	ws := materialize(t, filepath.Join(testdataDir, "codex", "override", "input"))
	imported, err := compiler.Import(ctx, ws, compiler.ImportOptions{
		Format: canonical.TargetCodex, ProjectID: "prj_fixture", ProjectName: "Fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.VerifiedTarget.GeneratedFiles) != 7 {
		t.Fatalf("import verified %+v, want all seven sources", imported.VerifiedTarget.GeneratedFiles)
	}
	project := imported.Project
	for i := range project.ContextDocuments {
		if project.ContextDocuments[i].Provenance.SourcePath == "services/payments/AGENTS.override.md" {
			project.ContextDocuments[i].Content += "\n\nNEW PAYMENTS RULE"
		}
	}
	m := manifest.New()
	m.RecordImport(string(canonical.TargetCodex), imported.Sources, imported.VerifiedTarget)
	plan, err := compiler.BuildPlan(ctx, ws, project, compiler.PlanOptions{
		Target: canonical.TargetCodex, Profile: profiles.Default(canonical.TargetCodex), Manifest: m,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range plan.Changes {
		switch c.Path {
		case "services/payments/AGENTS.override.md":
			if !strings.Contains(c.Content, "NEW PAYMENTS RULE") {
				t.Errorf("edit not carried: %+v", c)
			}
		default:
			if c.Kind != compiler.ChangeUnchanged {
				t.Errorf("%s: %s, want unchanged", c.Path, c.Kind)
			}
		}
	}
	res, err := compiler.Apply(ctx, ws, plan, compiler.ApplyOptions{Manifest: m})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	again, err := compiler.BuildPlan(ctx, ws, project, compiler.PlanOptions{
		Target: canonical.TargetCodex, Profile: profiles.Default(canonical.TargetCodex), Manifest: res.Manifest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.HasChanges() {
		t.Errorf("re-plan after apply proposed changes: %+v", again.Changes)
	}
}

// A project imported before override precedence was modelled holds the whole
// override as an opaque block next to active AGENTS.md entities. Its export
// is unchanged until the repository is imported again: nothing is merged or
// moved between the two files.
func TestLegacyOverrideLayoutIsUnchanged(t *testing.T) {
	_, project := importFixture(t, "codex/nested", canonical.TargetCodex)
	const legacy = "# Local overrides\n\nThis file uses semantics Stemma does not model.\n"
	project.OpaqueBlocks = append(project.OpaqueBlocks, canonical.OpaqueBlock{
		ID: "opaque.agents-override-md", Provider: string(canonical.TargetCodex),
		SourcePath: "AGENTS.override.md", Content: legacy,
		Reason: "AGENTS.override.md semantics are not modelled by Stemma; the file is preserved verbatim",
		Hash:   provenance.HashString(legacy), ReemitForRoundTrip: true,
	})
	out := compileTarget(t, project, canonical.TargetCodex)
	if text, _ := fileText(out, "AGENTS.override.md"); text != legacy {
		t.Errorf("legacy override = %q", text)
	}
	if text, _ := fileText(out, "AGENTS.md"); !strings.Contains(text, "Handlers call services") {
		t.Errorf("legacy AGENTS.md = %q", text)
	}
	if m := mappingOf(t, out, "context.architecture"); strings.Join(m.Files, ",") != "AGENTS.md" {
		t.Errorf("legacy entity moved to %v", m.Files)
	}
	if len(out.Diagnostics) != 0 {
		t.Errorf("unexpected diagnostics: %+v", out.Diagnostics)
	}
}
