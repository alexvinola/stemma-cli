package compiler_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/discovery"
	"github.com/alexvinola/stemma-cli/internal/provenance"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// Exercise discovery and compiler validation as well as the exhaustive adapter
// field-type tables. No fixture directory is needed for these rejection cases.
func TestCompilerImportRejectsWrongTypesForEveryRole(t *testing.T) {
	for _, tc := range importFieldCases() {
		t.Run(tc.path, func(t *testing.T) {
			content := rejectedFieldContent(tc)
			res := importRejectedFiles(t, tc.format, workspace.WriteOp{Path: tc.path, Content: []byte(content), Mode: 0o644})
			code := diagnostics.InvalidFrontMatter
			if tc.json {
				code = diagnostics.InvalidAgentJSON
			}
			assertCompilerImportTypeError(t, res, tc.path, tc.strings[0], "number", content, code)
			matches := res.Scan.Files(tc.format)
			if len(matches) != 1 || matches[0].Path != tc.path || matches[0].Role != tc.role {
				t.Fatalf("wrong discovery role: %+v", matches)
			}
		})
	}
}

func TestCompilerImportReportsEveryInvalidFieldOncePerFile(t *testing.T) {
	for _, tc := range []struct {
		name, path, content string
		format              canonical.TargetFormat
		code                diagnostics.Code
		fields              []struct{ name, found string }
	}{
		{
			name: "YAML", path: ".claude/rules/scoped.md", format: canonical.TargetClaude,
			content: "---\npaths: [\"src/**\", null]\nenabled: []\npriority: true\n---\nKeep this body.\n",
			code:    diagnostics.InvalidFrontMatter,
			fields:  []struct{ name, found string }{{"paths", "array (item 2: null)"}, {"enabled", "array"}, {"priority", "boolean"}},
		},
		{
			name: "Kiro JSON", path: ".kiro/agents/reviewer.json", format: canonical.TargetKiro,
			content: `{"prompt":"Keep this body.","name":null,"model":false,"tools":["read",null]}`,
			code:    diagnostics.InvalidAgentJSON,
			fields:  []struct{ name, found string }{{"name", "null"}, {"model", "boolean"}, {"tools", "array (item 2: null)"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := importRejectedFiles(t, tc.format, workspace.WriteOp{Path: tc.path, Content: []byte(tc.content), Mode: 0o644})
			for _, field := range tc.fields {
				assertCompilerImportTypeError(t, res, tc.path, field.name, field.found, tc.content, tc.code)
			}
			var count int
			for _, d := range res.Diagnostics {
				if d.Code == tc.code {
					count++
				}
			}
			if count != len(tc.fields) {
				t.Fatalf("want one type diagnostic per invalid field, got %+v", res.Diagnostics)
			}
		})
	}
}

func TestCompilerRejectedOpaqueProjectionAcrossProviders(t *testing.T) {
	// Skills provide a rejected standalone file in each of the four providers.
	for _, tc := range importFieldCases() {
		if tc.role != discovery.RoleSkill {
			continue
		}
		t.Run(string(tc.format), func(t *testing.T) {
			content := rejectedFieldContent(tc)
			res := importRejectedFiles(t, tc.format, workspace.WriteOp{Path: tc.path, Content: []byte(content), Mode: 0o644})
			assertCompilerImportTypeError(t, res, tc.path, tc.strings[0], "number", content, diagnostics.InvalidFrontMatter)
			for _, target := range []canonical.TargetFormat{canonical.TargetClaude, canonical.TargetCodex, canonical.TargetCopilot, canonical.TargetKiro} {
				t.Run(string(target), func(t *testing.T) {
					out, err := compiler.Compile(context.Background(), res.Project, compiler.CompileOptions{Target: target})
					if err != nil || diagnostics.HasBlocking(out.Diagnostics) || len(out.Files) != 0 {
						t.Fatalf("opaque-only compile: %v %+v", err, out)
					}
					assertProjectionInvariants(t, res.Project, out)
					if len(out.Mappings) != 1 {
						t.Fatalf("want one opaque mapping: %+v", out.Mappings)
					}
					m := out.Mappings[0]
					if m.EntityID != res.Project.OpaqueBlocks[0].ID || m.EntityType != canonical.EntityOpaque || m.Target != target || len(m.Files) != 0 || m.Activation.Type != canonical.ActivationDocumentationOnly {
						t.Fatalf("unexpected opaque mapping: %+v", m)
					}
					if target != tc.format {
						if m.Outcome != adapters.OutcomeSkipped || len(m.Diagnostics) != 0 {
							t.Fatalf("foreign opaque content must be explicitly skipped: %+v", m)
						}
						// Import enables only the source provider in Project.Targets.
						if len(out.Diagnostics) != 1 {
							t.Fatalf("want only the target-not-enabled warning: %+v", out.Diagnostics)
						}
						d := out.Diagnostics[0]
						if d.Code != diagnostics.TargetNotEnabled || d.Severity != diagnostics.SeverityWarning || d.Blocking || d.Target != string(target) {
							t.Fatalf("unexpected foreign-target diagnostic: %+v", d)
						}
						return
					}
					if m.Outcome != adapters.OutcomeLossy || len(m.Diagnostics) != 1 || len(out.Diagnostics) != 1 {
						t.Fatalf("own-provider opaque content must report loss: %+v", out)
					}
					d := out.Diagnostics[0]
					if d.Code != diagnostics.OpaqueNotReemitted || d.Severity != diagnostics.SeverityWarning || d.Path != tc.path || d.Fingerprint != m.Diagnostics[0] {
						t.Fatalf("missing linked opaque diagnostic: %+v", d)
					}
				})
			}
		})
	}
}

func TestCompilerRejectedGlobsPreserveValidSibling(t *testing.T) {
	oversized := "src/" + strings.Repeat("{a,b}", 11) + "/{ok,..}/**"
	for _, tc := range []struct {
		name, path, header, pattern string
		format                      canonical.TargetFormat
		code                        diagnostics.Code
	}{
		{"Claude expansion", ".claude/rules/rejected.md", "paths", oversized, canonical.TargetClaude, diagnostics.GlobExpansionLimit},
		{"Copilot expansion", ".github/instructions/rejected.instructions.md", "applyTo", oversized, canonical.TargetCopilot, diagnostics.GlobExpansionLimit},
		{"Kiro expansion", ".kiro/steering/rejected.md", "inclusion: fileMatch\nfileMatchPattern", oversized, canonical.TargetKiro, diagnostics.GlobExpansionLimit},
		{"Copilot corrupted", ".github/instructions/rejected.instructions.md", "applyTo", "src/**/*.{ts,tsx", canonical.TargetCopilot, diagnostics.InvalidGlob},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const rejectedBody = "REJECTED SCOPE MUST NOT BECOME ALWAYS ON"
			const keptBody = "Keep this sibling instruction."
			sibling := strings.Replace(tc.path, "rejected", "valid", 1)
			res := importRejectedFiles(t, tc.format,
				workspace.WriteOp{Path: tc.path, Content: []byte("---\n" + tc.header + ": \"" + tc.pattern + "\"\n---\n" + rejectedBody + "\n"), Mode: 0o644},
				workspace.WriteOp{Path: sibling, Content: []byte("---\n" + tc.header + ": \"safe/**\"\n---\n" + keptBody + "\n"), Mode: 0o644},
			)
			if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != tc.code || !res.Diagnostics[0].Blocking || res.Diagnostics[0].Severity != diagnostics.SeverityError || res.Diagnostics[0].Path != tc.path {
				t.Fatalf("missing precise rejection diagnostic: %+v", res.Diagnostics)
			}
			if len(res.Project.Entities()) != 1 || len(res.Project.OpaqueBlocks) != 0 {
				t.Fatalf("only the valid sibling should create an entity: %+v", res.Project)
			}
			var activation canonical.Activation
			var body, source string
			if tc.format == canonical.TargetClaude {
				if len(res.Project.Rules) != 1 {
					t.Fatalf("missing sibling rule: %+v", res.Project)
				}
				r := res.Project.Rules[0]
				activation, body, source = r.Activation, r.Instruction, r.Provenance.SourcePath
			} else {
				if len(res.Project.ContextDocuments) != 1 {
					t.Fatalf("missing sibling context: %+v", res.Project)
				}
				d := res.Project.ContextDocuments[0]
				activation, body, source = d.Activation, d.Content, d.Provenance.SourcePath
			}
			if source != sibling || strings.TrimSpace(body) != keptBody || !reflect.DeepEqual(activation, canonical.PathScoped([]string{"safe/**"}, nil)) {
				t.Fatalf("sibling content or scope changed: source=%s body=%q activation=%+v", source, body, activation)
			}
		})
	}
}

func rejectedFieldContent(tc importFieldCase) string {
	if tc.json {
		return `{"prompt":"Keep this body.","` + tc.strings[0] + `":42}`
	}
	return "---\n" + tc.strings[0] + ": 42\n---\nKeep this body.\n"
}

func assertCompilerImportTypeError(t *testing.T, res compiler.ImportResult, path, field, found, content string, code diagnostics.Code) {
	t.Helper()
	assertImportTypeError(t, adapters.ImportResult{Project: res.Project, Diagnostics: res.Diagnostics}, path, field, found, content, code)
	if len(res.Project.Entities()) != 0 || res.Project.OpaqueBlocks[0].Provider != string(res.Format) {
		t.Fatalf("rejected source acquired entities or the wrong provider: %+v", res.Project)
	}
	var preserved int
	for _, d := range res.Diagnostics {
		if d.Code == diagnostics.OpaqueBlockKept && d.Path == path {
			preserved++
		}
	}
	if preserved != 1 {
		t.Fatalf("want one compiler-visible preservation diagnostic: %+v", res.Diagnostics)
	}
}

func importRejectedFiles(t *testing.T, format canonical.TargetFormat, files ...workspace.WriteOp) compiler.ImportResult {
	t.Helper()
	ws, err := workspace.Open(t.TempDir(), workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	tx := ws.Begin()
	for _, file := range files {
		if err := tx.Add(file); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	before := rejectedImportTree(t, ws.Root())
	res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{Format: format, ProjectID: "prj_rejected", ProjectName: "Rejected input"})
	if after := rejectedImportTree(t, ws.Root()); !reflect.DeepEqual(before, after) {
		t.Fatal("compiler.Import changed workspace paths, bytes, permissions, or modification times")
	}
	if err != nil {
		t.Fatalf("import should return diagnostics: %v", err)
	}
	if res.Format != format || len(res.Sources) != len(files) {
		t.Fatalf("import lost source records: %+v", res)
	}
	for _, file := range files {
		var found bool
		for _, source := range res.Sources {
			if source.Path == file.Path && source.Hash == provenance.HashBytes(file.Content) && source.Format == string(format) {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing source record for %s: %+v", file.Path, res.Sources)
		}
	}
	return res
}

type rejectedImportEntry struct {
	content string
	mode    fs.FileMode
	mtime   time.Time
}

func rejectedImportTree(t *testing.T, root string) map[string]rejectedImportEntry {
	t.Helper()
	tree := map[string]rejectedImportEntry{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entry := rejectedImportEntry{mode: info.Mode(), mtime: info.ModTime()}
		if !d.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entry.content = string(data)
		}
		tree[path] = entry
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}
