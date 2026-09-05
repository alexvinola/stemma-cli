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
