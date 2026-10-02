package codex

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/discovery"
	"github.com/alexvinola/stemma-cli/internal/provenance"
)

func sourceFiles(t *testing.T, files map[string]string) []adapters.SourceFile {
	t.Helper()
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]adapters.SourceFile, 0, len(paths))
	for _, p := range paths {
		format, role, ok := discovery.Classify(p)
		if !ok || format != canonical.TargetCodex {
			t.Fatalf("%s is not a Codex path", p)
		}
		data := []byte(files[p])
		out = append(out, adapters.SourceFile{Path: p, Data: data, Hash: provenance.HashBytes(data), Role: role})
	}
	return out
}

// TestImportResolvesOneEffectiveFilePerDirectory pins Codex precedence: in
// each directory, AGENTS.override.md is the file Codex reads whenever it
// exists (even when it is empty), and AGENTS.md is read only without one.
// Expected values are hand-authored from the Codex documentation.
func TestImportResolvesOneEffectiveFilePerDirectory(t *testing.T) {
	const (
		base     = "# Base\n\n## Base rules\n\nBASE CONTENT\n"
		override = "# Override\n\n## Override rules\n\nOVERRIDE CONTENT\n"
	)
	cases := []struct {
		name  string
		files map[string]string
		// sources lists the files entities were imported from.
		sources []string
		// shadowed lists AGENTS.md files preserved as inactive.
		shadowed []string
		// emptyOverrides lists override files preserved because they are empty.
		emptyOverrides []string
		// scope is the activation of every imported entity: "" for always-on.
		scope string
	}{
		{name: "root base only", files: map[string]string{"AGENTS.md": base},
			sources: []string{"AGENTS.md"}},
		{name: "root override only", files: map[string]string{"AGENTS.override.md": override},
			sources: []string{"AGENTS.override.md"}},
		{name: "root override and base",
			files:   map[string]string{"AGENTS.md": base, "AGENTS.override.md": override},
			sources: []string{"AGENTS.override.md"}, shadowed: []string{"AGENTS.md"}},
		{name: "nested base only", files: map[string]string{"svc/AGENTS.md": base},
			sources: []string{"svc/AGENTS.md"}, scope: "svc/**"},
		{name: "nested override only", files: map[string]string{"svc/AGENTS.override.md": override},
			sources: []string{"svc/AGENTS.override.md"}, scope: "svc/**"},
		{name: "nested override and base",
			files:   map[string]string{"svc/AGENTS.md": base, "svc/AGENTS.override.md": override},
			sources: []string{"svc/AGENTS.override.md"}, shadowed: []string{"svc/AGENTS.md"}, scope: "svc/**"},
		{name: "zero-byte override still shadows",
			files:    map[string]string{"AGENTS.md": base, "AGENTS.override.md": ""},
			shadowed: []string{"AGENTS.md"}, emptyOverrides: []string{"AGENTS.override.md"}},
		{name: "whitespace-only override still shadows",
			files:    map[string]string{"svc/AGENTS.md": base, "svc/AGENTS.override.md": " \n\t\r\n\u00a0\u3000\n"},
			shadowed: []string{"svc/AGENTS.md"}, emptyOverrides: []string{"svc/AGENTS.override.md"}},
		{name: "empty override without a base",
			files:          map[string]string{"AGENTS.override.md": "\n"},
			emptyOverrides: []string{"AGENTS.override.md"}},
		{name: "empty base shadowed by an override",
			files:    map[string]string{"AGENTS.md": "", "AGENTS.override.md": override},
			sources:  []string{"AGENTS.override.md"},
			shadowed: []string{"AGENTS.md"}},
		{name: "precedence is per directory",
			files: map[string]string{
				"AGENTS.md":              base,
				"svc/AGENTS.override.md": override,
				"svc/AGENTS.md":          base,
				"lib/AGENTS.md":          base,
			},
			sources:  []string{"AGENTS.md", "lib/AGENTS.md", "svc/AGENTS.override.md"},
			shadowed: []string{"svc/AGENTS.md"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Importer{}.Import(context.Background(), adapters.ImportInput{
				Files: sourceFiles(t, tc.files), IDs: canonical.NewAllocator(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if diagnostics.HasBlocking(res.Diagnostics) {
				t.Fatalf("unexpected blocking diagnostics: %+v", res.Diagnostics)
			}
			p := res.Project

			var sources []string
			for _, d := range p.ContextDocuments {
				if !contains(sources, d.Provenance.SourcePath) {
					sources = append(sources, d.Provenance.SourcePath)
				}
				if strings.Contains(d.Content, "BASE CONTENT") && contains(tc.shadowed, d.Provenance.SourcePath) {
					t.Errorf("shadowed content became entity %s", d.ID)
				}
				want := canonical.ActivationAlways
				if strings.Contains(d.Provenance.SourcePath, "/") {
					want = canonical.ActivationPathScoped
				}
				if d.Activation.Type != want {
					t.Errorf("%s activation = %+v", d.ID, d.Activation)
				}
				if tc.scope != "" && (len(d.Activation.Include) != 1 || d.Activation.Include[0] != tc.scope) {
					t.Errorf("%s scope = %v, want %s", d.ID, d.Activation.Include, tc.scope)
				}
			}
			sort.Strings(sources)
			if strings.Join(sources, ",") != strings.Join(tc.sources, ",") {
				t.Errorf("entities imported from %v, want %v", sources, tc.sources)
			}

			var shadowed, empties []string
			for _, blk := range p.OpaqueBlocks {
				switch {
				case strings.HasPrefix(blk.Reason, "inactive under Codex precedence"):
					shadowed = append(shadowed, blk.SourcePath)
					if blk.Content != tc.files[blk.SourcePath] || !blk.ReemitForRoundTrip {
						t.Errorf("shadowed %s not preserved verbatim: %+v", blk.SourcePath, blk)
					}
					if blk.Hash != provenance.HashString(blk.Content) {
						t.Errorf("shadowed %s hash mismatch", blk.SourcePath)
					}
					marker, ok := p.Extensions.GetString(string(canonical.TargetCodex), shadowedKeyPrefix+blk.SourcePath)
					if !ok || marker != joinDir(instructionsDir(blk.SourcePath), OverrideFile) {
						t.Errorf("shadowed %s marker = %q, %v", blk.SourcePath, marker, ok)
					}
					assertDiagnostic(t, res.Diagnostics, diagnostics.ShadowedFilePreserved,
						diagnostics.SeverityWarning, blk.SourcePath, blk.ID)
				case strings.HasPrefix(blk.Reason, "the override file is empty"):
					empties = append(empties, blk.SourcePath)
					if blk.Content != tc.files[blk.SourcePath] || !blk.ReemitForRoundTrip {
						t.Errorf("empty override %s not preserved verbatim: %+v", blk.SourcePath, blk)
					}
				default:
					t.Errorf("unexpected opaque block %+v", blk)
				}
			}
			sort.Strings(shadowed)
			sort.Strings(empties)
			if strings.Join(shadowed, ",") != strings.Join(tc.shadowed, ",") {
				t.Errorf("shadowed = %v, want %v", shadowed, tc.shadowed)
			}
			if strings.Join(empties, ",") != strings.Join(tc.emptyOverrides, ",") {
				t.Errorf("empty overrides = %v, want %v", empties, tc.emptyOverrides)
			}
			count := 0
			for _, d := range res.Diagnostics {
				if d.Code == diagnostics.ShadowedFilePreserved {
					count++
				}
			}
			if count != len(tc.shadowed) {
				t.Errorf("%d STEMMA1204 diagnostics, want %d: %+v", count, len(tc.shadowed), res.Diagnostics)
			}
		})
	}
}

// A byte order mark is not whitespace for Codex (Rust's str::trim) or for
// Stemma, so a BOM-only override is non-empty: it is the effective file and
// the sibling AGENTS.md stays shadowed.
func TestBOMOnlyOverrideIsNotEmpty(t *testing.T) {
	if isEmptyInstructions([]byte("\xEF\xBB\xBF")) {
		t.Fatal("a BOM-only file must not count as empty")
	}
	res, err := Importer{}.Import(context.Background(), adapters.ImportInput{
		Files: sourceFiles(t, map[string]string{
			"AGENTS.md": "# Base\n\nBASE\n", "AGENTS.override.md": "\xEF\xBB\xBF",
		}),
		IDs: canonical.NewAllocator(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Project.ContextDocuments) != 0 {
		t.Errorf("entities = %+v", res.Project.ContextDocuments)
	}
	if _, ok := res.Project.Extensions.GetString(string(canonical.TargetCodex), shadowedKeyPrefix+"AGENTS.md"); !ok {
		t.Error("AGENTS.md must be shadowed by a BOM-only override")
	}
}

// A shadowed file that is not UTF-8 cannot be preserved in canonical storage,
// so it is refused like any other non-UTF-8 file, never silently dropped.
func TestInvalidUTF8ShadowedFileBlocks(t *testing.T) {
	res, err := Importer{}.Import(context.Background(), adapters.ImportInput{
		Files: sourceFiles(t, map[string]string{
			"AGENTS.md": "# Base\n\n\xff\xfe\n", "AGENTS.override.md": "# O\n\nOVERRIDE\n",
		}),
		IDs: canonical.NewAllocator(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, d := range res.Diagnostics {
		if d.Code == diagnostics.InvalidEncoding && d.Path == "AGENTS.md" && d.Blocking {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing blocking STEMMA1004 for AGENTS.md: %+v", res.Diagnostics)
	}
	for _, blk := range res.Project.OpaqueBlocks {
		if blk.SourcePath == "AGENTS.md" && blk.Content != "" {
			t.Errorf("invalid bytes were stored: %q", blk.Content)
		}
	}
}

func assertDiagnostic(t *testing.T, ds []diagnostics.Diagnostic, code diagnostics.Code,
	sev diagnostics.Severity, path, entity string) {
	t.Helper()
	for _, d := range ds {
		if d.Code == code && d.Severity == sev && d.Path == path && d.EntityID == entity {
			return
		}
	}
	t.Errorf("missing %s (%s) for %s / %s in %+v", code, sev, path, entity, ds)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
