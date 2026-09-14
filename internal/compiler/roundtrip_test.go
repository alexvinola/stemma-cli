package compiler_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/manifest"
	"github.com/alexvinola/stemma-cli/internal/profiles"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

var fixtureFormats = map[string]canonical.TargetFormat{
	"copilot/basic": canonical.TargetCopilot,
	"claude/basic":  canonical.TargetClaude,
	"codex/nested":  canonical.TargetCodex,
	"kiro/steering": canonical.TargetKiro,
	// Brace groups are expanded into the canonical model, so these two cases
	// also assert the other half of that contract: a hand-written pattern is
	// still written back exactly as the author wrote it.
	"copilot/brace-globs": canonical.TargetCopilot,
	"claude/brace-globs":  canonical.TargetClaude,
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

// TestBracePatternsSurviveCopilotRoundTrip is the regression test for the
// silently corrupted applyTo: a brace group holds a comma, Copilot's applyTo
// separates patterns with a comma, and the two used to be indistinguishable.
//
// Expansion at import is what resolves that, so the assertion is not that the
// braces come back — they are gone by design — but that the *scope* does:
// Claude -> Copilot -> Claude must name exactly the same set of files, and
// must claim "exact" only when that is true.
func TestBracePatternsSurviveCopilotRoundTrip(t *testing.T) {
	ctx := context.Background()
	ws := materialize(t, filepath.Join(testdataDir, "claude", "brace-globs", "input"))

	first, err := compiler.Import(ctx, ws, compiler.ImportOptions{
		Format: canonical.TargetClaude, ProjectID: "prj_fixture", ProjectName: "Fixture",
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	want := map[string][]string{
		"rule.strict-typing":     {"src/**/*.ts", "src/**/*.tsx", "lib/**/*.go"},
		"rule.asset-conventions": {"app/**/*.css", "app/**/*.scss", "packages/**/*.css", "packages/**/*.scss"},
	}
	assertScopes(t, "after importing Claude", first.Project, want)

	// Claude -> Copilot. The mapping may only be "exact" if applyTo really
	// does carry the scope, so an unexpanded brace group would fail here.
	out, err := compiler.Compile(ctx, first.Project, compiler.CompileOptions{
		Target: canonical.TargetCopilot, Profile: profiles.Default(canonical.TargetCopilot),
	})
	if err != nil {
		t.Fatalf("compile to copilot: %v", err)
	}
	for _, m := range out.Mappings {
		if _, scoped := want[m.EntityID]; !scoped {
			continue
		}
		if m.Outcome != adapters.OutcomeExact {
			t.Errorf("%s: outcome = %s, want exact", m.EntityID, m.Outcome)
		}
	}
	for _, f := range out.Files {
		if strings.Contains(string(f.Content), "{") {
			t.Errorf("%s still contains a brace group:\n%s", f.Path, f.Content)
		}
	}

	// Copilot -> Claude, through the filesystem, exactly as a user would.
	viaCopilot := reimport(t, ctx, out, canonical.TargetCopilot)
	assertScopes(t, "after re-importing Copilot", viaCopilot, map[string][]string{
		"context.strict-typing":     want["rule.strict-typing"],
		"context.asset-conventions": want["rule.asset-conventions"],
	})

	back, err := compiler.Compile(ctx, viaCopilot, compiler.CompileOptions{
		Target: canonical.TargetClaude, Profile: profiles.Default(canonical.TargetClaude),
	})
	if err != nil {
		t.Fatalf("compile back to claude: %v", err)
	}
	// Back in .claude/rules/, so these are rules again, with the scopes they
	// started with.
	final := reimport(t, ctx, back, canonical.TargetClaude)
	assertScopes(t, "after the full Claude -> Copilot -> Claude trip", final, want)
}

// reimport writes a compilation into a clean workspace and imports it back.
func reimport(
	t *testing.T, ctx context.Context, out compiler.CompileResult, format canonical.TargetFormat,
) canonical.Project {
	t.Helper()
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
	res, err := compiler.Import(ctx, dest, compiler.ImportOptions{
		Format: format, ProjectID: "prj_fixture", ProjectName: "Fixture",
	})
	if err != nil {
		t.Fatalf("re-import %s: %v", format, err)
	}
	return res.Project
}

// assertScopes checks the include patterns of the named entities, whether they
// were imported as rules or as context documents.
func assertScopes(t *testing.T, stage string, p canonical.Project, want map[string][]string) {
	t.Helper()
	got := map[string][]string{}
	for _, r := range p.Rules {
		got[r.ID] = r.Activation.Include
	}
	for _, d := range p.ContextDocuments {
		got[d.ID] = d.Activation.Include
	}
	for id, wantInc := range want {
		gotInc, ok := got[id]
		if !ok {
			t.Errorf("%s: entity %s is missing", stage, id)
			continue
		}
		if strings.Join(gotInc, "\x00") != strings.Join(wantInc, "\x00") {
			t.Errorf("%s: %s include = %v, want %v", stage, id, gotInc, wantInc)
		}
	}
}
