// Package copilot implements the GitHub Copilot adapter.
//
// Recognised paths:
//
//	.github/copilot-instructions.md            repository-wide instructions
//	.github/instructions/**/*.instructions.md  path-scoped instructions
//	.github/prompts/**/*.prompt.md             prompt files
//	.github/skills/*/SKILL.md                  agent skills
//	.github/agents/*.md                        custom agents
package copilot

import (
	"context"
	"path"
	"strings"

	"github.com/alexvinola/stemma/internal/adapters"
	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/diagnostics"
	"github.com/alexvinola/stemma/internal/discovery"
	"github.com/alexvinola/stemma/internal/provenance"
)

// Importer converts Copilot configuration into canonical entities.
type Importer struct{}

// Format implements adapters.Importer.
func (Importer) Format() canonical.TargetFormat { return canonical.TargetCopilot }

// Import implements adapters.Importer.
func (Importer) Import(ctx context.Context, in adapters.ImportInput) (adapters.ImportResult, error) {
	var bag diagnostics.Bag
	c := &adapters.ImportCtx{Provider: canonical.TargetCopilot, IDs: in.IDs, Bag: &bag}
	project := canonical.NewProject("", "")

	for _, file := range in.Files {
		if err := ctx.Err(); err != nil {
			return adapters.ImportResult{}, err
		}
		switch file.Role {
		case discovery.RoleRootInstructions:
			importRootInstructions(c, &project, file)
		case discovery.RoleScopedInstructions:
			importScopedInstructions(c, &project, file)
		case discovery.RolePrompt:
			importPrompt(c, &project, file)
		case discovery.RoleSkill:
			doc, ok := c.ParseDocument(file)
			if !ok {
				continue
			}
			project.Skills = append(project.Skills, c.SkillFromDocument(file, doc, discovery.SkillName(file.Path)))
		case discovery.RoleAgent:
			doc, ok := c.ParseDocument(file)
			if !ok {
				continue
			}
			project.Agents = append(project.Agents, c.AgentFromDocument(file, doc))
		default:
			bag.Add(diagnostics.New(diagnostics.UnrecognizedFormat, diagnostics.SeverityWarning,
				"file matched the Copilot registry but has no importer for its role").
				WithPath(file.Path))
		}
	}

	project.OpaqueBlocks = append(project.OpaqueBlocks, c.Opaque...)
	return adapters.ImportResult{Project: project, Diagnostics: bag.Items()}, nil
}

// importRootInstructions maps .github/copilot-instructions.md to always-on
// context documents, one per top-level section.
func importRootInstructions(c *adapters.ImportCtx, project *canonical.Project, file adapters.SourceFile) {
	doc, ok := c.ParseDocument(file)
	if !ok {
		return
	}
	if doc.FrontMatter != nil && len(doc.FrontMatter.Keys) > 0 {
		for _, k := range doc.FrontMatter.Keys {
			project.Extensions.Set(string(canonical.TargetCopilot), "rootInstructions."+k, doc.FrontMatter.Fields[k])
		}
		c.Bag.Add(diagnostics.New(diagnostics.UnknownKeysKept, diagnostics.SeverityInfo,
			"front matter on the repository-wide instructions file was preserved as a project extension").
			WithPath(file.Path))
	}
	units := adapters.SplitDocument(doc)
	if len(units) == 0 {
		if strings.TrimSpace(string(file.Data)) != "" {
			c.AddOpaque(file, string(file.Data),
				"the file contains no headings or body text that could be modelled",
				adapters.FullSpan(file, doc), true)
		}
		return
	}
	for _, u := range units {
		title := u.Title
		if title == "" {
			title = firstNonEmpty(doc.Title, "Repository overview")
		}
		if strings.TrimSpace(u.Content) == "" {
			c.AddOpaque(file, strings.Repeat("#", maxInt(u.Level, 2))+" "+u.Title,
				"heading with no content", u.Span, true)
			continue
		}
		id := c.IDs.Allocate(canonical.EntityContext, canonical.Slug(title), file.Path+"#"+title)
		project.ContextDocuments = append(project.ContextDocuments, canonical.ContextDocument{
			ID:         id,
			Title:      title,
			Kind:       adapters.KindFromHeading(title),
			Content:    u.Content,
			Audience:   canonical.AudienceAgent,
			Activation: canonical.Always(),
			Provenance: c.Provenance(file, u.Span, provenance.DispositionParsed),
		})
	}
}

// importScopedInstructions maps a .instructions.md file to a path-scoped
// context document. applyTo is a comma-separated glob list.
func importScopedInstructions(c *adapters.ImportCtx, project *canonical.Project, file adapters.SourceFile) {
	doc, ok := c.ParseDocument(file)
	if !ok {
		return
	}
	title := adapters.TitleFor(doc, file, "description")
	id := c.IDs.Allocate(canonical.EntityContext, canonical.Slug(title), file.Path)

	activation := canonical.Always()
	if applyTo, has := doc.FrontMatter.String("applyTo"); has {
		patterns := splitApplyTo(applyTo)
		if len(patterns) == 0 {
			c.Bag.Add(diagnostics.New(diagnostics.InvalidGlob, diagnostics.SeverityWarning,
				"applyTo is present but empty; the instructions are treated as always-on").
				WithPath(file.Path).WithEntity(id).
				WithPosition(doc.FrontMatter.StartLine, 1))
		} else {
			activation = canonical.PathScoped(patterns, nil)
		}
	} else {
		c.Bag.Add(diagnostics.New(diagnostics.UnknownSectionKept, diagnostics.SeverityInfo,
			"instructions file has no applyTo; it is imported as always-on context").
			WithPath(file.Path).WithEntity(id))
	}

	desc, _ := doc.FrontMatter.String("description")
	content := adapters.BodyWithoutTitle(doc)
	if strings.TrimSpace(content) == "" {
		c.AddOpaque(file, string(file.Data), "instructions file has no body content",
			adapters.FullSpan(file, doc), true)
		return
	}
	entity := canonical.ContextDocument{
		ID:         id,
		Title:      title,
		Kind:       canonical.KindOther,
		Content:    content,
		Audience:   canonical.AudienceAgent,
		Activation: activation,
		Provenance: c.Provenance(file, adapters.FullSpan(file, doc), provenance.DispositionParsed),
	}
	if strings.TrimSpace(desc) != "" {
		entity.Extensions.Set(string(canonical.TargetCopilot), "description", strings.TrimSpace(desc))
	}
	entity.Extensions.Set(string(canonical.TargetCopilot), "stemma.instructionsFile", path.Base(file.Path))
	c.PreserveUnknownKeys(&entity.Extensions, doc, file, id, "applyTo", "description")
	project.ContextDocuments = append(project.ContextDocuments, entity)
}

// importPrompt maps a .prompt.md file to a canonical procedure.
func importPrompt(c *adapters.ImportCtx, project *canonical.Project, file adapters.SourceFile) {
	doc, ok := c.ParseDocument(file)
	if !ok {
		return
	}
	name := strings.TrimSuffix(path.Base(file.Path), ".prompt.md")
	if v, ok := doc.FrontMatter.String("name"); ok && strings.TrimSpace(v) != "" {
		name = strings.TrimSpace(v)
	}
	id := c.IDs.Allocate(canonical.EntityProcedure, canonical.Slug(name), file.Path)
	desc, _ := doc.FrontMatter.String("description")
	content := adapters.BodyWithoutTitle(doc)
	if strings.TrimSpace(content) == "" {
		c.AddOpaque(file, string(file.Data), "prompt file has no body content",
			adapters.FullSpan(file, doc), true)
		return
	}
	proc := canonical.Procedure{
		ID:          id,
		Name:        name,
		Description: strings.TrimSpace(desc),
		Content:     content,
		Provenance:  c.Provenance(file, adapters.FullSpan(file, doc), provenance.DispositionParsed),
	}
	proc.Extensions.Set(string(canonical.TargetCopilot), "stemma.promptFile", path.Base(file.Path))
	c.PreserveUnknownKeys(&proc.Extensions, doc, file, id, "name", "description")
	project.Procedures = append(project.Procedures, proc)
}

// splitApplyTo splits Copilot's comma-separated glob list.
func splitApplyTo(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, "\"'")
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
