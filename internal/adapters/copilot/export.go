package copilot

import (
	"context"
	"path"
	"sort"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/provenance"
	"github.com/alexvinola/stemma-cli/internal/tokenestimate"
)

// Destination paths.
const (
	rootInstructionsPath = ".github/copilot-instructions.md"
	instructionsDir      = ".github/instructions"
	promptsDir           = ".github/prompts"
	skillsDir            = ".github/skills"
	agentsDir            = ".github/agents"
)

// Exporter renders canonical entities as Copilot configuration.
type Exporter struct{}

// Format implements adapters.Exporter.
func (Exporter) Format() canonical.TargetFormat { return canonical.TargetCopilot }

// Export implements adapters.Exporter.
func (Exporter) Export(ctx context.Context, in adapters.ExportInput) (adapters.ExportResult, error) {
	b := adapters.NewBuilder(canonical.TargetCopilot, in)

	always := adapters.NewAlwaysBucket()

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
		switch res.Activation.Type {
		case canonical.ActivationAlways:
			always.AddSection(doc.ID, doc.Title, res.Content)
			b.Exact(doc.ID, canonical.EntityContext, res, doc.Provenance, []string{rootInstructionsPath},
				"Always-on context is written into the repository-wide instructions file.")
			b.CountAlwaysOn(res.Content)
		case canonical.ActivationPathScoped:
			exportScoped(b, in, doc.ID, canonical.EntityContext, doc.Title, res, doc.Provenance,
				docDescription(doc), instructionsFileName(doc))
		case canonical.ActivationOnDemand:
			exportOnDemand(b, doc.ID, canonical.EntityContext, doc.Title, res, doc.Provenance)
		default:
			b.Invariant(doc.ID, canonical.EntityContext, res, doc.Provenance)
		}
	}

	for _, rule := range in.Project.Rules {
		if err := ctx.Err(); err != nil {
			return adapters.ExportResult{}, err
		}
		res := b.Resolve(rule.ID, rule.Enabled, rule.Activation, adapters.RuleLine(rule))
		b.CountDocumentationOnly(rule.Rationale + strings.Join(rule.GoodExamples, "\n") + strings.Join(rule.BadExamples, "\n"))
		if !res.Included {
			b.Skip(rule.ID, canonical.EntityRule, res, rule.Provenance)
			continue
		}
		switch res.Activation.Type {
		case canonical.ActivationAlways:
			always.AddRule(rule.ID, rule.Priority, res.Content)
			b.Exact(rule.ID, canonical.EntityRule, res, rule.Provenance, []string{rootInstructionsPath},
				"The rule is written as a bullet in the repository-wide instructions file.")
			b.CountAlwaysOn(res.Content)
		case canonical.ActivationPathScoped:
			exportScoped(b, in, rule.ID, canonical.EntityRule, rule.Title, res, rule.Provenance, "", "")
		case canonical.ActivationOnDemand:
			exportOnDemand(b, rule.ID, canonical.EntityRule, rule.Title, res, rule.Provenance)
		default:
			b.Invariant(rule.ID, canonical.EntityRule, res, rule.Provenance)
		}
	}

	for _, dec := range in.Project.Decisions {
		res := b.Resolve(dec.ID, canonical.IsEnabled(nil), canonical.Always(), strings.Join(dec.AgentConstraints, "\n"))
		b.CountDocumentationOnly(dec.Context + dec.Decision + dec.Consequences)
		if !res.Included || len(dec.AgentConstraints) == 0 {
			if res.Included {
				res.SkippedReason = "the decision record declares no agent constraints, so it stays human documentation"
			}
			b.Skip(dec.ID, canonical.EntityDecision, res, dec.Provenance)
			continue
		}
		always.AddDecision(dec.ID, dec.Title, dec.AgentConstraints)
		b.Adapted(dec.ID, canonical.EntityDecision, res, dec.Provenance, []string{rootInstructionsPath},
			"Only the decision's agent constraints are projected; context, decision and consequences stay human documentation.")
		b.CountAlwaysOn(strings.Join(dec.AgentConstraints, "\n"))
	}

	// Procedures become prompt files.
	for _, proc := range in.Project.Procedures {
		res := b.Resolve(proc.ID, canonical.IsEnabled(proc.Enabled), canonical.OnDemand(proc.Trigger, proc.Name), proc.Content)
		if !res.Included {
			b.Skip(proc.ID, canonical.EntityProcedure, res, proc.Provenance)
			continue
		}
		name := adapters.FileSlug(proc.Name, proc.ID)
		file := name + ".prompt.md"
		if v, ok := proc.Extensions.GetString(string(canonical.TargetCopilot), "stemma.promptFile"); ok && safeName(v) {
			file = v
		}
		dest := b.Path(res, promptsDir, file)
		entries := []adapters.KV{}
		if proc.Description != "" {
			entries = append(entries, adapters.KV{Key: "description", Value: proc.Description})
		}
		entries = append(entries, adapters.ExtensionEntries(proc.Extensions, string(canonical.TargetCopilot),
			"description")...)
		var md adapters.Markdown
		md.Heading(1, proc.Name)
		if proc.Trigger != "" {
			md.Paragraph("Use this prompt when: " + proc.Trigger)
		}
		md.Paragraph(res.Content)
		content := adapters.RenderFrontMatter(entries) + md.String()
		b.Emit(dest, content, []string{proc.ID})
		b.Exact(proc.ID, canonical.EntityProcedure, res, proc.Provenance, []string{dest},
			"Procedures map to Copilot prompt files, which are invoked explicitly.")
		b.CountOnDemand(res.Content)
	}

	for _, skill := range in.Project.Skills {
		res := b.Resolve(skill.ID, canonical.IsEnabled(skill.Enabled), canonical.OnDemand("", skill.Name), skill.Content)
		if !res.Included {
			b.Skip(skill.ID, canonical.EntitySkill, res, skill.Provenance)
			continue
		}
		name := adapters.FileSlug(skill.Name, skill.ID)
		if v, ok := skill.Extensions.GetString(string(canonical.TargetCopilot), "stemma.sourceDir"); ok && safeName(v) {
			name = v
		}
		dest := b.Path(res, path.Join(skillsDir, name), "SKILL.md")
		entries := []adapters.KV{{Key: "name", Value: skill.Name}}
		if skill.Description != "" {
			entries = append(entries, adapters.KV{Key: "description", Value: skill.Description})
		}
		if len(skill.AllowedTools) > 0 {
			entries = append(entries, adapters.KV{Key: "allowed-tools", Value: skill.AllowedTools})
		}
		entries = append(entries, adapters.ExtensionEntries(skill.Extensions, string(canonical.TargetCopilot),
			"name", "description", "allowed-tools")...)
		var md adapters.Markdown
		md.Heading(1, skill.Name)
		md.Paragraph(res.Content)
		b.Emit(dest, adapters.RenderFrontMatter(entries)+md.String(), []string{skill.ID})
		b.Exact(skill.ID, canonical.EntitySkill, res, skill.Provenance, []string{dest},
			"Copilot supports agent skills natively.")
		b.CountOnDemand(res.Content)
	}

	for _, agent := range in.Project.Agents {
		res := b.Resolve(agent.ID, canonical.IsEnabled(agent.Enabled), canonical.OnDemand("", agent.Name), agent.Instructions)
		if !res.Included {
			b.Skip(agent.ID, canonical.EntityAgent, res, agent.Provenance)
			continue
		}
		fileName := adapters.FileSlug(agent.Name, agent.ID) + ".md"
		if v, ok := agent.Extensions.GetString(string(canonical.TargetCopilot), "stemma.sourceFile"); ok && safeName(v) {
			fileName = v
		}
		dest := b.Path(res, agentsDir, fileName)
		entries := []adapters.KV{{Key: "name", Value: agent.Name}}
		if agent.Description != "" {
			entries = append(entries, adapters.KV{Key: "description", Value: agent.Description})
		}
		if len(agent.Tools) > 0 {
			entries = append(entries, adapters.KV{Key: "tools", Value: agent.Tools})
		}
		entries = append(entries, adapters.ExtensionEntries(agent.Extensions, string(canonical.TargetCopilot),
			"name", "description", "tools", "model")...)
		var md adapters.Markdown
		md.Heading(1, agent.Name)
		md.Paragraph(res.Content)
		b.Emit(dest, adapters.RenderFrontMatter(entries)+md.String(), []string{agent.ID})
		outcome, explanation, diagIDs := adapters.AgentOutcome(agent, canonical.TargetCopilot, b, res)
		b.RecordWithDiagnostics(agent.ID, canonical.EntityAgent, outcome, res, agent.Provenance,
			[]string{dest}, explanation, diagIDs)
		b.CountOnDemand(res.Content)
	}

	// Repository-wide instructions file.
	if !always.Empty() {
		content := always.Render(in.Project.Name, extraFrontMatter(in.Project))
		content += b.ReemitOpaque(rootInstructionsPath)
		if reused, ok := adapters.ReuseOriginal(in, rootInstructionsPath, always.EntityIDs()); ok {
			b.EmitReused(rootInstructionsPath, reused, always.EntityIDs())
		} else {
			b.Emit(rootInstructionsPath, content, always.EntityIDs())
		}
	}
	b.ReportUnemittedOpaque()

	return b.Result(), nil
}

// exportScoped writes a path-scoped entity as a .instructions.md file.
func exportScoped(
	b *adapters.Builder,
	in adapters.ExportInput,
	id string,
	kind canonical.EntityType,
	title string,
	res adapters.Resolution,
	prov provenance.Provenance,
	description string,
	pinnedFile string,
) {
	name := adapters.FileSlug(title, id)
	file := name + ".instructions.md"
	if pinnedFile != "" && safeName(pinnedFile) {
		file = pinnedFile
	}
	dest := b.Path(res, instructionsDir, file)

	entries := []adapters.KV{{Key: "applyTo", Value: strings.Join(res.Activation.Include, ",")}}
	if description != "" {
		entries = append(entries, adapters.KV{Key: "description", Value: description})
	}

	var md adapters.Markdown
	md.Heading(1, title)
	md.Paragraph(res.Content)

	outcome := adapters.OutcomeExact
	explanation := "Copilot applyTo represents the canonical include patterns directly."
	var diagIDs []string
	if len(res.Activation.Exclude) > 0 {
		outcome = adapters.OutcomeLossy
		explanation = "Copilot applyTo has no negative pattern syntax, so the exclude patterns " +
			"are only reproduced as a note in the file body."
		md.BlankLine()
		md.Paragraph("> Scope note: these instructions do not apply to " +
			strings.Join(res.Activation.Exclude, ", ") + ".")
		diagIDs = append(diagIDs, b.Diag(diagnostics.New(diagnostics.ExcludeNotRepresent,
			diagnostics.SeverityWarning,
			"exclude patterns cannot be represented in Copilot applyTo").
			WithEntity(id).WithTarget(string(canonical.TargetCopilot)).WithPath(dest).
			WithDetail("The canonical entity excludes %s. Copilot has no negative applyTo syntax, "+
				"so the exclusion is written as a note in the generated file and is advisory only.",
				strings.Join(res.Activation.Exclude, ", ")).
			WithSuggestion("Narrow the include patterns instead, or accept this diagnostic in the profile.")))
	}
	b.Emit(dest, adapters.RenderFrontMatter(entries)+md.String(), []string{id})
	b.RecordWithDiagnostics(id, kind, outcome, res, prov, []string{dest}, explanation, diagIDs)
	b.CountScoped(tokenestimate.ScopeName(res.Activation.Include), id, res.Content)
}

// exportOnDemand writes an on-demand entity as a prompt file.
func exportOnDemand(
	b *adapters.Builder,
	id string,
	kind canonical.EntityType,
	title string,
	res adapters.Resolution,
	prov provenance.Provenance,
) {
	name := adapters.FileSlug(title, id)
	dest := b.Path(res, promptsDir, name+".prompt.md")
	entries := []adapters.KV{}
	if res.Activation.Trigger != "" {
		entries = append(entries, adapters.KV{Key: "description", Value: res.Activation.Trigger})
	}
	var md adapters.Markdown
	md.Heading(1, title)
	md.Paragraph(res.Content)
	b.Emit(dest, adapters.RenderFrontMatter(entries)+md.String(), []string{id})
	b.RecordWithDiagnostics(id, kind, adapters.OutcomeAdapted, res, prov, []string{dest},
		"On-demand context has no dedicated Copilot format; it is delivered as a prompt file "+
			"that a developer invokes explicitly.",
		[]string{b.Diag(diagnostics.New(diagnostics.OnDemandAdapted, diagnostics.SeverityInfo,
			"on-demand content is delivered as a Copilot prompt file").
			WithEntity(id).WithTarget(string(canonical.TargetCopilot)).WithPath(dest))})
	b.CountOnDemand(res.Content)
}

func instructionsFileName(doc canonical.ContextDocument) string {
	if v, ok := doc.Extensions.GetString(string(canonical.TargetCopilot), "stemma.instructionsFile"); ok {
		return v
	}
	return ""
}

func docDescription(doc canonical.ContextDocument) string {
	if v, ok := doc.Extensions.GetString(string(canonical.TargetCopilot), "description"); ok {
		return v
	}
	return ""
}

func extraFrontMatter(p canonical.Project) []adapters.KV {
	ext, ok := p.Extensions[string(canonical.TargetCopilot)]
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(ext))
	for k := range ext {
		if strings.HasPrefix(k, "rootInstructions.") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]adapters.KV, 0, len(keys))
	for _, k := range keys {
		out = append(out, adapters.KV{Key: strings.TrimPrefix(k, "rootInstructions."), Value: ext[k]})
	}
	return out
}

// safeName rejects file names that could escape a directory.
func safeName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	return true
}
