package store

import (
	"reflect"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/parser"
)

type entityDecoder func(string, string, []byte) (any, []diagnostics.Diagnostic)

func decoderOf[T any](decode func(string, string, []byte) (T, []diagnostics.Diagnostic)) entityDecoder {
	return func(id, path string, data []byte) (any, []diagnostics.Diagnostic) { return decode(id, path, data) }
}

func entityDecoders() []struct {
	name   string
	decode entityDecoder
} {
	return []struct {
		name   string
		decode entityDecoder
	}{
		{"context", decoderOf(DecodeContext)}, {"rule", decoderOf(DecodeRule)},
		{"procedure", decoderOf(DecodeProcedure)}, {"skill", decoderOf(DecodeSkill)},
		{"agent", decoderOf(DecodeAgent)}, {"decision", decoderOf(DecodeDecision)},
	}
}

func TestEntityDecodersRejectMissingFrontMatter(t *testing.T) {
	for _, dec := range entityDecoders() {
		for _, input := range []struct{ name, content string }{
			{"empty", ""}, {"plain", "Plain text, no front matter.\n"}, {"unterminated", "---\ntitle: unfinished\n"}, {"oversized", "---\n" + strings.Repeat("#", parser.MaxFrontMatterBytes+1) + "\n---\nbody"},
		} {
			t.Run(dec.name+"/"+input.name, func(t *testing.T) {
				path := ".stemma/" + dec.name + "/broken.md"
				_, diags := dec.decode(dec.name+".broken", path, []byte(input.content))
				assertBlockingField(t, diags, path, "front matter")
			})
		}
	}
}

func assertBlockingField(t *testing.T, diags []diagnostics.Diagnostic, path, field string) {
	t.Helper()
	for _, d := range diags {
		if d.Blocking && d.Severity == diagnostics.SeverityError && d.Path == path && strings.Contains(d.Summary, field) {
			return
		}
	}
	t.Fatalf("no blocking diagnostic naming %q at %s: %+v", field, path, diags)
}

func TestEntityDecodersRejectWrongFieldTypes(t *testing.T) {
	for _, dec := range entityDecoders() {
		fields := []string{"extensions: []", "extensions:\n  claude: []"}
		switch dec.name {
		case "context":
			fields = append(fields, "title: []", "kind: {}", "audience: 12", "enabled: maybe", "activation: []", "activation:\n  type: always\n  exclude: [true]")
		case "rule":
			fields = append(fields, "title: {}", "priority: []", "enabled: 12", "activation:\n  type: on-demand\n  trigger: []", "activation:\n  type: path-scoped\n  include: [12]", "activation:\n  type: always\n  invocationName: true")
		case "procedure":
			fields = append(fields, "name: []", "description: {}", "trigger: false", "enabled: null")
		case "skill":
			fields = append(fields, "name: {}", "description: []", "allowedTools: [read, true]", "allowedTools: {}", "invocationPolicy: []", "enabled: []")
		case "agent":
			fields = append(fields, "name: []", "description: {}", "tools: [read, {}]", "modelPreference: 12", "enabled: {}")
		case "decision":
			fields = append(fields, "title: []", "status: {}")
		}
		for _, field := range fields {
			t.Run(dec.name+"/"+field, func(t *testing.T) {
				path := ".stemma/" + dec.name + "/broken.md"
				_, diags := dec.decode(dec.name+".broken", path, []byte("---\n"+field+"\n---\nBody.\n"))
				assertBlockingField(t, diags, path, strings.Split(field, ":")[0])
			})
		}
	}
}

func TestEntityDecoderPreservesOptionalDefaultsAndStringShorthand(t *testing.T) {
	input := []byte("---\nname: review\nallowedTools: read\nextensions:\n  claude:\n    custom: [true, 12, null]\n---\nReview changes.\n")
	skill, diags := DecodeSkill("skill.review", ".stemma/skills/review.md", input)
	if len(diags) != 0 || skill.Enabled != nil || !reflect.DeepEqual(skill.AllowedTools, []string{"read"}) {
		t.Fatalf("unexpected decode: %+v, %+v", skill, diags)
	}
	if !reflect.DeepEqual(skill.Extensions["claude"]["custom"], []any{true, int64(12), nil}) {
		t.Fatalf("extension values changed: %#v", skill.Extensions)
	}
}

func TestCanonicalRecognizedFieldsReportFoundTypes(t *testing.T) {
	for _, dec := range entityDecoders() {
		stringsByEntity := map[string][]string{
			"context": {"title", "kind", "audience"}, "rule": {"title", "priority"},
			"procedure": {"name", "description", "trigger"}, "skill": {"name", "description", "invocationPolicy"},
			"agent": {"name", "description", "modelPreference"}, "decision": {"title", "status"},
		}
		fields := append([]string{}, stringsByEntity[dec.name]...)
		fields = append(fields, "extensions", "extensions.provider")
		if dec.name != "decision" {
			fields = append(fields, "enabled")
		}
		if dec.name == "skill" {
			fields = append(fields, "allowedTools")
		}
		if dec.name == "agent" {
			fields = append(fields, "tools")
		}
		if dec.name == "context" || dec.name == "rule" {
			fields = append(fields, "activation", "activation.type", "activation.include", "activation.exclude", "activation.trigger", "activation.invocationName")
		}
		for _, field := range fields {
			for _, value := range []struct{ name, yaml, found string }{
				{"array", `["read"]`, "array"}, {"object", "{}", "object"}, {"number", "42", "number"},
				{"boolean", "false", "boolean"}, {"null", "null", "null"}, {"string", `"false"`, "string"},
				{"mixed-null", `["read", null]`, "array"},
			} {
				isList := field == "allowedTools" || field == "tools" || field == "activation.include" || field == "activation.exclude"
				isMap := field == "extensions" || field == "extensions.provider" || field == "activation"
				isBool := field == "enabled"
				if isList && (value.name == "array" || value.name == "string") || isMap && value.name == "object" || isBool && value.name == "boolean" || !isList && !isMap && !isBool && value.name == "string" {
					continue
				}
				t.Run(dec.name+"/"+field+"/"+value.name, func(t *testing.T) {
					front := "activation: {type: always}\n"
					key := strings.Split(field, ".")
					entry := field + ": " + value.yaml + "\n"
					if len(key) == 2 {
						entry = key[0] + ":\n"
						if key[0] == "activation" && key[1] != "type" {
							entry += "  type: always\n"
						}
						entry += "  " + key[1] + ": " + value.yaml + "\n"
					}
					if key[0] == "activation" {
						front = ""
					}
					path := ".stemma/" + dec.name + "/broken.md"
					_, diags := dec.decode(dec.name+".broken", path, []byte("---\n"+front+entry+"---\nBody.\n"))
					assertBlockingField(t, diags, path, field)
					found := false
					for _, d := range diags {
						if d.Blocking && strings.Contains(d.Summary, field) && strings.Contains(d.Summary, "found "+value.found) {
							found = true
						}
					}
					if !found {
						t.Fatalf("missing found type: %+v", diags)
					}
				})
			}
		}
	}
}
