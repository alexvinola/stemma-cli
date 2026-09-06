package compiler_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/adapters/registry"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/discovery"
	"github.com/alexvinola/stemma-cli/internal/provenance"
)

type importFieldCase struct {
	format                canonical.TargetFormat
	path                  string
	role                  discovery.Role
	strings, lists, bools []string
	base                  string
	json                  bool
}

func importFieldCases() []importFieldCase {
	cases := []importFieldCase{
		{format: canonical.TargetCopilot, path: ".github/instructions/test.instructions.md", role: discovery.RoleScopedInstructions, strings: []string{"applyTo", "description"}},
		{format: canonical.TargetCopilot, path: ".github/prompts/test.prompt.md", role: discovery.RolePrompt, strings: []string{"name", "description"}},
		{format: canonical.TargetClaude, path: ".claude/rules/test.md", role: discovery.RoleRule, strings: []string{"description", "priority"}, lists: []string{"paths"}, bools: []string{"enabled"}},
		{format: canonical.TargetKiro, path: ".kiro/steering/test.md", role: discovery.RoleSteering, strings: []string{"inclusion", "name", "description"}, lists: []string{"fileMatchPattern"}},
		{format: canonical.TargetKiro, path: ".kiro/agents/test.json", role: discovery.RoleAgent, strings: []string{"name", "description", "prompt", "instructions", "model"}, lists: []string{"tools"}, json: true},
	}
	for _, provider := range []struct {
		format canonical.TargetFormat
		dir    string
	}{
		{canonical.TargetCopilot, ".github"}, {canonical.TargetClaude, ".claude"}, {canonical.TargetKiro, ".kiro"}, {canonical.TargetCodex, ".agents"},
	} {
		cases = append(cases, importFieldCase{format: provider.format, path: provider.dir + "/skills/test/SKILL.md", role: discovery.RoleSkill, strings: []string{"name", "description"}, lists: []string{"allowed-tools", "allowedTools", "tools"}})
		if provider.format == canonical.TargetCopilot || provider.format == canonical.TargetClaude {
			cases = append(cases, importFieldCase{format: provider.format, path: provider.dir + "/agents/test.md", role: discovery.RoleAgent, strings: []string{"name", "description", "model"}, lists: []string{"tools", "allowed-tools", "allowedTools"}})
		}
	}
	return cases
}

func importTypedFile(t *testing.T, tc importFieldCase, field, value string) (adapters.ImportResult, string) {
	t.Helper()
	content := "---\n" + tc.base + field + ": " + value + "\n---\n# Test\n\nKeep this body.\n"
	if field == "" {
		content = "# Test\n\nKeep this body.\n"
	}
	if tc.json {
		content = `{"prompt":"Keep this body.","` + field + `":` + value + `}`
		if field == "prompt" {
			content = `{"prompt":` + value + `}`
		}
		if field == "" {
			content = `{"prompt":"Keep this body."}`
		}
	}
	imp, ok := registry.Importer(tc.format)
	if !ok {
		t.Fatal("missing importer")
	}
	result, err := imp.Import(context.Background(), adapters.ImportInput{
		IDs: canonical.NewAllocator(), Files: []adapters.SourceFile{{Path: tc.path, Role: tc.role, Data: []byte(content), Hash: provenance.HashString(content)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return result, content
}

func TestImportRecognizedFieldsRejectWrongTypes(t *testing.T) {
	values := []struct{ name, value, found string }{
		{"array", `["src/**"]`, "array"}, {"object", `{nested: value}`, "object"},
		{"integer", "42", "number"}, {"decimal", "1.5", "number"}, {"boolean", "true", "boolean"}, {"null", "null", "null"},
		{"mixed-number", `["src/**", 42]`, "array (item 2: number)"},
		{"mixed-boolean", `["src/**", false]`, "array (item 2: boolean)"},
		{"mixed-null", `["src/**", null]`, "array (item 2: null)"},
		{"nested-array", `["src/**", ["nested"]]`, "array (item 2: array)"},
		{"nested-object", `["src/**", {}]`, "array (item 2: object)"},
		{"string", `"false"`, "string"},
	}
	for _, tc := range importFieldCases() {
		for _, group := range []struct {
			fields []string
			kind   string
		}{{tc.strings, "string"}, {tc.lists, "list"}, {tc.bools, "bool"}} {
			for _, field := range group.fields {
				for _, v := range values {
					if group.kind == "list" && (v.name == "array" || (v.name == "string" && !tc.json)) || group.kind == "bool" && v.name == "boolean" || group.kind == "string" && v.name == "string" {
						continue
					}
					t.Run(tc.path+"/"+field+"/"+v.name, func(t *testing.T) {
						value := v.value
						if tc.json && v.name == "object" {
							value = `{"nested":"value"}`
						}
						res, content := importTypedFile(t, tc, field, value)
						found := v.found
						if group.kind != "list" && strings.HasPrefix(found, "array") {
							found = "array"
						}
						code := diagnostics.InvalidFrontMatter
						if tc.json {
							code = diagnostics.InvalidAgentJSON
						}
						assertImportTypeError(t, res, tc.path, field, found, content, code)
						again, _ := importTypedFile(t, tc, field, value)
						if !reflect.DeepEqual(res, again) {
							t.Fatal("import is not deterministic")
						}
					})
				}
			}
		}
	}
}

func assertImportTypeError(t *testing.T, res adapters.ImportResult, path, field, found, content string, code diagnostics.Code) {
	t.Helper()
	matched := false
	for _, d := range res.Diagnostics {
		if d.Code == code && d.Blocking && d.Severity == diagnostics.SeverityError && d.Path == path && strings.Contains(d.Summary, fmt.Sprintf("%q", field)) && strings.Contains(d.Summary, "found "+found) {
			matched = true
		}
	}
	if !matched {
		t.Fatalf("missing file/key/type diagnostic: %+v", res.Diagnostics)
	}
	p := res.Project
	if len(p.ContextDocuments)+len(p.Rules)+len(p.Skills)+len(p.Agents)+len(p.Procedures) != 0 {
		t.Fatalf("invalid file created canonical entities: %+v", p)
	}
	if len(p.OpaqueBlocks) != 1 {
		t.Fatalf("expected one opaque block: %+v", p.OpaqueBlocks)
	}
	op := p.OpaqueBlocks[0]
	if op.SourcePath != path || op.Content != content || op.Hash != provenance.HashString(content) || !op.ReemitForRoundTrip {
		t.Fatalf("source was not preserved verbatim: %+v", op)
	}
}

func TestImportRecognizedFieldsPreserveMissingDefaults(t *testing.T) {
	for _, tc := range importFieldCases() {
		t.Run(tc.path, func(t *testing.T) {
			res, _ := importTypedFile(t, tc, "", "")
			if diagnostics.HasBlocking(res.Diagnostics) || len(res.Project.OpaqueBlocks) != 0 {
				t.Fatalf("missing optional metadata rejected: %+v", res)
			}
			for _, c := range res.Project.ContextDocuments {
				if c.Activation.Type != canonical.ActivationAlways {
					t.Fatal("missing scope default changed")
				}
			}
			for _, r := range res.Project.Rules {
				if r.Activation.Type != canonical.ActivationAlways || !r.Enabled || r.Priority != canonical.PriorityShould {
					t.Fatal("missing rule defaults changed")
				}
			}
		})
	}
}

func TestImportRecognizedFieldsAcceptValidTypes(t *testing.T) {
	for _, tc := range importFieldCases() {
		for _, field := range tc.lists {
			for _, value := range []string{`"src/**"`, `["src/**", "test/**"]`, `[]`} {
				if tc.json && value == `"src/**"` {
					continue
				}
				t.Run(tc.path+"/"+field+"/"+value, func(t *testing.T) {
					res, _ := importTypedFile(t, tc, field, value)
					if diagnostics.HasBlocking(res.Diagnostics) || len(res.Project.OpaqueBlocks) != 0 {
						t.Fatalf("valid metadata rejected: %+v", res)
					}
				})
			}
		}
	}
}

func TestImportValidAliasDoesNotHideInvalidToolField(t *testing.T) {
	for _, tc := range importFieldCases() {
		if tc.role != discovery.RoleSkill {
			continue
		}
		t.Run(tc.path, func(t *testing.T) {
			tc.base = "allowed-tools: [read]\n"
			res, content := importTypedFile(t, tc, "tools", "null")
			assertImportTypeError(t, res, tc.path, "tools", "null", content, diagnostics.InvalidFrontMatter)
		})
	}
}

func TestImportRecognizedFieldsKeepValidScopeAndEnabled(t *testing.T) {
	for _, tc := range []struct {
		format   canonical.TargetFormat
		path     string
		role     discovery.Role
		metadata string
	}{
		{canonical.TargetCopilot, ".github/instructions/scope.instructions.md", discovery.RoleScopedInstructions, "applyTo: \"src/**,test/**\"\ndescription: \"true\""},
		{canonical.TargetClaude, ".claude/rules/scope.md", discovery.RoleRule, "paths: [\"src/**\", \"test/**\"]\nenabled: false\npriority: must"},
		{canonical.TargetKiro, ".kiro/steering/scope.md", discovery.RoleSteering, "inclusion: fileMatch\nfileMatchPattern: [\"src/**\", \"test/**\"]"},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			imp, _ := registry.Importer(tc.format)
			data := []byte("---\n" + tc.metadata + "\n---\nScoped instruction.\n")
			res, err := imp.Import(context.Background(), adapters.ImportInput{IDs: canonical.NewAllocator(), Files: []adapters.SourceFile{{Path: tc.path, Role: tc.role, Data: data}}})
			if err != nil || diagnostics.HasBlocking(res.Diagnostics) {
				t.Fatalf("valid scope rejected: %v %+v", err, res)
			}
			var activation canonical.Activation
			if tc.format == canonical.TargetClaude {
				if len(res.Project.Rules) != 1 || res.Project.Rules[0].Enabled || res.Project.Rules[0].Priority != canonical.PriorityMust {
					t.Fatalf("valid rule changed: %+v", res.Project.Rules)
				}
				activation = res.Project.Rules[0].Activation
			} else {
				if len(res.Project.ContextDocuments) != 1 {
					t.Fatalf("missing context: %+v", res.Project)
				}
				activation = res.Project.ContextDocuments[0].Activation
			}
			if activation.Type != canonical.ActivationPathScoped || !reflect.DeepEqual(activation.Include, []string{"src/**", "test/**"}) {
				t.Fatalf("valid scope changed: %+v", activation)
			}
		})
	}
}

func TestImportUnknownMetadataKeepsOriginalTypes(t *testing.T) {
	for _, tc := range importFieldCases() {
		t.Run(tc.path, func(t *testing.T) {
			value := `[42, true, null, {nested: value}]`
			if tc.json {
				value = `[42, true, null, {"nested":"value"}]`
			}
			res, _ := importTypedFile(t, tc, "custom", value)
			if diagnostics.HasBlocking(res.Diagnostics) || len(res.Project.OpaqueBlocks) != 0 {
				t.Fatalf("unknown metadata rejected: %+v", res)
			}
			var ext canonical.Extensions
			switch {
			case len(res.Project.ContextDocuments) == 1:
				ext = res.Project.ContextDocuments[0].Extensions
			case len(res.Project.Rules) == 1:
				ext = res.Project.Rules[0].Extensions
			case len(res.Project.Procedures) == 1:
				ext = res.Project.Procedures[0].Extensions
			case len(res.Project.Skills) == 1:
				ext = res.Project.Skills[0].Extensions
			case len(res.Project.Agents) == 1:
				ext = res.Project.Agents[0].Extensions
			default:
				t.Fatal("missing entity")
			}
			var number any = int64(42)
			if tc.json {
				number = float64(42)
			}
			if !reflect.DeepEqual(ext[string(tc.format)]["custom"], []any{number, true, nil, map[string]any{"nested": "value"}}) {
				t.Fatalf("unknown metadata changed: %#v", ext)
			}
		})
	}
}
