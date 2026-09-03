package compiler_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexvinola/stemma/internal/adapters"
	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/compiler"
	"github.com/alexvinola/stemma/internal/diagnostics"
	"github.com/alexvinola/stemma/internal/manifest"
	"github.com/alexvinola/stemma/internal/profiles"
	"github.com/alexvinola/stemma/internal/workspace"
)

// assertProjectionInvariants checks the guarantees that must hold for every
// compilation, whatever the input.
func assertProjectionInvariants(t *testing.T, project canonical.Project, out compiler.CompileResult) {
	t.Helper()

	// 1. Exactly one projection outcome per entity and per opaque block.
	seen := map[string]int{}
	for _, m := range out.Mappings {
		if !adapters.KnownOutcome(m.Outcome) {
			t.Errorf("entity %s has unknown outcome %q", m.EntityID, m.Outcome)
		}
		seen[m.EntityID]++
	}
	for _, e := range project.Entities() {
		if seen[e.ID] != 1 {
			t.Errorf("entity %s has %d outcomes, want exactly 1", e.ID, seen[e.ID])
		}
	}
	for _, blk := range project.OpaqueBlocks {
		if seen[blk.ID] != 1 {
			t.Errorf("opaque block %s has %d outcomes, want exactly 1", blk.ID, seen[blk.ID])
		}
	}

	// 2. Generated paths stay inside the workspace and are normalized.
	for _, f := range out.Files {
		clean, err := workspace.NormalizeRel(f.Path)
		if err != nil || clean != f.Path {
			t.Errorf("generated path %q is not a safe normalized path (%v)", f.Path, err)
		}
	}

	// 3. Disabled rules never reach generated output.
	for _, rule := range project.Rules {
		if rule.Enabled {
			continue
		}
		for _, f := range out.Files {
			if strings.Contains(f.Text, strings.TrimSpace(rule.Instruction)) {
				t.Errorf("disabled rule %s leaked into %s", rule.ID, f.Path)
			}
		}
	}

	// 4. Documentation-only content never reaches agent-facing output.
	for _, doc := range project.ContextDocuments {
		if doc.Activation.Type != canonical.ActivationDocumentationOnly {
			continue
		}
		for _, f := range out.Files {
			if strings.Contains(f.Text, strings.TrimSpace(doc.Content)) {
				t.Errorf("documentation-only context %s leaked into %s", doc.ID, f.Path)
			}
		}
	}

	// 5. Rule rationale and examples stay out of agent-facing output.
	for _, rule := range project.Rules {
		for _, text := range append([]string{rule.Rationale}, append(rule.GoodExamples, rule.BadExamples...)...) {
			if strings.TrimSpace(text) == "" {
				continue
			}
			for _, f := range out.Files {
				if strings.Contains(f.Text, strings.TrimSpace(text)) {
					t.Errorf("human-only text of rule %s leaked into %s", rule.ID, f.Path)
				}
			}
		}
	}

	// 6. A lossy or blocked mapping must reference at least one diagnostic.
	for _, m := range out.Mappings {
		if m.Outcome != adapters.OutcomeLossy && m.Outcome != adapters.OutcomeBlocked {
			continue
		}
		if len(m.Diagnostics) == 0 {
			t.Errorf("%s mapping for %s references no diagnostic", m.Outcome, m.EntityID)
		}
	}

	// 7. Token numbers are always marked approximate.
	if !out.TokenReport.Approximate {
		t.Error("token report is not marked approximate")
	}
}

// TestCompilationIsDeterministic compiles the same project many times and
// requires byte-identical output, identical diagnostics and identical order.
func TestCompilationIsDeterministic(t *testing.T) {
	ctx := context.Background()
	ws := materialize(t, filepath.Join(testdataDir, "copilot/basic/input"))
	res, err := compiler.Import(ctx, ws, compiler.ImportOptions{
		Format: canonical.TargetCopilot, ProjectID: "prj_fixture", ProjectName: "Fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []canonical.TargetFormat{
		canonical.TargetClaude, canonical.TargetCodex, canonical.TargetCopilot, canonical.TargetKiro,
	} {
		var first string
		for i := 0; i < 25; i++ {
			out, err := compiler.Compile(ctx, res.Project, compiler.CompileOptions{
				Target: target, Profile: profiles.Default(target),
			})
			if err != nil {
				t.Fatal(err)
			}
			var b strings.Builder
			for _, f := range out.Files {
				b.WriteString(f.Path + "\n" + f.Text + "\n")
			}
			for _, d := range out.Diagnostics {
				b.WriteString(string(d.Code) + d.Fingerprint + "\n")
			}
			for _, m := range out.Mappings {
				b.WriteString(m.EntityID + string(m.Outcome) + strings.Join(m.Files, ",") + "\n")
			}
			if i == 0 {
				first = b.String()
				continue
			}
			if b.String() != first {
				t.Fatalf("compilation for %s is not deterministic (run %d differs)", target, i)
			}
		}
	}
}

// TestImportIsDeterministic checks that importing the same tree repeatedly
// produces identical canonical JSON.
func TestImportIsDeterministic(t *testing.T) {
	ctx := context.Background()
	var first string
	for i := 0; i < 15; i++ {
		ws := materialize(t, filepath.Join(testdataDir, "kiro/steering/input"))
		res, err := compiler.Import(ctx, ws, compiler.ImportOptions{
			Format: canonical.TargetKiro, ProjectID: "prj_fixture", ProjectName: "Fixture",
		})
		if err != nil {
			t.Fatal(err)
		}
		data, err := canonical.MarshalProject(res.Project)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = string(data)
			continue
		}
		if string(data) != first {
			t.Fatal("import is not deterministic")
		}
	}
}

// TestUnavailableTargetIsRefused checks that a declared-but-unimplemented
// target fails with a typed error instead of producing plausible output.
func TestUnavailableTargetIsRefused(t *testing.T) {
	_, err := compiler.Compile(context.Background(), canonical.NewProject("prj", "x"),
		compiler.CompileOptions{Target: canonical.TargetCursor, Profile: profiles.Default(canonical.TargetCursor)})
	if err == nil {
		t.Fatal("compiling for cursor must fail")
	}
	if !strings.Contains(err.Error(), "target unavailable") {
		t.Fatalf("err = %v, want a target-unavailable error", err)
	}
}

// TestProfileCanSkipAndRescope verifies that profiles change delivery only.
func TestProfileCanSkipAndRescope(t *testing.T) {
	ctx := context.Background()
	ws := materialize(t, filepath.Join(testdataDir, "copilot/basic/input"))
	res, err := compiler.Import(ctx, ws, compiler.ImportOptions{
		Format: canonical.TargetCopilot, ProjectID: "prj_fixture", ProjectName: "Fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	no := false
	profile := profiles.Default(canonical.TargetClaude)
	scoped := canonical.PathScoped([]string{"docs/**"}, nil)
	profile.Overrides = map[string]profiles.Override{
		"context.architecture": {Include: &no},
		"context.testing":      {Activation: &scoped},
	}
	out, err := compiler.Compile(ctx, res.Project, compiler.CompileOptions{
		Target: canonical.TargetClaude, Profile: profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range out.Mappings {
		switch m.EntityID {
		case "context.architecture":
			if m.Outcome != adapters.OutcomeSkipped {
				t.Errorf("excluded entity outcome = %s", m.Outcome)
			}
			if len(m.Files) != 0 {
				t.Errorf("excluded entity still wrote files: %v", m.Files)
			}
		case "context.testing":
			if m.Activation.Type != canonical.ActivationPathScoped {
				t.Errorf("re-scoped entity activation = %s", m.Activation.Type)
			}
			if len(m.Files) != 1 || !strings.HasPrefix(m.Files[0], ".claude/rules/") {
				t.Errorf("re-scoped entity files = %v", m.Files)
			}
		}
	}
	// The canonical project itself must not have been modified.
	for _, doc := range res.Project.ContextDocuments {
		if doc.ID == "context.testing" && doc.Activation.Type != canonical.ActivationAlways {
			t.Error("a profile override mutated the canonical project")
		}
	}
}

// TestContentOverrideIsReported checks that diverging wording is never silent.
func TestContentOverrideIsReported(t *testing.T) {
	ctx := context.Background()
	ws := materialize(t, filepath.Join(testdataDir, "copilot/basic/input"))
	res, err := compiler.Import(ctx, ws, compiler.ImportOptions{
		Format: canonical.TargetCopilot, ProjectID: "prj_fixture", ProjectName: "Fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := profiles.Default(canonical.TargetKiro)
	profile.Overrides = map[string]profiles.Override{
		"context.architecture": {ContentOverride: "Different wording for Kiro."},
	}
	out, err := compiler.Compile(ctx, res.Project, compiler.CompileOptions{
		Target: canonical.TargetKiro, Profile: profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range out.Diagnostics {
		if d.Code == diagnostics.TargetOverridesContent {
			found = true
		}
	}
	if !found {
		t.Fatal("a content override must produce STEMMA3601")
	}
	for _, m := range out.Mappings {
		if m.EntityID == "context.architecture" && m.Outcome == adapters.OutcomeExact {
			t.Fatal("an entity whose text was overridden must not be reported as exact")
		}
	}
}

// TestAcceptedDiagnosticsStopBlocking verifies profile-level acceptance.
func TestAcceptedDiagnosticsStopBlocking(t *testing.T) {
	ctx := context.Background()
	data := mustReadFixtureProject(t, "canonical/exclude-and-disabled")
	profile := profiles.Default(canonical.TargetCopilot)
	out, err := compiler.Compile(ctx, data, compiler.CompileOptions{
		Target: canonical.TargetCopilot, Profile: profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	var fingerprint string
	for _, d := range out.Diagnostics {
		if d.Code == diagnostics.ExcludeNotRepresent {
			fingerprint = d.Fingerprint
		}
	}
	if fingerprint == "" {
		t.Fatal("expected an exclude diagnostic to accept")
	}
	profile.AcceptedDiagnostics = []string{fingerprint}
	out, err = compiler.Compile(ctx, data, compiler.CompileOptions{
		Target: canonical.TargetCopilot, Profile: profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range out.Diagnostics {
		if d.Fingerprint == fingerprint {
			if d.Severity != diagnostics.SeverityInfo || d.Blocking {
				t.Fatalf("accepted diagnostic is still %s (blocking=%v)", d.Severity, d.Blocking)
			}
			return
		}
	}
	t.Fatal("the accepted diagnostic disappeared instead of being downgraded")
}

func mustReadFixtureProject(t *testing.T, dir string) canonical.Project {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdataDir, dir, "project.json"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := canonical.UnmarshalProject(data)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestHumanAudienceIsNeverProjected checks that a document written for people
// is not sent to an agent, whatever its activation says.
func TestHumanAudienceIsNeverProjected(t *testing.T) {
	project := canonical.NewProject("prj", "Audience")
	project.ContextDocuments = []canonical.ContextDocument{
		{
			ID: "context.for-people", Title: "Why", Kind: canonical.KindArchitecture,
			Content:  "A long explanation meant for maintainers.",
			Audience: canonical.AudienceHuman, Activation: canonical.Always(),
		},
		{
			ID: "context.for-agents", Title: "How", Kind: canonical.KindConventions,
			Content:  "Use two-space indentation.",
			Audience: canonical.AudienceAgent, Activation: canonical.Always(),
		},
	}
	for _, target := range []canonical.TargetFormat{
		canonical.TargetClaude, canonical.TargetCodex, canonical.TargetCopilot, canonical.TargetKiro,
	} {
		out, err := compiler.Compile(context.Background(), project, compiler.CompileOptions{
			Target: target, Profile: profiles.Default(target),
		})
		if err != nil {
			t.Fatalf("compile %s: %v", target, err)
		}
		for _, f := range out.Files {
			if strings.Contains(f.Text, "meant for maintainers") {
				t.Errorf("%s: human-audience content leaked into %s", target, f.Path)
			}
			if strings.Contains(f.Text, "two-space indentation") {
				continue
			}
		}
		for _, m := range out.Mappings {
			if m.EntityID == "context.for-people" && m.Outcome != adapters.OutcomeSkipped {
				t.Errorf("%s: human-audience document outcome = %s", target, m.Outcome)
			}
		}
		assertProjectionInvariants(t, project, out)
	}
}

// TestRegenerationIsReported checks that rewriting a source file wholesale is
// never silent.
func TestRegenerationIsReported(t *testing.T) {
	ctx := context.Background()
	ws := materialize(t, filepath.Join(testdataDir, "claude/basic/input"))
	res, err := compiler.Import(ctx, ws, compiler.ImportOptions{
		Format: canonical.TargetClaude, ProjectID: "prj_fixture", ProjectName: "Fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Change one always-on document so CLAUDE.md can no longer be reused.
	project := res.Project
	for i := range project.ContextDocuments {
		if project.ContextDocuments[i].Activation.Type == canonical.ActivationAlways {
			project.ContextDocuments[i].Content += "\n\nAn added line."
			break
		}
	}
	plan, err := compiler.BuildPlan(ctx, ws, project, compiler.PlanOptions{
		Target: canonical.TargetClaude, Profile: profiles.Default(canonical.TargetClaude),
		Manifest: manifest.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range plan.Diagnostics {
		if d.Code == diagnostics.RegeneratedFile {
			found = true
		}
	}
	if !found {
		t.Fatalf("regenerating a source file must be reported; diagnostics: %+v", plan.Diagnostics)
	}
}

// TestAcceptedDiagnosticsAreRecordedInTheManifest checks that an apply records
// which lossy mappings were accepted.
func TestAcceptedDiagnosticsAreRecordedInTheManifest(t *testing.T) {
	ctx := context.Background()
	ws := materialize(t, filepath.Join(testdataDir, "copilot/basic/input"))
	res, err := compiler.Import(ctx, ws, compiler.ImportOptions{
		Format: canonical.TargetCopilot, ProjectID: "prj_fixture", ProjectName: "Fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := profiles.Default(canonical.TargetClaude)
	profile.AcceptedDiagnostics = []string{"dg_0000000000000000"}
	plan, err := compiler.BuildPlan(ctx, ws, res.Project, compiler.PlanOptions{
		Target: canonical.TargetClaude, Profile: profile, Manifest: manifest.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := compiler.Apply(ctx, ws, plan, compiler.ApplyOptions{Manifest: manifest.New()})
	if err != nil {
		t.Fatal(err)
	}
	rec := applied.Manifest.Targets["claude"]
	if len(rec.AcceptedDiagnostics) != 1 || rec.AcceptedDiagnostics[0] != "dg_0000000000000000" {
		t.Fatalf("manifest accepted diagnostics = %v", rec.AcceptedDiagnostics)
	}
}
