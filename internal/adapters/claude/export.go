package claude

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
	defaultMemoryPath = "CLAUDE.md"
	skillsDir         = ".claude/skills"
	agentsDir         = ".claude/agents"
)

// Exporter renders canonical entities as Claude Code configuration.
type Exporter struct{}

// Format implements adapters.Exporter.
func (Exporter) Format() canonical.TargetFormat { return canonical.TargetClaude }

// Export implements adapters.Exporter.
func (Exporter) Export(ctx context.Context, in adapters.ExportInput) (adapters.ExportResult, error) {
	b := adapters.NewBuilder(canonical.TargetClaude, in)
	always := adapters.NewAlwaysBucket()
	memoryPath := memoryFilePath(in.Project)

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
			b.Exact(doc.ID, canonical.EntityContext, res, doc.Provenance, []string{memoryPath},
				"Always-on context is written into the project memory file, which Claude Code loads every session.")
			b.CountAlwaysOn(res.Content)
		case canonical.ActivationPathScoped:
			exportScopedRule(b, doc.ID, canonical.EntityContext, doc.Title, ruleFileName(doc.Extensions, doc.Title, doc.ID),
				descriptionOf(doc.Extensions), res, doc.Provenance, nil)
		case canonical.ActivationOnDemand:
			exportAsSkill(b, doc.ID, canonical.EntityContext, doc.Title, descriptionOf(doc.Extensions), res, doc.Provenance,
				"Claude Code has no on-demand context format other than skills, so the document is "+
					"delivered as a skill that loads when invoked.")
		default:
			b.Invariant(doc.ID, canonical.EntityContext, res, doc.Provenance)
		}
	}

	for _, rule := range in.Project.Rules {
		res := b.Resolve(rule.ID, rule.Enabled, rule.Activation, adapters.RuleLine(rule))
		b.CountDocumentationOnly(rule.Rationale + strings.Join(rule.GoodExamples, "\n") + strings.Join(rule.BadExamples, "\n"))
		if !res.Included {
			b.Skip(rule.ID, canonical.EntityRule, res, rule.Provenance)
			continue
		}
		switch res.Activation.Type {
		case canonical.ActivationAlways:
			// An always-on rule keeps its own file under .claude/rules when it
			// came from there, so file identity survives a round trip.
			if file := ruleFileHint(rule.Extensions); file != "" {
				exportRuleFile(b, rule, res, file, nil)
				continue
			}
			always.AddRule(rule.ID, rule.Priority, res.Content)
			b.Exact(rule.ID, canonical.EntityRule, res, rule.Provenance, []string{memoryPath},
				"The rule is written as a bullet in the project memory file.")
			b.CountAlwaysOn(res.Content)
		case canonical.ActivationPathScoped:
			exportRuleFile(b, rule, res, ruleFileName(rule.Extensions, rule.Title, rule.ID), res.Activation.Include)
		case canonical.ActivationOnDemand:
			exportAsSkill(b, rule.ID, canonical.EntityRule, rule.Title, descriptionOf(rule.Extensions), res, rule.Provenance,
				"On-demand rules are delivered as skills, which Claude Code loads when invoked.")
		default:
			b.Invariant(rule.ID, canonical.EntityRule, res, rule.Provenance)
		}
	}

	for _, dec := range in.Project.Decisions {
		res := b.Resolve(dec.ID, true, canonical.Always(), strings.Join(dec.AgentConstraints, "\n"))
		b.CountDocumentationOnly(dec.Context + dec.Decision + dec.Consequences)
		if !res.Included || len(dec.AgentConstraints) == 0 {
			if res.Included {
				res.SkippedReason = "the decision record declares no agent constraints, so it stays human documentation"
			}
			b.Skip(dec.ID, canonical.EntityDecision, res, dec.Provenance)
			continue
		}
		always.AddDecision(dec.ID, dec.Title, dec.AgentConstraints)
		b.Adapted(dec.ID, canonical.EntityDecision, res, dec.Provenance, []string{memoryPath},
			"Only the decision's agent constraints are projected; the rest stays human documentation.")
		b.CountAlwaysOn(strings.Join(dec.AgentConstraints, "\n"))
	}

	// Procedures have no native Claude format; skills are the closest fit.
	for _, proc := range in.Project.Procedures {
		res := b.Resolve(proc.ID, canonical.IsEnabled(proc.Enabled),
			canonical.OnDemand(proc.Trigger, proc.Name), proc.Content)
		if !res.Included {
			b.Skip(proc.ID, canonical.EntityProcedure, res, proc.Provenance)
			continue
		}
		name := skillDirName(proc.Extensions, proc.Name, proc.ID)
		dest := b.Path(res, path.Join(skillsDir, name), "SKILL.md")
		entries := []adapters.KV{{Key: "name", Value: proc.Name}}
		desc := proc.Description
		if desc == "" && proc.Trigger != "" {
			desc = proc.Trigger
		}
		if desc != "" {
			entries = append(entries, adapters.KV{Key: "description", Value: desc})
		}
		var md adapters.Markdown
		md.Heading(1, proc.Name)
		md.Paragraph(res.Content)
		b.Emit(dest, adapters.RenderFrontMatter(entries)+md.String(), []string{proc.ID})
		b.RecordWithDiagnostics(proc.ID, canonical.EntityProcedure, adapters.OutcomeAdapted, res,
			proc.Provenance, []string{dest},
			"Claude Code has no dedicated procedure format; the procedure is delivered as a skill.",
			[]string{b.Diag(diagnostics.New(diagnostics.OnDemandAdapted, diagnostics.SeverityInfo,
				"procedure delivered as a Claude skill").WithEntity(proc.ID))})
		b.CountOnDemand(res.Content)
	}

	for _, skill := range in.Project.Skills {
		res := b.Resolve(skill.ID, canonical.IsEnabled(skill.Enabled), canonical.OnDemand("", skill.Name), skill.Content)
		if !res.Included {
			b.Skip(skill.ID, canonical.EntitySkill, res, skill.Provenance)
			continue
		}
		name := skillDirName(skill.Extensions, skill.Name, skill.ID)
		dest := b.Path(res, path.Join(skillsDir, name), "SKILL.md")
		entries := []adapters.KV{{Key: "name", Value: skill.Name}}
		if skill.Description != "" {
			entries = append(entries, adapters.KV{Key: "description", Value: skill.Description})
		}
		if len(skill.AllowedTools) > 0 {
			entries = append(entries, adapters.KV{Key: "allowed-tools", Value: skill.AllowedTools})
		}
		entries = append(entries, adapters.ExtensionEntries(skill.Extensions, string(canonical.TargetClaude),
			"name", "description", "allowed-tools")...)
		var md adapters.Markdown
		md.Heading(1, skill.Name)
		md.Paragraph(res.Content)
		b.Emit(dest, adapters.RenderFrontMatter(entries)+md.String(), []string{skill.ID})
		b.Exact(skill.ID, canonical.EntitySkill, res, skill.Provenance, []string{dest},
			"Claude Code supports skills natively.")
		b.CountOnDemand(res.Content)
	}

	for _, agent := range in.Project.Agents {
		res := b.Resolve(agent.ID, canonical.IsEnabled(agent.Enabled), canonical.OnDemand("", agent.Name), agent.Instructions)
		if !res.Included {
			b.Skip(agent.ID, canonical.EntityAgent, res, agent.Provenance)
			continue
		}
		fileName := adapters.FileSlug(agent.Name, agent.ID) + ".md"
		if v, ok := agent.Extensions.GetString(string(canonical.TargetClaude), "stemma.sourceFile"); ok && safeName(v) {
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
		if agent.ModelPreference != "" {
			entries = append(entries, adapters.KV{Key: "model", Value: agent.ModelPreference})
		}
		entries = append(entries, adapters.ExtensionEntries(agent.Extensions, string(canonical.TargetClaude),
			"name", "description", "tools", "model")...)
		var md adapters.Markdown
		md.Paragraph(res.Content)
		b.Emit(dest, adapters.RenderFrontMatter(entries)+md.String(), []string{agent.ID})
		outcome, explanation, diagIDs := adapters.AgentOutcome(agent, canonical.TargetClaude, b, res)
		b.RecordWithDiagnostics(agent.ID, canonical.EntityAgent, outcome, res, agent.Provenance,
			[]string{dest}, explanation, diagIDs)
		b.CountOnDemand(res.Content)
	}

	if !always.Empty() {
		content := always.Render(in.Project.Name, memoryFrontMatter(in.Project))
		content += b.ReemitOpaque(memoryPath)
		b.Emit(memoryPath, content, always.EntityIDs())
	}
	b.ReportUnemittedOpaque()
	return b.Result(), nil
}

// exportRuleFile writes a rule as a .claude/rules file.
func exportRuleFile(b *adapters.Builder, rule canonical.Rule, res adapters.Resolution, file string, include []string) {
	exportScopedRule(b, rule.ID, canonical.EntityRule, rule.Title, file, descriptionOf(rule.Extensions),
		res, rule.Provenance, include)
}

// exportScopedRule writes a path-scoped entity as a Claude rule file.
func exportScopedRule(
	b *adapters.Builder,
	id string,
	kind canonical.EntityType,
	title, file, description string,
	res adapters.Resolution,
	prov provenance.Provenance,
	_ []string,
) {
	dest := b.Path(res, RulesDir, file)
	var entries []adapters.KV
	if len(res.Activation.Include) > 0 {
		entries = append(entries, adapters.KV{Key: "paths", Value: res.Activation.Include})
	}
	if description != "" {
		entries = append(entries, adapters.KV{Key: "description", Value: description})
	}

	var md adapters.Markdown
	md.Heading(1, title)
	md.Paragraph(res.Content)

	outcome := adapters.OutcomeExact
	explanation := "Claude rules accept the canonical include patterns in their paths front matter."
	var diagIDs []string
	if res.Activation.Type != canonical.ActivationPathScoped {
		explanation = "The rule keeps its own file under .claude/rules and loads unconditionally, " +
			"because it has no paths front matter."
	}
	if len(res.Activation.Exclude) > 0 {
		outcome = adapters.OutcomeLossy
		explanation = "The documented paths front matter has no negative pattern syntax, so the " +
			"exclude patterns are only reproduced as a note in the file body."
		md.BlankLine()
		md.Paragraph("> Scope note: this rule does not apply to " +
			strings.Join(res.Activation.Exclude, ", ") + ".")
		diagIDs = append(diagIDs, b.Diag(diagnostics.New(diagnostics.ExcludeNotRepresent,
			diagnostics.SeverityWarning,
			"exclude patterns cannot be represented in Claude paths front matter").
			WithEntity(id).WithPath(dest).
			WithDetail("The canonical entity excludes %s.", strings.Join(res.Activation.Exclude, ", ")).
			WithSuggestion("Narrow the include patterns, or accept this diagnostic in the profile.")))
	}
	b.Emit(dest, adapters.RenderFrontMatter(entries)+md.String(), []string{id})
	b.RecordWithDiagnostics(id, kind, outcome, res, prov, []string{dest}, explanation, diagIDs)
	if res.Activation.Type == canonical.ActivationPathScoped {
		b.CountScoped(tokenestimate.ScopeName(res.Activation.Include), id, res.Content)
	} else {
		b.CountAlwaysOn(res.Content)
	}
}

// exportAsSkill delivers on-demand content through the skills mechanism.
func exportAsSkill(
	b *adapters.Builder,
	id string,
	kind canonical.EntityType,
	title, description string,
	res adapters.Resolution,
	prov provenance.Provenance,
	explanation string,
) {
	skillName := title
	if res.Activation.InvocationName != "" {
		skillName = res.Activation.InvocationName
	}
	name := adapters.FileSlug(skillName, id)
	dest := b.Path(res, path.Join(skillsDir, name), "SKILL.md")
	entries := []adapters.KV{{Key: "name", Value: skillName}}
	desc := description
	if desc == "" {
		desc = res.Activation.Trigger
	}
	if desc != "" {
		entries = append(entries, adapters.KV{Key: "description", Value: desc})
	}
	var md adapters.Markdown
	md.Heading(1, title)
	md.Paragraph(res.Content)
	b.Emit(dest, adapters.RenderFrontMatter(entries)+md.String(), []string{id})
	b.RecordWithDiagnostics(id, kind, adapters.OutcomeAdapted, res, prov, []string{dest}, explanation,
		[]string{b.Diag(diagnostics.New(diagnostics.OnDemandAdapted, diagnostics.SeverityInfo,
			"on-demand content is delivered as a Claude skill").WithEntity(id).WithPath(dest))})
	b.CountOnDemand(res.Content)
}

func memoryFilePath(p canonical.Project) string {
	if v, ok := p.Extensions.GetString(string(canonical.TargetClaude), "stemma.rootFile"); ok {
		if v == "CLAUDE.md" || v == ".claude/CLAUDE.md" {
			return v
		}
	}
	return defaultMemoryPath
}

func memoryFrontMatter(p canonical.Project) []adapters.KV {
	ext, ok := p.Extensions[string(canonical.TargetClaude)]
	if !ok {
		return nil
	}
	var keys []string
	for k := range ext {
		if strings.HasPrefix(k, "memory.") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]adapters.KV, 0, len(keys))
	for _, k := range keys {
		out = append(out, adapters.KV{Key: strings.TrimPrefix(k, "memory."), Value: ext[k]})
	}
	return out
}

// ruleFileName picks the destination file name for a rule, preferring the
// original name so that a round trip does not move files.
func ruleFileName(ext canonical.Extensions, title, id string) string {
	if v := ruleFileHint(ext); v != "" {
		return v
	}
	return adapters.FileSlug(title, id) + ".md"
}

func ruleFileHint(ext canonical.Extensions) string {
	v, ok := ext.GetString(string(canonical.TargetClaude), "stemma.ruleFile")
	if !ok || v == "" {
		return ""
	}
	for _, seg := range strings.Split(v, "/") {
		if !safeName(seg) {
			return ""
		}
	}
	return v
}

func skillDirName(ext canonical.Extensions, name, id string) string {
	if v, ok := ext.GetString(string(canonical.TargetClaude), "stemma.sourceDir"); ok && safeName(v) {
		return v
	}
	return adapters.FileSlug(name, id)
}

func descriptionOf(ext canonical.Extensions) string {
	for _, provider := range []string{
		string(canonical.TargetClaude), string(canonical.TargetCopilot),
		string(canonical.TargetKiro), string(canonical.TargetCodex),
	} {
		if v, ok := ext.GetString(provider, "description"); ok && v != "" {
			return v
		}
	}
	return ""
}

func safeName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, "/\\\x00")
}
