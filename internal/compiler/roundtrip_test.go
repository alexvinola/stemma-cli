package compiler_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/compiler"
	"github.com/alexvinola/stemma/internal/manifest"
	"github.com/alexvinola/stemma/internal/profiles"
	"github.com/alexvinola/stemma/internal/workspace"
)

var fixtureFormats = map[string]canonical.TargetFormat{
	"copilot/basic": canonical.TargetCopilot,
	"claude/basic":  canonical.TargetClaude,
	"codex/nested":  canonical.TargetCodex,
	"kiro/steering": canonical.TargetKiro,
}

func fixtureNames() []string {
	out := make([]string, 0, len(fixtureFormats))
	for k := range fixtureFormats {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestSameFormatRoundTripIsByteIdentical checks the strongest round-trip
// guarantee: importing a provider's files and compiling straight back to the
// same provider, with no semantic change, must reproduce the original bytes
// and propose no changes at all.
func TestSameFormatRoundTripIsByteIdentical(t *testing.T) {
	for _, name := range fixtureNames() {
		format := fixtureFormats[name]
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			ws := materialize(t, filepath.Join(testdataDir, name, "input"))

			res, err := compiler.Import(ctx, ws, compiler.ImportOptions{
				Format: format, ProjectID: "prj_fixture", ProjectName: "Fixture",
			})
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			plan, err := compiler.BuildPlan(ctx, ws, res.Project, compiler.PlanOptions{
				Target: format, Profile: profiles.Default(format), Manifest: manifest.New(),
			})
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			for _, c := range plan.Changes {
				if c.Kind != compiler.ChangeUnchanged {
					native, _ := ws.Native(c.Path)
					original, _ := os.ReadFile(native)
					t.Errorf("%s: %s (expected unchanged)\n--- on disk ---\n%s\n--- generated ---\n%s",
						c.Path, c.Kind, original, c.Content)
				}
			}
			if plan.HasChanges() {
				t.Error("a no-op same-format round trip proposed changes")
			}
		})
	}
}

// TestCrossFormatRoundTrips compiles every fixture to every other provider and
// checks the invariants that must hold for any target.
func TestCrossFormatRoundTrips(t *testing.T) {
	targets := []canonical.TargetFormat{
		canonical.TargetClaude, canonical.TargetCodex,
		canonical.TargetCopilot, canonical.TargetKiro,
	}
	for _, name := range fixtureNames() {
		format := fixtureFormats[name]
		for _, target := range targets {
			t.Run(name+"->"+string(target), func(t *testing.T) {
				ctx := context.Background()
				ws := materialize(t, filepath.Join(testdataDir, name, "input"))
				res, err := compiler.Import(ctx, ws, compiler.ImportOptions{
					Format: format, ProjectID: "prj_fixture", ProjectName: "Fixture",
				})
				if err != nil {
					t.Fatalf("import: %v", err)
				}
				out, err := compiler.Compile(ctx, res.Project, compiler.CompileOptions{
					Target: target, Profile: profiles.Default(target),
				})
				if err != nil {
					t.Fatalf("compile: %v", err)
				}
				if len(out.Files) == 0 {
					t.Fatal("no files were generated")
				}
				assertProjectionInvariants(t, res.Project, out)
			})
		}
	}
}

// TestReimportPreservesEntities compiles a project to another provider, writes
// the result, imports it back and checks that no agent-facing content is lost.
func TestReimportPreservesEntities(t *testing.T) {
	pairs := []struct{ from, to string }{
		{"copilot/basic", "claude"},
		{"claude/basic", "codex"},
		{"codex/nested", "kiro"},
		{"kiro/steering", "github-copilot"},
	}
	for _, pair := range pairs {
		t.Run(pair.from+"->"+pair.to, func(t *testing.T) {
			ctx := context.Background()
			ws := materialize(t, filepath.Join(testdataDir, pair.from, "input"))
			source := fixtureFormats[pair.from]
			target := canonical.TargetFormat(pair.to)

			first, err := compiler.Import(ctx, ws, compiler.ImportOptions{
				Format: source, ProjectID: "prj_fixture", ProjectName: "Fixture",
			})
			if err != nil {
				t.Fatalf("import: %v", err)
			}

			// Write the compiled output into a clean workspace.
			out, err := compiler.Compile(ctx, first.Project, compiler.CompileOptions{
				Target: target, Profile: profiles.Default(target),
			})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			dest, err := workspace.Open(t.TempDir(), workspace.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			tx := dest.Begin()
			for _, f := range out.Files {
				if err := tx.Add(workspace.WriteOp{Path: f.Path, Content: f.Content, Mode: 0o644}); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}

			second, err := compiler.Import(ctx, dest, compiler.ImportOptions{
				Format: target, ProjectID: "prj_fixture", ProjectName: "Fixture",
			})
			if err != nil {
				t.Fatalf("re-import: %v", err)
			}

			// Every piece of agent-facing text from the first project must
			// still be findable in the re-imported project.
			haystack := allText(second.Project)
			for _, doc := range first.Project.ContextDocuments {
				if doc.Activation.Type == canonical.ActivationDocumentationOnly {
					continue
				}
				if !containsNormalized(haystack, doc.Content) {
					t.Errorf("context %s was lost in the round trip through %s", doc.ID, target)
				}
			}
			for _, rule := range first.Project.Rules {
				if !rule.Enabled {
					continue
				}
				if !containsNormalized(haystack, rule.Instruction) {
					t.Errorf("rule %s was lost in the round trip through %s", rule.ID, target)
				}
			}
			for _, skill := range first.Project.Skills {
				if !containsNormalized(haystack, skill.Content) {
					t.Errorf("skill %s was lost in the round trip through %s", skill.ID, target)
				}
			}
			for _, proc := range first.Project.Procedures {
				if !containsNormalized(haystack, proc.Content) {
					t.Errorf("procedure %s was lost in the round trip through %s", proc.ID, target)
				}
			}
		})
	}
}

func allText(p canonical.Project) string {
	var b strings.Builder
	for _, e := range p.ContextDocuments {
		b.WriteString(e.Content + "\n")
	}
	for _, e := range p.Rules {
		b.WriteString(e.Instruction + "\n")
	}
	for _, e := range p.Procedures {
		b.WriteString(e.Content + "\n")
	}
	for _, e := range p.Skills {
		b.WriteString(e.Content + "\n")
	}
	for _, e := range p.Agents {
		b.WriteString(e.Instructions + "\n")
	}
	for _, e := range p.OpaqueBlocks {
		b.WriteString(e.Content + "\n")
	}
	return normalizeSpace(b.String())
}

func containsNormalized(haystack, needle string) bool {
	return strings.Contains(haystack, normalizeSpace(needle))
}

func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TestCanonicalEditIsNeverDiscarded is the counterpart to the byte-identical
// round trip: once the canonical project is edited, the original bytes must no
// longer be reused, or the edit would silently disappear.
func TestCanonicalEditIsNeverDiscarded(t *testing.T) {
	for _, name := range fixtureNames() {
		format := fixtureFormats[name]
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			ws := materialize(t, filepath.Join(testdataDir, name, "input"))
			res, err := compiler.Import(ctx, ws, compiler.ImportOptions{
				Format: format, ProjectID: "prj_fixture", ProjectName: "Fixture",
			})
			if err != nil {
				t.Fatal(err)
			}
			project := res.Project
			const marker = "AN EDIT MADE IN THE CANONICAL PROJECT"
			switch {
			case len(project.ContextDocuments) > 0:
				project.ContextDocuments[0].Content += "\n\n" + marker
			case len(project.Rules) > 0:
				project.Rules[0].Instruction += "\n\n" + marker
			default:
				t.Skip("fixture has no editable entity")
			}

			plan, err := compiler.BuildPlan(ctx, ws, project, compiler.PlanOptions{
				Target: format, Profile: profiles.Default(format), Manifest: manifest.New(),
			})
			if err != nil {
				t.Fatal(err)
			}
			var carried bool
			for _, c := range plan.Changes {
				if strings.Contains(c.Content, marker) {
					carried = true
				}
			}
			if !carried {
				t.Fatal("an edit to the canonical project did not reach the generated output")
			}
			if !plan.HasChanges() {
				t.Fatal("an edited project produced no changes")
			}
		})
	}
}
