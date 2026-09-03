package codex

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/globs"
	"github.com/alexvinola/stemma-cli/internal/provenance"
	"github.com/alexvinola/stemma-cli/internal/tokenestimate"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// Exporter renders canonical entities as AGENTS.md files and skills.
type Exporter struct{}

// Format implements adapters.Exporter.
func (Exporter) Format() canonical.TargetFormat { return canonical.TargetCodex }

// Export implements adapters.Exporter.
func (Exporter) Export(ctx context.Context, in adapters.ExportInput) (adapters.ExportResult, error) {
	b := adapters.NewBuilder(canonical.TargetCodex, in)
	root := adapters.NewAlwaysBucket()
	nested := map[string]*adapters.AlwaysBucket{}

	bucketFor := func(dir string) *adapters.AlwaysBucket {
		if bk, ok := nested[dir]; ok {
			return bk
		}
		bk := adapters.NewAlwaysBucket()
		nested[dir] = bk
		return bk
	}

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
			root.AddSection(doc.ID, doc.Title, res.Content)
			b.Exact(doc.ID, canonical.EntityContext, res, doc.Provenance, []string{RootFile},
				"Always-on context is written into the root AGENTS.md.")
			b.CountAlwaysOn(res.Content)
		case canonical.ActivationPathScoped:
			placeScoped(b, root, bucketFor, doc.ID, canonical.EntityContext, doc.Title, res, doc.Provenance)
		case canonical.ActivationOnDemand:
			exportSkillLike(b, doc.ID, canonical.EntityContext, doc.Title, descriptionOf(doc.Extensions),
				res, doc.Provenance,
				"This ecosystem has no on-demand instructions format other than skills, so the "+
					"document is delivered as a skill.")
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
			root.AddRule(rule.ID, rule.Priority, res.Content)
			b.Exact(rule.ID, canonical.EntityRule, res, rule.Provenance, []string{RootFile},
				"The rule is written as a bullet in the root AGENTS.md.")
			b.CountAlwaysOn(res.Content)
		case canonical.ActivationPathScoped:
			placeScopedRule(b, root, bucketFor, rule, res)
		case canonical.ActivationOnDemand:
			exportSkillLike(b, rule.ID, canonical.EntityRule, rule.Title, "", res, rule.Provenance,
				"On-demand rules are delivered as skills.")
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
		root.AddDecision(dec.ID, dec.Title, dec.AgentConstraints)
		b.Adapted(dec.ID, canonical.EntityDecision, res, dec.Provenance, []string{RootFile},
			"Only the decision's agent constraints are projected; the rest stays human documentation.")
		b.CountAlwaysOn(strings.Join(dec.AgentConstraints, "\n"))
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
			"There is no dedicated procedure format; the procedure is delivered as a skill.",
			[]string{b.Diag(diagnostics.New(diagnostics.OnDemandAdapted, diagnostics.SeverityInfo,
				"procedure delivered as a skill").WithEntity(proc.ID).WithPath(dest))})
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
			"Skills are supported natively.")
		b.CountOnDemand(res.Content)
	}

	// There is no native specialist-agent format, so agent definitions are
	// flattened into the root instructions. This is never called exact.
	for _, agent := range in.Project.Agents {
		res := b.Resolve(agent.ID, canonical.IsEnabled(agent.Enabled), canonical.Always(), agent.Instructions)
		if !res.Included {
			b.Skip(agent.ID, canonical.EntityAgent, res, agent.Provenance)
			continue
		}
		var body strings.Builder
		if agent.Description != "" {
			body.WriteString(agent.Description + "\n\n")
		}
		body.WriteString(res.Content)
		if len(agent.Tools) > 0 {
			body.WriteString("\n\nDeclared tools in the source definition: " + strings.Join(agent.Tools, ", ") + ".")
		}
		root.AddSection(agent.ID, "Specialist guidance: "+agent.Name, strings.TrimSpace(body.String()))
		fp := b.Diag(diagnostics.New(diagnostics.AgentNotNative, diagnostics.SeverityWarning,
			"there is no native specialist-agent format for this target").
			WithEntity(agent.ID).WithPath(RootFile).
			WithDetail("The definition of %q was flattened into ordinary always-on guidance in %s. "+
				"It is no longer a separate agent, and any declared tools are only mentioned as text.",
				agent.Name, RootFile).
			WithSuggestion("Set include:false for this entity in .stemma/profiles/codex.json if the " +
				"flattened guidance is not wanted."))
		b.RecordWithDiagnostics(agent.ID, canonical.EntityAgent, adapters.OutcomeLossy, res,
			agent.Provenance, []string{RootFile},
			"Flattened into always-on guidance because this target has no specialist-agent format.",
			[]string{fp})
		b.CountAlwaysOn(body.String())
	}

	// Preserved override files are written back verbatim.
	for _, blk := range b.OpaqueBlocksFor() {
		if strings.HasSuffix(blk.SourcePath, "AGENTS.override.md") {
			b.EmitOpaqueFile(blk)
		}
	}

	if !root.Empty() {
		content := root.Render(in.Project.Name, nil)
		content += b.ReemitOpaque(RootFile)
		b.Emit(RootFile, content, root.EntityIDs())
	}
	dirs := make([]string, 0, len(nested))
	for d := range nested {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		bk := nested[d]
		if bk.Empty() {
			continue
		}
		dest, err := workspace.JoinRel(d, RootFile)
		if err != nil {
			continue
		}
		content := bk.Render("Instructions for "+d, nil)
		content += b.ReemitOpaque(dest)
		b.Emit(dest, content, bk.EntityIDs())
	}
	b.ReportUnemittedOpaque()
	return b.Result(), nil
}

// placeScoped decides where a path-scoped context document goes.
func placeScoped(
	b *adapters.Builder,
	root *adapters.AlwaysBucket,
	bucketFor func(string) *adapters.AlwaysBucket,
	id string,
	kind canonical.EntityType,
	title string,
	res adapters.Resolution,
	prov provenance.Provenance,
) {
	dir, outcome, explanation, diagIDs := resolveDirectory(b, id, res)
	scope := tokenestimate.ScopeName(res.Activation.Include)
	if dir == "" {
		root.AddSection(id, title, scopeNote(res)+res.Content)
		b.RecordWithDiagnostics(id, kind, outcome, res, prov, []string{RootFile}, explanation, diagIDs)
		b.CountAlwaysOn(res.Content)
		return
	}
	dest, err := workspace.JoinRel(dir, RootFile)
	if err != nil {
		b.Diag(diagnostics.New(diagnostics.PathEscape, diagnostics.SeverityError,
			fmt.Sprintf("unsafe directory %q for entity %s", dir, id)).WithEntity(id))
		return
	}
	bucketFor(dir).AddSection(id, title, res.Content)
	b.RecordWithDiagnostics(id, kind, outcome, res, prov, []string{dest}, explanation, diagIDs)
	b.CountScoped(scope, id, res.Content)
}

func placeScopedRule(
	b *adapters.Builder,
	root *adapters.AlwaysBucket,
	bucketFor func(string) *adapters.AlwaysBucket,
	rule canonical.Rule,
	res adapters.Resolution,
) {
	dir, outcome, explanation, diagIDs := resolveDirectory(b, rule.ID, res)
	scope := tokenestimate.ScopeName(res.Activation.Include)
	if dir == "" {
		root.AddSection(rule.ID, rule.Title, scopeNote(res)+res.Content)
		b.RecordWithDiagnostics(rule.ID, canonical.EntityRule, outcome, res, rule.Provenance,
			[]string{RootFile}, explanation, diagIDs)
		b.CountAlwaysOn(res.Content)
		return
	}
	dest, err := workspace.JoinRel(dir, RootFile)
	if err != nil {
		return
	}
	bucketFor(dir).AddRule(rule.ID, rule.Priority, res.Content)
	b.RecordWithDiagnostics(rule.ID, canonical.EntityRule, outcome, res, rule.Provenance,
		[]string{dest}, explanation, diagIDs)
	b.CountScoped(scope, rule.ID, res.Content)
}

// resolveDirectory maps canonical include patterns onto a concrete directory,
// or returns "" when no directory can be derived without inventing one.
//
// Directory proximity is the only scoping mechanism here, so the mapping is
// exact only when the canonical pattern is precisely a directory subtree and
// nothing is excluded.
func resolveDirectory(
	b *adapters.Builder, id string, res adapters.Resolution,
) (dir string, outcome adapters.Outcome, explanation string, diagIDs []string) {
	pinned := res.Directory != ""
	if pinned {
		dir = res.Directory
	} else {
		derived, ok := globs.DirectoryScope(res.Activation.Include)
		if !ok {
			fp := b.Diag(diagnostics.New(diagnostics.DirectoryScopeAmbig, diagnostics.SeverityWarning,
				"the include patterns do not resolve to a single directory").
				WithEntity(id).WithPath(RootFile).
				WithDetail("Scoping here is expressed only by file location. The patterns %s do not "+
					"share one concrete directory, and Stemma will not invent one, so the content "+
					"stays in the root %s and applies everywhere.",
					strings.Join(res.Activation.Include, ", "), RootFile).
				WithSuggestion("Set a directory for this entity in .stemma/profiles/codex.json, or " +
					"set include:false to skip it for this target."))
			return "", adapters.OutcomeLossy,
				fmt.Sprintf("No single directory could be derived from %s, so the content stays in "+
					"the root %s and is no longer scoped.",
					strings.Join(res.Activation.Include, ", "), RootFile),
				[]string{fp}
		}
		dir = derived
	}

	origin := fmt.Sprintf("The patterns %s were mapped to the directory %s",
		strings.Join(res.Activation.Include, ", "), dir)
	if pinned {
		origin = fmt.Sprintf("The target profile pins this entity to %s/%s", dir, RootFile)
	}

	if len(res.Activation.Exclude) > 0 {
		fp := b.Diag(diagnostics.New(diagnostics.ExcludeNotRepresent, diagnostics.SeverityWarning,
			"exclude patterns cannot be represented by directory scoping").
			WithEntity(id).WithPath(path.Join(dir, RootFile)).
			WithDetail("The canonical entity excludes %s, but a nested %s applies to everything "+
				"under %s.", strings.Join(res.Activation.Exclude, ", "), RootFile, dir).
			WithSuggestion("Narrow the include patterns, move the excluded files elsewhere, or " +
				"accept this diagnostic in the profile."))
		return dir, adapters.OutcomeLossy,
			origin + ", which cannot express the exclude patterns.", []string{fp}
	}

	if len(res.Activation.Include) == 1 && res.Activation.Include[0] == dir+"/**" {
		return dir, adapters.OutcomeExact,
			fmt.Sprintf("The pattern %s is exactly the subtree of %s, which a nested %s expresses "+
				"directly.", res.Activation.Include[0], dir, RootFile), nil
	}

	fp := b.Diag(diagnostics.New(diagnostics.DirectoryScopeBroader, diagnostics.SeverityWarning,
		"directory scoping is broader than the canonical patterns").
		WithEntity(id).WithPath(path.Join(dir, RootFile)).
		WithDetail("%s. Everything under that directory now matches, including files the canonical "+
			"patterns did not select.", origin).
		WithSuggestion("Accept this diagnostic in the profile if the wider scope is acceptable."))
	return dir, adapters.OutcomeLossy,
		origin + ", which matches more files than the canonical patterns do.", []string{fp}
}

func scopeNote(res adapters.Resolution) string {
	if len(res.Activation.Include) == 0 {
		return ""
	}
	note := "> Scope note: this guidance is meant for " + strings.Join(res.Activation.Include, ", ")
	if len(res.Activation.Exclude) > 0 {
		note += ", excluding " + strings.Join(res.Activation.Exclude, ", ")
	}
	return note + ".\n\n"
}

// exportSkillLike writes on-demand content as a skill.
func exportSkillLike(
	b *adapters.Builder,
	id string,
	kind canonical.EntityType,
	title, description string,
	res adapters.Resolution,
	prov provenance.Provenance,
	explanation string,
) {
	desc := description
	if desc == "" {
		desc = res.Activation.Trigger
	}
	name := title
	if res.Activation.InvocationName != "" {
		name = res.Activation.InvocationName
	}
	dest := writeSkill(b, res, name, id, desc, nil, res.Content, nil)
	b.RecordWithDiagnostics(id, kind, adapters.OutcomeAdapted, res, prov, []string{dest}, explanation,
		[]string{b.Diag(diagnostics.New(diagnostics.OnDemandAdapted, diagnostics.SeverityInfo,
			"on-demand content is delivered as a skill").WithEntity(id).WithPath(dest))})
	b.CountOnDemand(res.Content)
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
	if v, ok := ext.GetString(string(canonical.TargetCodex), "stemma.sourceDir"); ok && safeName(v) {
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
	entries = append(entries, adapters.ExtensionEntries(ext, string(canonical.TargetCodex),
		"name", "description", "allowed-tools")...)
	var md adapters.Markdown
	md.Heading(1, name)
	md.Paragraph(content)
	b.Emit(dest, adapters.RenderFrontMatter(entries)+md.String(), []string{id})
	return dest
}

func descriptionOf(ext canonical.Extensions) string {
	for _, provider := range []string{
		string(canonical.TargetCodex), string(canonical.TargetCopilot),
		string(canonical.TargetClaude), string(canonical.TargetKiro),
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
