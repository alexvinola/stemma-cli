package compiler_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/profiles"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

func workspaceWith(t *testing.T, limits workspace.Limits, files map[string]string) *workspace.Workspace {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := workspace.Open(root, limits)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func findDiagnostic(ds []diagnostics.Diagnostic, code diagnostics.Code) (diagnostics.Diagnostic, bool) {
	for _, d := range ds {
		if d.Code == code {
			return d, true
		}
	}
	return diagnostics.Diagnostic{}, false
}

// An incomplete discovery could silently import a subset of the repository's
// configuration, so it blocks unless the caller explicitly allows it.
func TestIncompleteScanBlocksImportUnlessAllowed(t *testing.T) {
	limits := workspace.DefaultLimits()
	limits.MaxDepth = 2
	files := map[string]string{
		"CLAUDE.md":            "# Project\n\n## Style\n\nUse tabs.\n",
		"a/b/c/deep/CLAUDE.md": "# Deep\n\nNever seen.\n",
	}
	for _, format := range []canonical.TargetFormat{"", canonical.TargetClaude} {
		ws := workspaceWith(t, limits, files)
		res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{Format: format})
		if !errors.Is(err, compiler.ErrIncompleteScan) {
			t.Fatalf("format %q: err = %v, want ErrIncompleteScan", format, err)
		}
		d, ok := findDiagnostic(res.Diagnostics, diagnostics.DiscoveryIncomplete)
		if !ok || d.Severity != diagnostics.SeverityError || !d.Blocking {
			t.Fatalf("format %q: want a blocking STEMMA1303 error, got %+v", format, res.Diagnostics)
		}
		if !strings.Contains(d.Detail, workspace.LimitMaxDepth) {
			t.Errorf("detail should name the limit: %q", d.Detail)
		}
		if len(res.Sources) != 0 || len(res.Project.Entities()) != 0 {
			t.Errorf("format %q: an incomplete import must not read sources: %+v", format, res.Sources)
		}
	}

	ws := workspaceWith(t, limits, files)
	res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{AllowIncompleteScan: true})
	if err != nil {
		t.Fatalf("allowed import: %v", err)
	}
	d, ok := findDiagnostic(res.Diagnostics, diagnostics.DiscoveryIncomplete)
	if !ok || d.Severity != diagnostics.SeverityWarning || d.Blocking {
		t.Fatalf("an allowed incomplete scan must stay visible as a warning: %+v", res.Diagnostics)
	}
	if diagnostics.HasBlocking(res.Diagnostics) {
		t.Fatalf("unexpected blocking diagnostics: %+v", res.Diagnostics)
	}
	if len(res.Sources) != 1 || res.Sources[0].Path != "CLAUDE.md" {
		t.Errorf("sources = %+v, want only the discovered CLAUDE.md", res.Sources)
	}
}

func TestCompleteScanHasNoIncompleteDiagnostic(t *testing.T) {
	ws := workspaceWith(t, workspace.DefaultLimits(), map[string]string{
		"CLAUDE.md": "# Project\n\n## Style\n\nUse tabs.\n",
	})
	res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findDiagnostic(res.Diagnostics, diagnostics.DiscoveryIncomplete); ok {
		t.Fatalf("complete scan reported as incomplete: %+v", res.Diagnostics)
	}
}

// A nested CLAUDE.md becomes context scoped to its directory, and the import is
// verified byte-identically so that ownership is recorded.
func TestNestedClaudeMDImportsAsDirectoryScope(t *testing.T) {
	ws := workspaceWith(t, workspace.DefaultLimits(), map[string]string{
		"CLAUDE.md":         "# Project\n\n## Style\n\nUse tabs.\n",
		"src/api/CLAUDE.md": "# API\n\n## Validation\n\nValidate at the boundary.\n",
	})
	res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != canonical.TargetClaude || diagnostics.HasBlocking(res.Diagnostics) {
		t.Fatalf("format = %s, diagnostics = %+v", res.Format, res.Diagnostics)
	}
	var scoped []canonical.ContextDocument
	for _, doc := range res.Project.ContextDocuments {
		if doc.Activation.Type == canonical.ActivationPathScoped {
			scoped = append(scoped, doc)
		}
	}
	if len(scoped) != 1 || len(scoped[0].Activation.Include) != 1 || scoped[0].Activation.Include[0] != "src/api/**" {
		t.Fatalf("scoped documents = %+v", scoped)
	}
	if dir, _ := scoped[0].Extensions.GetString(string(canonical.TargetClaude), "stemma.directory"); dir != "src/api" {
		t.Errorf("directory hint = %q, want src/api", dir)
	}
	verified := map[string]bool{}
	for _, f := range res.VerifiedTarget.GeneratedFiles {
		verified[f.Path] = true
	}
	if !verified["CLAUDE.md"] || !verified["src/api/CLAUDE.md"] {
		t.Errorf("verified files = %+v, want both memory files", res.VerifiedTarget.GeneratedFiles)
	}
}

// Kiro reads AGENTS.md, but the Kiro adapter does not import it (the Codex
// adapter owns it). Choosing --from kiro names the file instead of dropping it.
func TestKiroImportNamesAgentsMDItDoesNotImport(t *testing.T) {
	agents := "# Project\n\n## Style\n\nUse tabs.\n"

	t.Run("only AGENTS.md", func(t *testing.T) {
		ws := workspaceWith(t, workspace.DefaultLimits(), map[string]string{"AGENTS.md": agents})
		auto, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{})
		if err != nil || auto.Format != canonical.TargetCodex {
			t.Fatalf("auto-detected %q, %v; want codex", auto.Format, err)
		}
		if _, ok := findDiagnostic(auto.Diagnostics, diagnostics.SharedFileNotImported); ok {
			t.Errorf("a codex import must not warn about kiro: %+v", auto.Diagnostics)
		}

		res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{Format: canonical.TargetKiro})
		if !errors.Is(err, compiler.ErrNoSources) {
			t.Fatalf("err = %v, want ErrNoSources", err)
		}
		d, ok := findDiagnostic(res.Diagnostics, diagnostics.SharedFileNotImported)
		if !ok || d.Path != "AGENTS.md" || d.Target != string(canonical.TargetKiro) ||
			d.Severity != diagnostics.SeverityWarning || d.Blocking {
			t.Fatalf("want a STEMMA1304 warning for AGENTS.md, got %+v", res.Diagnostics)
		}
		if !strings.Contains(d.Suggestion, "--from codex") {
			t.Errorf("suggestion = %q", d.Suggestion)
		}
	})

	t.Run("with steering", func(t *testing.T) {
		ws := workspaceWith(t, workspace.DefaultLimits(), map[string]string{
			"AGENTS.md":                agents,
			"src/AGENTS.md":            agents,
			".kiro/steering/typing.md": "---\ninclusion: always\n---\n\n# Typing\n\nUse strict types.\n",
		})
		if _, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{}); !errors.Is(err, compiler.ErrAmbiguousSource) {
			t.Fatalf("err = %v, want ErrAmbiguousSource", err)
		}
		res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{Format: canonical.TargetKiro})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Sources) != 1 || res.Sources[0].Path != ".kiro/steering/typing.md" {
			t.Errorf("sources = %+v, want only the steering file", res.Sources)
		}
		var named []string
		for _, d := range res.Diagnostics {
			if d.Code == diagnostics.SharedFileNotImported {
				named = append(named, d.Path)
			}
		}
		if strings.Join(named, ",") != "AGENTS.md,src/AGENTS.md" {
			t.Errorf("warned about %v, want AGENTS.md and src/AGENTS.md", named)
		}
	})
}

// The nested CLAUDE.md destination is only a hint. It is honoured while the
// scope is still exactly <dir>/**; any other shape falls back to a
// .claude/rules file whose paths front matter expresses the scope exactly.
func TestClaudeNestedDirectoryHintFallsBackToRules(t *testing.T) {
	cases := []struct {
		name     string
		hint     string
		include  []string
		exclude  []string
		override *profiles.Override
		want     string
	}{
		{name: "exact subtree", hint: "src/api", include: []string{"src/api/**"}, want: "src/api/CLAUDE.md"},
		{name: "no hint", include: []string{"src/api/**"}, want: ".claude/rules/context-api.md"},
		{name: "narrowed scope", hint: "src/api", include: []string{"src/api/*.ts"}, want: ".claude/rules/context-api.md"},
		{name: "exclude", hint: "src/api", include: []string{"src/api/**"}, exclude: []string{"src/api/gen/**"}, want: ".claude/rules/context-api.md"},
		{name: "profile pin", hint: "src/api", include: []string{"src/api/**"},
			override: &profiles.Override{Filename: "api.md"}, want: ".claude/rules/api.md"},
		{name: "hint inside rules", hint: ".claude/rules/x", include: []string{".claude/rules/x/**"}, want: ".claude/rules/context-api.md"},
		{name: "hint is root memory", hint: ".claude", include: []string{".claude/**"}, want: ".claude/rules/context-api.md"},
		{name: "unsafe hint", hint: "../out", include: []string{"../out/**"}, want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := canonical.NewProject("project", "Project")
			doc := canonical.ContextDocument{ID: "context.api", Title: "API", Content: "Validate input.",
				Kind: canonical.KindOther, Audience: canonical.AudienceAgent,
				Activation: canonical.PathScoped(c.include, c.exclude)}
			if c.hint != "" {
				doc.Extensions.Set(string(canonical.TargetClaude), "stemma.directory", c.hint)
			}
			p.ContextDocuments = []canonical.ContextDocument{doc}
			profile := profiles.Default(canonical.TargetClaude)
			if c.override != nil {
				profile.Overrides["context.api"] = *c.override
			}
			out, err := compiler.Compile(context.Background(), p, compiler.CompileOptions{
				Target: canonical.TargetClaude, Profile: profile,
			})
			if c.want == "" {
				// An escaping pattern is rejected before any destination is chosen.
				for _, f := range out.Files {
					if strings.HasPrefix(f.Path, "..") || strings.HasSuffix(f.Path, "CLAUDE.md") {
						t.Errorf("unsafe hint produced %s", f.Path)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Files) != 1 || out.Files[0].Path != c.want {
				var paths []string
				for _, f := range out.Files {
					paths = append(paths, f.Path)
				}
				t.Fatalf("files = %v, want [%s]", paths, c.want)
			}
			if len(out.Mappings) != 1 || out.Mappings[0].Outcome != "exact" && len(c.exclude) == 0 {
				t.Errorf("mappings = %+v", out.Mappings)
			}
		})
	}
}

// A directory that cannot be read hides whatever configuration it holds, so
// it makes discovery incomplete exactly like a resource limit.
func TestUnreadableDirectoryBlocksImportUnlessAllowed(t *testing.T) {
	ws := workspaceWith(t, workspace.DefaultLimits(), map[string]string{
		"CLAUDE.md":        "Root instructions.",
		"hidden/CLAUDE.md": "Hidden instructions.",
	})
	hidden, err := ws.Native("hidden")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hidden, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(hidden, 0o755) })
	if _, err := os.ReadDir(hidden); err == nil {
		t.Skip("directory permissions are not enforced for this user")
	}

	res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{Format: canonical.TargetClaude})
	if !errors.Is(err, compiler.ErrIncompleteScan) {
		t.Fatalf("err = %v, want ErrIncompleteScan; sources = %+v", err, res.Sources)
	}
	d, ok := findDiagnostic(res.Diagnostics, diagnostics.DiscoveryIncomplete)
	if !ok || !d.Blocking || !strings.Contains(d.Detail, "1 unreadable directory") {
		t.Fatalf("want a blocking STEMMA1303 naming the unreadable directory: %+v", res.Diagnostics)
	}
	if u, ok := findDiagnostic(res.Diagnostics, diagnostics.DirectoryUnreadable); !ok || u.Path != "hidden" {
		t.Errorf("want STEMMA1305 at hidden: %+v", res.Diagnostics)
	}

	res, err = compiler.Import(context.Background(), ws, compiler.ImportOptions{
		Format: canonical.TargetClaude, AllowIncompleteScan: true,
	})
	if err != nil || diagnostics.HasBlocking(res.Diagnostics) {
		t.Fatalf("allowed import: %v %+v", err, res.Diagnostics)
	}
	if len(res.Sources) != 1 || res.Sources[0].Path != "CLAUDE.md" {
		t.Errorf("sources = %+v", res.Sources)
	}
}
