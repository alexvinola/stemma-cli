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

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/discovery"
	"github.com/alexvinola/stemma-cli/internal/globs"
	"github.com/alexvinola/stemma-cli/internal/parser"
	"github.com/alexvinola/stemma-cli/internal/provenance"
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
			doc, ok := c.ParseDocument(file, adapters.SkillFields()...)
			if !ok {
				continue
			}
			project.Skills = append(project.Skills, c.SkillFromDocument(file, doc, discovery.SkillName(file.Path)))
		case discovery.RoleAgent:
			doc, ok := c.ParseDocument(file, adapters.AgentFields()...)
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
	doc, ok := c.ParseDocument(file,
		parser.FieldSpec{Key: "applyTo", Type: parser.StringField},
		parser.FieldSpec{Key: "description", Type: parser.StringField},
	)
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
			for _, p := range patterns {
				if err := globs.Validate(p); err != nil {
					c.Bag.Add(diagnostics.New(adapters.GlobErrorCode(err), diagnostics.SeverityError,
						"invalid pattern in applyTo").
						WithPath(file.Path).WithEntity(id).
						WithPosition(doc.FrontMatter.StartLine, 1).
						WithDetail("%v", err).
						WithSuggestion("applyTo separates patterns with commas, so a brace group " +
							"split across two of them leaves a pattern that matches nothing. " +
							"Close the group, or write its alternatives as separate patterns."))
					return
				}
			}
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
	doc, ok := c.ParseDocument(file,
		parser.FieldSpec{Key: "name", Type: parser.StringField},
		parser.FieldSpec{Key: "description", Type: parser.StringField},
	)
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

// splitApplyTo splits Copilot's comma-separated applyTo into patterns.
//
// The split honours brace nesting: a comma inside a brace group belongs to the
// group, not to the list. Splitting blindly is what silently turned
// "src/**/*.{ts,tsx}" into the two patterns "src/**/*.{ts" and "tsx}", neither
// of which matches a file. A group left unclosed at the end of the string is
// exactly that corruption, so its commas are treated as separators again and
// the resulting patterns are rejected by validation rather than accepted as a
// scope that matches nothing.
func splitApplyTo(v string) []string {
	parts := splitTopLevel(v)
	if !allBalanced(parts) {
		parts = strings.Split(v, ",")
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, "\"'")
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// splitTopLevel splits on the commas that are not inside a brace group.
func splitTopLevel(v string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(v); i++ {
		if end, ok := globs.ClassEnd(v, i); ok {
			i = end
			continue
		}
		switch v[i] {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, v[start:i])
				start = i + 1
			}
		}
	}
	return append(out, v[start:])
}

// allBalanced reports whether every part closes the brace groups it opens.
func allBalanced(parts []string) bool {
	for _, p := range parts {
		depth := 0
		for i := 0; i < len(p); i++ {
			if end, ok := globs.ClassEnd(p, i); ok {
				i = end
				continue
			}
			switch p[i] {
			case '{':
				depth++
			case '}':
				if depth > 0 {
					depth--
				}
			}
		}
		if depth != 0 {
			return false
		}
	}
	return true
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
