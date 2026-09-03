package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"path"
	"sort"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/provenance"
	"github.com/alexvinola/stemma-cli/internal/tokenestimate"
)

// Exporter renders canonical entities as Kiro configuration.
type Exporter struct{}

// Format implements adapters.Exporter.
func (Exporter) Format() canonical.TargetFormat { return canonical.TargetKiro }

// Export implements adapters.Exporter.
func (Exporter) Export(ctx context.Context, in adapters.ExportInput) (adapters.ExportResult, error) {
	b := adapters.NewBuilder(canonical.TargetKiro, in)

	for _, doc := range in.Project.ContextDocuments {
		if err := ctx.Err(); err != nil {
			return adapters.ExportResult{}, err
		}
		res := b.ResolveContext(doc)
		if !res.Included {
			b.Skip(doc.ID, canonical.EntityContext, res, doc.Provenance)
			b.CountDocumentationOnly(doc.Content)
			continue
		}
		exportSteering(b, doc.ID, canonical.EntityContext, doc.Title, doc.Extensions, res, doc.Provenance)
	}

	for _, rule := range in.Project.Rules {
		res := b.Resolve(rule.ID, rule.Enabled, rule.Activation, ruleBody(rule, b))
		b.CountDocumentationOnly(rule.Rationale + strings.Join(rule.GoodExamples, "\n") + strings.Join(rule.BadExamples, "\n"))
		if !res.Included {
			b.Skip(rule.ID, canonical.EntityRule, res, rule.Provenance)
			continue
		}
		exportSteering(b, rule.ID, canonical.EntityRule, rule.Title, rule.Extensions, res, rule.Provenance)
	}

	for _, dec := range in.Project.Decisions {
		content := ""
		for _, c := range dec.AgentConstraints {
			content += "- " + strings.TrimSpace(c) + "\n"
		}
		res := b.Resolve(dec.ID, true, canonical.Always(), strings.TrimRight(content, "\n"))
		b.CountDocumentationOnly(dec.Context + dec.Decision + dec.Consequences)
		if !res.Included || len(dec.AgentConstraints) == 0 {
			if res.Included {
				res.SkippedReason = "the decision record declares no agent constraints, so it stays human documentation"
			}
			b.Skip(dec.ID, canonical.EntityDecision, res, dec.Provenance)
			continue
		}
		dest := b.Path(res, SteeringDir, adapters.FileSlug(dec.Title, dec.ID)+".md")
		entries := []adapters.KV{{Key: "inclusion", Value: InclusionAlways}}
		var md adapters.Markdown
		md.Heading(1, dec.Title)
		md.Paragraph(res.Content)
		b.Emit(dest, adapters.RenderFrontMatter(entries)+md.String(), []string{dec.ID})
		b.Adapted(dec.ID, canonical.EntityDecision, res, dec.Provenance, []string{dest},
			"Only the decision's agent constraints are projected, as an always-included steering document.")
		b.CountAlwaysOn(res.Content)
	}

	for _, proc := range in.Project.Procedures {
		res := b.Resolve(proc.ID, canonical.IsEnabled(proc.Enabled),
			canonical.OnDemand(proc.Trigger, proc.Name), proc.Content)
		if !res.Included {
			b.Skip(proc.ID, canonical.EntityProcedure, res, proc.Provenance)
			continue
		}
		desc := proc.Description
		if desc == "" {
			desc = proc.Trigger
		}
		dest := writeSkill(b, res, proc.Name, proc.ID, desc, nil, res.Content, proc.Extensions)
		b.RecordWithDiagnostics(proc.ID, canonical.EntityProcedure, adapters.OutcomeAdapted, res,
			proc.Provenance, []string{dest},
			"Kiro has no dedicated procedure format; the procedure is delivered as a skill.",
			[]string{b.Diag(diagnostics.New(diagnostics.OnDemandAdapted, diagnostics.SeverityInfo,
				"procedure delivered as a Kiro skill").WithEntity(proc.ID).WithPath(dest))})
		b.CountOnDemand(res.Content)
	}

	for _, skill := range in.Project.Skills {
		res := b.Resolve(skill.ID, canonical.IsEnabled(skill.Enabled), canonical.OnDemand("", skill.Name), skill.Content)
		if !res.Included {
			b.Skip(skill.ID, canonical.EntitySkill, res, skill.Provenance)
			continue
		}
		dest := writeSkill(b, res, skill.Name, skill.ID, skill.Description, skill.AllowedTools,
			res.Content, skill.Extensions)
		b.Exact(skill.ID, canonical.EntitySkill, res, skill.Provenance, []string{dest},
			"Kiro supports skills natively.")
		b.CountOnDemand(res.Content)
	}

	for _, agent := range in.Project.Agents {
		res := b.Resolve(agent.ID, canonical.IsEnabled(agent.Enabled), canonical.OnDemand("", agent.Name), agent.Instructions)
		if !res.Included {
			b.Skip(agent.ID, canonical.EntityAgent, res, agent.Provenance)
			continue
		}
		fileName := adapters.FileSlug(agent.Name, agent.ID) + ".json"
		if v, ok := agent.Extensions.GetString(string(canonical.TargetKiro), "stemma.sourceFile"); ok && safeName(v) {
			fileName = v
		}
		dest := b.Path(res, AgentsDir, fileName)
		content, err := renderAgent(agent, res.Content)
		if err != nil {
			b.Diag(diagnostics.New(diagnostics.InternalInvariant, diagnostics.SeverityError,
				"the agent definition could not be encoded as JSON").
				WithEntity(agent.ID).WithDetail("%v", err))
			b.Record(agent.ID, canonical.EntityAgent, adapters.OutcomeBlocked, res, agent.Provenance,
				nil, "The agent could not be encoded as a Kiro agent definition.")
			continue
		}
		b.Emit(dest, content, []string{agent.ID})
		outcome, explanation, diagIDs := adapters.AgentOutcome(agent, canonical.TargetKiro, b, res)
		b.RecordWithDiagnostics(agent.ID, canonical.EntityAgent, outcome, res, agent.Provenance,
			[]string{dest}, explanation, diagIDs)
		b.CountOnDemand(res.Content)
	}

	b.ReportUnemittedOpaque()
	return b.Result(), nil
}

// exportSteering writes an entity as a steering document.
func exportSteering(
	b *adapters.Builder,
	id string,
	kind canonical.EntityType,
	title string,
	ext canonical.Extensions,
	res adapters.Resolution,
	prov provenance.Provenance,
) {
	file := adapters.FileSlug(title, id) + ".md"
	if v, ok := ext.GetString(string(canonical.TargetKiro), "stemma.steeringFile"); ok && safeName(v) {
		file = v
	}
	dest := b.Path(res, SteeringDir, file)

	var entries []adapters.KV
	outcome := adapters.OutcomeExact
	explanation := ""
	var diagIDs []string

	switch res.Activation.Type {
	case canonical.ActivationAlways:
		entries = append(entries, adapters.KV{Key: "inclusion", Value: InclusionAlways})
		explanation = "Always-on context maps to a steering document with inclusion: always."
		b.CountAlwaysOn(res.Content)
	case canonical.ActivationPathScoped:
		entries = append(entries, adapters.KV{Key: "inclusion", Value: InclusionFileMatch})
		if len(res.Activation.Include) == 1 {
			entries = append(entries, adapters.KV{Key: "fileMatchPattern", Value: res.Activation.Include[0]})
		} else {
			entries = append(entries, adapters.KV{Key: "fileMatchPattern", Value: res.Activation.Include})
		}
		explanation = "Path-scoped context maps to inclusion: fileMatch with the canonical patterns."
		b.CountScoped(tokenestimate.ScopeName(res.Activation.Include), id, res.Content)
	case canonical.ActivationOnDemand:
		mode := InclusionManual
		if v, ok := ext.GetString(string(canonical.TargetKiro), "inclusion"); ok && v == InclusionAuto {
			mode = InclusionAuto
		}
		entries = append(entries, adapters.KV{Key: "inclusion", Value: mode})
		name := res.Activation.InvocationName
		if name == "" {
			name = title
		}
		entries = append(entries, adapters.KV{Key: "name", Value: name})
		if res.Activation.Trigger != "" {
			entries = append(entries, adapters.KV{Key: "description", Value: res.Activation.Trigger})
		}
		explanation = "On-demand context maps to a steering document with inclusion: " + mode + "."
		b.CountOnDemand(res.Content)
	default:
		b.Invariant(id, kind, res, prov)
		return
	}

	var md adapters.Markdown
	md.Heading(1, title)
	md.Paragraph(res.Content)

	if len(res.Activation.Exclude) > 0 {
		outcome = adapters.OutcomeLossy
		explanation = "fileMatchPattern has no negative pattern syntax, so the exclude patterns are " +
			"only reproduced as a note in the file body."
		md.BlankLine()
		md.Paragraph("> Scope note: this document does not apply to " +
			strings.Join(res.Activation.Exclude, ", ") + ".")
		diagIDs = append(diagIDs, b.Diag(diagnostics.New(diagnostics.ExcludeNotRepresent,
			diagnostics.SeverityWarning,
			"exclude patterns cannot be represented in a Kiro fileMatchPattern").
			WithEntity(id).WithPath(dest).
			WithDetail("The canonical entity excludes %s.", strings.Join(res.Activation.Exclude, ", ")).
			WithSuggestion("Narrow the include patterns, or accept this diagnostic in the profile.")))
	}
	if desc, ok := ext.GetString(string(canonical.TargetKiro), "description"); ok &&
		res.Activation.Type != canonical.ActivationOnDemand && desc != "" {
		entries = append(entries, adapters.KV{Key: "description", Value: desc})
	}
	entries = append(entries, adapters.ExtensionEntries(ext, string(canonical.TargetKiro),
		"inclusion", "fileMatchPattern", "description", "name")...)

	b.Emit(dest, adapters.RenderFrontMatter(entries)+md.String(), []string{id})
	b.RecordWithDiagnostics(id, kind, outcome, res, prov, []string{dest}, explanation, diagIDs)
}

func ruleBody(rule canonical.Rule, _ *adapters.Builder) string {
	line := adapters.RuleLine(rule)
	if rule.Priority == canonical.PriorityMust || rule.Priority == canonical.PriorityMay {
		return strings.ToUpper(string(rule.Priority)) + ": " + line
	}
	return line
}

func writeSkill(
	b *adapters.Builder,
	res adapters.Resolution,
	name, id, description string,
	tools []string,
	content string,
	ext canonical.Extensions,
) string {
	dirName := adapters.FileSlug(name, id)
	if v, ok := ext.GetString(string(canonical.TargetKiro), "stemma.sourceDir"); ok && safeName(v) {
		dirName = v
	}
	dest := b.Path(res, path.Join(SkillsDir, dirName), "SKILL.md")
	entries := []adapters.KV{{Key: "name", Value: name}}
	if description != "" {
		entries = append(entries, adapters.KV{Key: "description", Value: description})
	}
	if len(tools) > 0 {
		entries = append(entries, adapters.KV{Key: "allowed-tools", Value: tools})
	}
	entries = append(entries, adapters.ExtensionEntries(ext, string(canonical.TargetKiro),
		"name", "description", "allowed-tools")...)
	var md adapters.Markdown
	md.Heading(1, name)
	md.Paragraph(content)
	b.Emit(dest, adapters.RenderFrontMatter(entries)+md.String(), []string{id})
	return dest
}

// renderAgent encodes a canonical agent as a Kiro agent definition. Keys are
// sorted by encoding/json, so output is deterministic.
func renderAgent(agent canonical.Agent, instructions string) (string, error) {
	out := map[string]any{
		"name": agent.Name,
	}
	if agent.Description != "" {
		out["description"] = agent.Description
	}
	key := "instructions"
	if v, ok := agent.Extensions.GetString(string(canonical.TargetKiro), "stemma.instructionsKey"); ok && v == "prompt" {
		key = "prompt"
	}
	out[key] = instructions
	if len(agent.Tools) > 0 {
		out["tools"] = agent.Tools
	}
	if agent.ModelPreference != "" {
		out["model"] = agent.ModelPreference
	}
	if ext, ok := agent.Extensions[string(canonical.TargetKiro)]; ok {
		keys := make([]string, 0, len(ext))
		for k := range ext {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if strings.HasPrefix(k, adapters.InternalExtensionPrefix) {
				continue
			}
			if _, taken := out[k]; taken {
				continue
			}
			out[k] = ext[k]
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func safeName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, "/\\\x00")
}
