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
	main, sections, lists := splitBody(doc)
	return decoded{doc: doc, main: main, sections: sections, lists: lists, diags: doc.Diagnostics}
}

func (d decoded) str(key string) string {
	v, _ := d.doc.FrontMatter.String(key)
	return strings.TrimSpace(v)
}

func (d decoded) boolPtr(key string) *bool {
	v, ok := d.doc.FrontMatter.Bool(key)
	if !ok {
		return nil
	}
	return &v
}

func (d decoded) list(key string) []string {
	v, ok := d.doc.FrontMatter.StringList(key)
	if !ok {
		return nil
	}
	return v
}

func (d decoded) activation(path string) (canonical.Activation, []diagnostics.Diagnostic) {
	raw, ok := d.doc.FrontMatter.Fields["activation"]
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

func (d decoded) extensions() canonical.Extensions {
	return parseExtensions(d.doc.FrontMatter.Fields["extensions"])
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
	return canonical.Procedure{
		ID:          id,
		Name:        d.str("name"),
		Description: d.str("description"),
		Trigger:     d.str("trigger"),
		Content:     d.main,
		Enabled:     d.boolPtr("enabled"),
		Extensions:  d.extensions(),
	}, d.diags
}

// DecodeSkill reads a skill file.
func DecodeSkill(id, path string, data []byte) (canonical.Skill, []diagnostics.Diagnostic) {
	d := decodeFile(path, data)
	return canonical.Skill{
		ID:               id,
		Name:             d.str("name"),
		Description:      d.str("description"),
		Content:          d.main,
		AllowedTools:     d.list("allowedTools"),
		InvocationPolicy: d.str("invocationPolicy"),
		Enabled:          d.boolPtr("enabled"),
		Extensions:       d.extensions(),
	}, d.diags
}

// DecodeAgent reads a specialist agent file.
func DecodeAgent(id, path string, data []byte) (canonical.Agent, []diagnostics.Diagnostic) {
	d := decodeFile(path, data)
	return canonical.Agent{
		ID:              id,
		Name:            d.str("name"),
		Description:     d.str("description"),
		Instructions:    d.main,
		Tools:           d.list("tools"),
		ModelPreference: d.str("modelPreference"),
		Enabled:         d.boolPtr("enabled"),
		Extensions:      d.extensions(),
	}, d.diags
}

// DecodeDecision reads an architecture decision file.
func DecodeDecision(id, path string, data []byte) (canonical.Decision, []diagnostics.Diagnostic) {
	d := decodeFile(path, data)
	status := canonical.DecisionStatus(d.str("status"))
	if status == "" {
		status = canonical.DecisionProposed
	}
	return canonical.Decision{
		ID:               id,
		Title:            d.str("title"),
		Status:           status,
		Context:          d.sections[sectionContext],
		Decision:         d.sections[sectionDecision],
		Consequences:     d.sections[sectionConsequences],
		AgentConstraints: d.lists[sectionConstraints],
		Extensions:       d.extensions(),
	}, d.diags
}
