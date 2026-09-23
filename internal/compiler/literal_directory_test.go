package compiler_test

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/globs"
	"github.com/alexvinola/stemma-cli/internal/manifest"
	"github.com/alexvinola/stemma-cli/internal/profiles"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// literalDirs are directory names that contain glob syntax. A nested
// instructions file in one of them is scoped to that physical directory.
var literalDirs = []struct {
	dir     string
	pattern string
	sibling string // a path the unquoted name would wrongly match
	comma   bool
	posix   bool // the name is not a valid Windows directory name
}{
	{dir: "app/[id]", pattern: "app/[[]id]/**", sibling: "app/i/handler.ts"},
	{dir: "app/[...slug]", pattern: "app/[[]...slug]/**", sibling: "app/s/page.tsx"},
	{dir: "lib/{core}", pattern: "lib/[{]core[}]/**", sibling: "lib/core/x.ts"},
	{dir: "lib/{a,b}", pattern: "lib/[{]a,b[}]/**", sibling: "lib/a/x.ts", comma: true},
	{dir: "q/a?b", pattern: "q/a[?]b/**", sibling: "q/axb/x.ts", posix: true},
	{dir: "s/*", pattern: "s/[*]/**", sibling: "s/anything/x.ts", posix: true},
}

func compileTarget(t *testing.T, p canonical.Project, target canonical.TargetFormat) compiler.CompileResult {
	t.Helper()
	out, err := compiler.Compile(context.Background(), p, compiler.CompileOptions{
		Target: target, Profile: profiles.Default(target),
	})
	if err != nil {
		t.Fatalf("compile %s: %v", target, err)
	}
	return out
}

func fileText(out compiler.CompileResult, path string) (string, bool) {
	for _, f := range out.Files {
		if f.Path == path {
			return f.Text, true
		}
	}
	return "", false
}

// The audit reproduction: app/[id]/CLAUDE.md must not become the character
// class "[id]", which matches app/i/ and not app/[id]/.
func TestNestedLiteralDirectoryIsNotAGlob(t *testing.T) {
	ws := workspaceWith(t, workspace.DefaultLimits(), map[string]string{
		"app/[id]/CLAUDE.md": "# API\n\nValidate input.\n",
	})
	res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{Format: canonical.TargetClaude})
	if err != nil || diagnostics.HasBlocking(res.Diagnostics) {
		t.Fatalf("import: %v %+v", err, res.Diagnostics)
	}
	if len(res.Project.ContextDocuments) != 1 {
		t.Fatalf("documents = %+v", res.Project.ContextDocuments)
	}
	include := res.Project.ContextDocuments[0].Activation.Include
	if len(include) != 1 || include[0] != "app/[[]id]/**" {
		t.Fatalf("include = %q, want the quoted literal directory", include)
	}
	if !globs.Match(include[0], "app/[id]/handler.ts") || globs.Match(include[0], "app/i/handler.ts") {
		t.Errorf("literal directory became a glob: %q", include)
	}
	out := compileTarget(t, res.Project, canonical.TargetCopilot)
	for _, f := range out.Files {
		if strings.Contains(f.Text, "applyTo") && !strings.Contains(f.Text, "applyTo: app/[[]id]/**") {
			t.Errorf("Copilot applyTo does not carry the literal scope:\n%s", f.Text)
		}
	}
}

func TestNestedLiteralDirectoriesRoundTripAndProject(t *testing.T) {
	for _, source := range []struct {
		format canonical.TargetFormat
		file   string
	}{
		{canonical.TargetClaude, "CLAUDE.md"},
		{canonical.TargetCodex, "AGENTS.md"},
	} {
		for _, c := range literalDirs {
			if c.posix && runtime.GOOS == "windows" {
				continue
			}
			t.Run(string(source.format)+"/"+c.dir, func(t *testing.T) {
				ctx := context.Background()
				nested := c.dir + "/" + source.file
				ws := workspaceWith(t, workspace.DefaultLimits(), map[string]string{
					source.file: "# Project\n\n## Style\n\nUse tabs.\n",
					nested:      "# Scoped\n\n## Validation\n\nValidate input.\n",
				})
				res, err := compiler.Import(ctx, ws, compiler.ImportOptions{Format: source.format})
				if err != nil || diagnostics.HasBlocking(res.Diagnostics) {
					t.Fatalf("import: %v %+v", err, res.Diagnostics)
				}
				var scoped *canonical.ContextDocument
				for i := range res.Project.ContextDocuments {
					if res.Project.ContextDocuments[i].Activation.Type == canonical.ActivationPathScoped {
						scoped = &res.Project.ContextDocuments[i]
					}
				}
				if scoped == nil || len(scoped.Activation.Include) != 1 || scoped.Activation.Include[0] != c.pattern {
					t.Fatalf("scoped document = %+v, want include %q", scoped, c.pattern)
				}
				if !globs.Match(c.pattern, c.dir+"/x.ts") || globs.Match(c.pattern, c.sibling) {
					t.Fatalf("%q does not scope exactly %s", c.pattern, c.dir)
				}

				// Same provider, unchanged: byte-identical, nothing to write.
				plan, err := compiler.BuildPlan(ctx, ws, res.Project, compiler.PlanOptions{
					Target: source.format, Profile: profiles.Default(source.format), Manifest: manifest.New(),
				})
				if err != nil {
					t.Fatal(err)
				}
				if plan.HasChanges() {
					t.Errorf("same-provider round trip proposed changes: %+v", plan.Changes)
				}

				// Same provider, regenerated after an edit: still the same directory.
				edited := res.Project
				edited.ContextDocuments = append([]canonical.ContextDocument{}, res.Project.ContextDocuments...)
				for i := range edited.ContextDocuments {
					if edited.ContextDocuments[i].ID == scoped.ID {
						edited.ContextDocuments[i].Content = "Validate every input."
					}
				}
				out := compileTarget(t, edited, source.format)
				if text, ok := fileText(out, nested); !ok || !strings.Contains(text, "Validate every input.") {
					t.Errorf("regenerated %s output does not write back to %s: %+v", source.format, nested, out.Files)
				}
				assertOutcome(t, out, scoped.ID, "exact")

				// Cross-provider: the other directory-scoped provider writes the
				// same literal directory; pattern providers carry the quoted scope.
				other, otherFile := canonical.TargetCodex, "AGENTS.md"
				if source.format == canonical.TargetCodex {
					other, otherFile = canonical.TargetClaude, "CLAUDE.md"
				}
				out = compileTarget(t, res.Project, other)
				if other == canonical.TargetCodex {
					if _, ok := fileText(out, c.dir+"/"+otherFile); !ok {
						t.Errorf("codex output lacks %s/%s: %+v", c.dir, otherFile, out.Files)
					}
				} else {
					// From Codex there is no Claude directory hint: a rule file.
					found := false
					for _, f := range out.Files {
						found = found || strings.Contains(f.Text, c.pattern)
					}
					if !found {
						t.Errorf("claude output lacks the quoted pattern %q", c.pattern)
					}
				}
				assertOutcome(t, out, scoped.ID, "exact")

				out = compileTarget(t, res.Project, canonical.TargetCopilot)
				copilotText := ""
				for _, f := range out.Files {
					if strings.Contains(f.Text, "applyTo:") {
						copilotText = f.Text
					}
				}
				if !strings.Contains(copilotText, c.pattern) {
					t.Errorf("Copilot applyTo lacks %q:\n%s", c.pattern, copilotText)
				}
				if c.comma {
					// applyTo is comma-separated: never exact, always explained.
					assertOutcome(t, out, scoped.ID, "lossy")
					if _, ok := findDiagnostic(out.Diagnostics, diagnostics.PatternNotRepresent); !ok {
						t.Errorf("missing STEMMA3102 for a comma-bearing directory: %+v", out.Diagnostics)
					}
				} else {
					assertOutcome(t, out, scoped.ID, "exact")
				}

				out = compileTarget(t, res.Project, canonical.TargetKiro)
				kiroText := ""
				for _, f := range out.Files {
					if strings.Contains(f.Text, "fileMatchPattern") {
						kiroText = f.Text
					}
				}
				if !strings.Contains(kiroText, c.pattern) {
					t.Errorf("Kiro fileMatchPattern lacks %q:\n%s", c.pattern, kiroText)
				}
			})
		}
	}
}

func assertOutcome(t *testing.T, out compiler.CompileResult, id string, want compiler.Outcome) {
	t.Helper()
	for _, m := range out.Mappings {
		if m.EntityID == id {
			if m.Outcome != want {
				t.Errorf("%s on %s: outcome %s, want %s (%s)", id, out.Target, m.Outcome, want, m.Explanation)
			}
			return
		}
	}
	t.Errorf("%s on %s: no mapping", id, out.Target)
}

// A directory whose quoted scope is not a valid pattern is refused with a
// blocking diagnostic, never imported with a different scope.
func TestNestedDirectoryThatCannotBeQuotedIsRejected(t *testing.T) {
	// Eight segments of 50 '[' quote to 8*50*3 bytes, beyond the 1024-byte
	// pattern limit, while each name stays within filesystem limits.
	dir := strings.TrimSuffix(strings.Repeat(strings.Repeat("[", 50)+"/", 8), "/")
	for _, source := range []struct {
		format canonical.TargetFormat
		file   string
	}{{canonical.TargetClaude, "CLAUDE.md"}, {canonical.TargetCodex, "AGENTS.md"}} {
		ws := workspaceWith(t, workspace.DefaultLimits(), map[string]string{
			dir + "/" + source.file: "# Scoped\n\nValidate input.\n",
		})
		res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{Format: source.format})
		if err != nil {
			t.Fatalf("%s: %v", source.format, err)
		}
		d, ok := findDiagnostic(res.Diagnostics, diagnostics.InvalidGlob)
		if !ok || !d.Blocking || d.Path != dir+"/"+source.file {
			t.Fatalf("%s: want a blocking STEMMA2101 for the file, got %+v", source.format, res.Diagnostics)
		}
		if len(res.Project.ContextDocuments) != 0 || len(res.Project.OpaqueBlocks) != 1 {
			t.Errorf("%s: the file must be preserved, not modelled: %+v", source.format, res.Project)
		}
	}
}
