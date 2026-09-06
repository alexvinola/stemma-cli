package parser

import (
	"fmt"

	"github.com/alexvinola/stemma-cli/internal/diagnostics"
)

// FieldType describes the shapes a consumer understands, without coercion.
type FieldType string

const (
	StringField     FieldType = "a string"
	BoolField       FieldType = "a boolean"
	StringListField FieldType = "a string or a list of strings"
	ListField       FieldType = "a list of strings"
)

// FieldSpec declares a recognized field. Missing fields retain the consumer's
// existing defaults; present fields (including null) must match their type.
type FieldSpec struct {
	Key  string
	Type FieldType
}

// Accepts tests the decoded value, including every member of a string list.
func (t FieldType) Accepts(value any) bool {
	switch t {
	case StringField:
		_, ok := value.(string)
		return ok
	case BoolField:
		_, ok := value.(bool)
		return ok
	case StringListField, ListField:
		if _, ok := value.(string); ok && t == StringListField {
			return true
		}
		list, ok := value.([]any)
		if !ok {
			return false
		}
		for _, item := range list {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	}
	return false
}

// ValueType names parsed YAML/JSON types without printing untrusted values.
func ValueType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case int64, float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", value)
	}
}

// TypeMismatch includes the offending member's type for malformed string lists.
func TypeMismatch(key, want string, value any) string {
	message := fmt.Sprintf("field %q must be %s; found %s", key, want, ValueType(value))
	if list, ok := value.([]any); ok && (want == string(StringListField) || want == string(ListField)) {
		for i, item := range list {
			if _, ok := item.(string); !ok {
				return fmt.Sprintf("%s (item %d: %s)", message, i+1, ValueType(item))
			}
		}
	}
	return message
}

// ValidateFields distinguishes missing, valid and invalid recognized fields.
// Specs are inspected in caller order so diagnostics never depend on map order.
func (f *FrontMatter) ValidateFields(path string, specs ...FieldSpec) []diagnostics.Diagnostic {
	var diags []diagnostics.Diagnostic
	for _, spec := range specs {
		if !f.Has(spec.Key) {
			continue
		}
		value := f.Fields[spec.Key]
		if !spec.Type.Accepts(value) {
			diags = append(diags, diagnostics.New(diagnostics.InvalidFrontMatter, diagnostics.SeverityError,
				"front matter "+TypeMismatch(spec.Key, string(spec.Type), value)).
				WithPath(path).WithPosition(f.StartLine, 1))
		}
	}
	return diags
}
