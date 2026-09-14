package store

import (
	"strings"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/parser"
)

// decoded is the shared result of reading one entity file.
type decoded struct {
	doc      parser.Document
	main     string
	sections map[string]string
	lists    map[string][]string
	diags    []diagnostics.Diagnostic
}

// decodeFile parses an entity file and splits its body.
func decodeFile(path string, data []byte) decoded {
	doc := parser.Parse(path, data)
	if doc.FrontMatter == nil && !diagnostics.HasBlocking(doc.Diagnostics) {
		doc.Diagnostics = append(doc.Diagnostics, diagf(path, "entity file requires a front matter block delimited by '---'"))
	}
	main, sections, lists := splitBody(doc)
	return decoded{doc: doc, main: main, sections: sections, lists: lists, diags: doc.Diagnostics}
}

// field distinguishes an absent field from a present value of the wrong type.
// Reading a nil map is safe in Go; dereferencing a nil *FrontMatter is not.
func (d decoded) field(key string) (any, bool) {
	if d.doc.FrontMatter == nil {
		return nil, false
	}
	v, ok := d.doc.FrontMatter.Fields[key]
	return v, ok
}

func (d *decoded) invalidType(key, want string, raw any) {
	d.diags = append(d.diags, diagnostics.New(diagnostics.InvalidFrontMatter, diagnostics.SeverityError,
		"front matter "+parser.TypeMismatch(key, want, raw)).WithPath(d.doc.Path))
}

func (d *decoded) str(key string) string {
	raw, exists := d.field(key)
	if !exists {
		return ""
	}
	v, ok := raw.(string)
	if !ok {
		d.invalidType(key, "a string", raw)
		return ""
	}
	return strings.TrimSpace(v)
}

func (d *decoded) boolPtr(key string) *bool {
	raw, exists := d.field(key)
	if !exists {
		return nil
	}
	v, ok := raw.(bool)
	if !ok {
		d.invalidType(key, "a boolean", raw)
		return nil
	}
	return &v
}

func (d *decoded) list(key string) []string {
	raw, exists := d.field(key)
	if !exists {
		return nil
	}
	v, ok := stringList(raw)
	if !ok {
		d.invalidType(key, "a string or a list of strings", raw)
		return nil
	}
	return v
}

func (d decoded) activation(path string) (canonical.Activation, []diagnostics.Diagnostic) {
	if d.doc.FrontMatter == nil {
		return canonical.Activation{}, nil
	}
	raw, ok := d.field("activation")
	if !ok {
		return canonical.Activation{}, []diagnostics.Diagnostic{
			diagf(path, "entity file is missing the required \"activation\" front matter"),
		}
	}
	a, err := parseActivation(raw, path)
	if err != nil {
		return canonical.Activation{}, []diagnostics.Diagnostic{
			diagnostics.New(diagnostics.InvalidActivation, diagnostics.SeverityError, err.Error()).WithPath(path),
		}
	}
	return a, nil
}

func (d *decoded) extensions() canonical.Extensions {
	raw, exists := d.field("extensions")
	if !exists {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		d.invalidType("extensions", "a mapping", raw)
		return nil
	}
	// Sorting keeps diagnostics stable even when several provider entries are invalid.
	for _, provider := range kvSorted(m) {
		if _, ok := m[provider].(map[string]any); !ok {
			d.invalidType("extensions."+provider, "a mapping", m[provider])
		}
	}
	return parseExtensions(m)
}

// DecodeContext reads a context document file.
func DecodeContext(id, path string, data []byte) (canonical.ContextDocument, []diagnostics.Diagnostic) {
	d := decodeFile(path, data)
	activation, adiags := d.activation(path)
	e := canonical.ContextDocument{
		ID:         id,
		Title:      d.str("title"),
		Kind:       canonical.ContextKind(d.str("kind")),
		Content:    d.main,
		Audience:   canonical.Audience(d.str("audience")),
		Activation: activation,
		Enabled:    d.boolPtr("enabled"),
		Extensions: d.extensions(),
	}
	if e.Kind == "" {
		e.Kind = canonical.KindOther
	}
	if e.Audience == "" {
		e.Audience = canonical.AudienceAgent
	}
	return e, append(d.diags, adiags...)
}

// DecodeRule reads a rule file.
func DecodeRule(id, path string, data []byte) (canonical.Rule, []diagnostics.Diagnostic) {
	d := decodeFile(path, data)
	activation, adiags := d.activation(path)
	enabled := true
	if v := d.boolPtr("enabled"); v != nil {
		enabled = *v
	}
	priority := canonical.Priority(d.str("priority"))
	if priority == "" {
		priority = canonical.PriorityShould
	}
	e := canonical.Rule{
		ID:           id,
		Title:        d.str("title"),
		Instruction:  d.main,
		Priority:     priority,
		Enabled:      enabled,
		Activation:   activation,
		Rationale:    d.sections[sectionRationale],
		GoodExamples: d.lists[sectionGoodExamples],
		BadExamples:  d.lists[sectionBadExamples],
		Extensions:   d.extensions(),
	}
	return e, append(d.diags, adiags...)
}

// DecodeProcedure reads a procedure file.
func DecodeProcedure(id, path string, data []byte) (canonical.Procedure, []diagnostics.Diagnostic) {
	d := decodeFile(path, data)
	e := canonical.Procedure{
		ID:          id,
		Name:        d.str("name"),
		Description: d.str("description"),
		Trigger:     d.str("trigger"),
		Content:     d.main,
		Enabled:     d.boolPtr("enabled"),
		Extensions:  d.extensions(),
	}
	return e, d.diags
}

// DecodeSkill reads a skill file.
func DecodeSkill(id, path string, data []byte) (canonical.Skill, []diagnostics.Diagnostic) {
	d := decodeFile(path, data)
	e := canonical.Skill{
		ID:               id,
		Name:             d.str("name"),
		Description:      d.str("description"),
		Content:          d.main,
		AllowedTools:     d.list("allowedTools"),
		InvocationPolicy: d.str("invocationPolicy"),
		Enabled:          d.boolPtr("enabled"),
		Extensions:       d.extensions(),
	}
	return e, d.diags
}

// DecodeAgent reads a specialist agent file.
func DecodeAgent(id, path string, data []byte) (canonical.Agent, []diagnostics.Diagnostic) {
	d := decodeFile(path, data)
	e := canonical.Agent{
		ID:              id,
		Name:            d.str("name"),
		Description:     d.str("description"),
		Instructions:    d.main,
		Tools:           d.list("tools"),
		ModelPreference: d.str("modelPreference"),
		Enabled:         d.boolPtr("enabled"),
		Extensions:      d.extensions(),
	}
	return e, d.diags
}

// DecodeDecision reads an architecture decision file.
func DecodeDecision(id, path string, data []byte) (canonical.Decision, []diagnostics.Diagnostic) {
	d := decodeFile(path, data)
	status := canonical.DecisionStatus(d.str("status"))
	if status == "" {
		status = canonical.DecisionProposed
	}
	e := canonical.Decision{
		ID:               id,
		Title:            d.str("title"),
		Status:           status,
		Context:          d.sections[sectionContext],
		Decision:         d.sections[sectionDecision],
		Consequences:     d.sections[sectionConsequences],
		AgentConstraints: d.lists[sectionConstraints],
		Extensions:       d.extensions(),
	}
	return e, d.diags
}
