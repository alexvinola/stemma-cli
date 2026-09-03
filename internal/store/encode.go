package store

import (
	"strings"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
)

// EncodeContext renders a context document as an editable Markdown file.
func EncodeContext(e canonical.ContextDocument) string {
	entries := []adapters.KV{
		{Key: "title", Value: e.Title},
		{Key: "kind", Value: string(e.Kind)},
		{Key: "audience", Value: string(e.Audience)},
		{Key: "activation", Value: renderActivation(e.Activation)},
	}
	if e.Enabled != nil {
		entries = append(entries, adapters.KV{Key: "enabled", Value: *e.Enabled})
	}
	if ext, ok := extensionsValue(e.Extensions); ok {
		entries = append(entries, adapters.KV{Key: "extensions", Value: ext})
	}
	var b body
	b.main(e.Content)
	return adapters.RenderFrontMatter(entries) + b.String()
}

// EncodeRule renders a rule. Rationale and examples become body sections so
// that they stay readable and stay clearly separate from the instruction,
// which is the only part an agent ever sees.
func EncodeRule(e canonical.Rule) string {
	entries := []adapters.KV{
		{Key: "title", Value: e.Title},
		{Key: "priority", Value: string(e.Priority)},
		{Key: "enabled", Value: e.Enabled},
		{Key: "activation", Value: renderActivation(e.Activation)},
	}
	if ext, ok := extensionsValue(e.Extensions); ok {
		entries = append(entries, adapters.KV{Key: "extensions", Value: ext})
	}
	var b body
	b.main(e.Instruction)
	b.section(sectionRationale, e.Rationale)
	b.bullets(sectionGoodExamples, e.GoodExamples)
	b.bullets(sectionBadExamples, e.BadExamples)
	return adapters.RenderFrontMatter(entries) + b.String()
}

// EncodeProcedure renders a procedure.
func EncodeProcedure(e canonical.Procedure) string {
	entries := []adapters.KV{{Key: "name", Value: e.Name}}
	if e.Description != "" {
		entries = append(entries, adapters.KV{Key: "description", Value: e.Description})
	}
	if e.Trigger != "" {
		entries = append(entries, adapters.KV{Key: "trigger", Value: e.Trigger})
	}
	if e.Enabled != nil {
		entries = append(entries, adapters.KV{Key: "enabled", Value: *e.Enabled})
	}
	if ext, ok := extensionsValue(e.Extensions); ok {
		entries = append(entries, adapters.KV{Key: "extensions", Value: ext})
	}
	var b body
	b.main(e.Content)
	return adapters.RenderFrontMatter(entries) + b.String()
}

// EncodeSkill renders a skill.
func EncodeSkill(e canonical.Skill) string {
	entries := []adapters.KV{{Key: "name", Value: e.Name}}
	if e.Description != "" {
		entries = append(entries, adapters.KV{Key: "description", Value: e.Description})
	}
	if len(e.AllowedTools) > 0 {
		entries = append(entries, adapters.KV{Key: "allowedTools", Value: anyList(e.AllowedTools)})
	}
	if e.InvocationPolicy != "" {
		entries = append(entries, adapters.KV{Key: "invocationPolicy", Value: e.InvocationPolicy})
	}
	if e.Enabled != nil {
		entries = append(entries, adapters.KV{Key: "enabled", Value: *e.Enabled})
	}
	if ext, ok := extensionsValue(e.Extensions); ok {
		entries = append(entries, adapters.KV{Key: "extensions", Value: ext})
	}
	var b body
	b.main(e.Content)
	return adapters.RenderFrontMatter(entries) + b.String()
}

// EncodeAgent renders a specialist agent.
func EncodeAgent(e canonical.Agent) string {
	entries := []adapters.KV{{Key: "name", Value: e.Name}}
	if e.Description != "" {
		entries = append(entries, adapters.KV{Key: "description", Value: e.Description})
	}
	if len(e.Tools) > 0 {
		entries = append(entries, adapters.KV{Key: "tools", Value: anyList(e.Tools)})
	}
	if e.ModelPreference != "" {
		entries = append(entries, adapters.KV{Key: "modelPreference", Value: e.ModelPreference})
	}
	if e.Enabled != nil {
		entries = append(entries, adapters.KV{Key: "enabled", Value: *e.Enabled})
	}
	if ext, ok := extensionsValue(e.Extensions); ok {
		entries = append(entries, adapters.KV{Key: "extensions", Value: ext})
	}
	var b body
	b.main(e.Instructions)
	return adapters.RenderFrontMatter(entries) + b.String()
}

// EncodeDecision renders an architecture decision record. Everything except
// the agent constraints is human documentation, so it lives in body sections.
func EncodeDecision(e canonical.Decision) string {
	entries := []adapters.KV{
		{Key: "title", Value: e.Title},
		{Key: "status", Value: string(e.Status)},
	}
	if ext, ok := extensionsValue(e.Extensions); ok {
		entries = append(entries, adapters.KV{Key: "extensions", Value: ext})
	}
	var b body
	b.section(sectionContext, e.Context)
	b.section(sectionDecision, e.Decision)
	b.section(sectionConsequences, e.Consequences)
	b.bullets(sectionConstraints, e.AgentConstraints)
	out := adapters.RenderFrontMatter(entries) + b.String()
	if strings.TrimSpace(b.String()) == "" {
		// A record with no prose still needs a body, or the file would be only
		// front matter and read as empty.
		out += "\n"
	}
	return out
}
