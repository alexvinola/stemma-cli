// Package claude implements the Claude Code adapter.
//
// Recognised paths:
//
//	CLAUDE.md, .claude/CLAUDE.md   always-on project instructions
//	.claude/rules/**/*.md          rules, path-scoped when they declare paths
//	.claude/skills/*/SKILL.md      skills
//	.claude/agents/*.md            subagents
package claude

import (
	"context"
	"strings"

	"github.com/alexvinola/stemma/internal/adapters"
	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/diagnostics"
	"github.com/alexvinola/stemma/internal/discovery"
	"github.com/alexvinola/stemma/internal/globs"
	"github.com/alexvinola/stemma/internal/provenance"
)

// RulesDir is the directory Claude rules live in.
const RulesDir = ".claude/rules"

// Importer converts Claude Code configuration into canonical entities.
type Importer struct{}

// Format implements adapters.Importer.
func (Importer) Format() canonical.TargetFormat { return canonical.TargetClaude }

// Import implements adapters.Importer.
func (Importer) Import(ctx context.Context, in adapters.ImportInput) (adapters.ImportResult, error) {
	var bag diagnostics.Bag
	c := &adapters.ImportCtx{Provider: canonical.TargetClaude, IDs: in.IDs, Bag: &bag}
	project := canonical.NewProject("", "")

	for _, file := range in.Files {
		if err := ctx.Err(); err != nil {
			return adapters.ImportResult{}, err
		}
		switch file.Role {
		case discovery.RoleRootInstructions:
			importMemory(c, &project, file)
		case discovery.RoleRule:
			importRule(c, &project, file)
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
				"file matched the Claude registry but has no importer for its role").WithPath(file.Path))
		}
	}
	project.OpaqueBlocks = append(project.OpaqueBlocks, c.Opaque...)
	return adapters.ImportResult{Project: project, Diagnostics: bag.Items()}, nil
}

// importMemory maps CLAUDE.md to always-on context documents.
func importMemory(c *adapters.ImportCtx, project *canonical.Project, file adapters.SourceFile) {
	doc, ok := c.ParseDocument(file)
	if !ok {
		return
	}
	project.Extensions.Set(string(canonical.TargetClaude), "stemma.rootFile", file.Path)
	if doc.FrontMatter != nil {
		for _, k := range doc.FrontMatter.Keys {
			project.Extensions.Set(string(canonical.TargetClaude), "memory."+k, doc.FrontMatter.Fields[k])
		}
	}
	if imports := findImports(doc.Body); len(imports) > 0 {
		c.Bag.Add(diagnostics.New(diagnostics.UnknownSectionKept, diagnostics.SeverityWarning,
			"the memory file uses @-imports, which are preserved verbatim but not resolved").
			WithPath(file.Path).
			WithDetail("Imported files (%s) are loaded into context at launch by Claude Code, so "+
				"they still cost tokens. Stemma keeps the import lines as ordinary text and does "+
				"not follow them.", strings.Join(imports, ", ")).
			WithSuggestion("Import those files with Stemma separately if you want them modelled."))
	}

	units := adapters.SplitDocument(doc)
	if len(units) == 0 {
		if strings.TrimSpace(string(file.Data)) != "" {
			c.AddOpaque(file, string(file.Data),
				"the memory file has no headings or body text that could be modelled",
				adapters.FullSpan(file, doc), true)
		}
		return
	}
	for _, u := range units {
		title := u.Title
		if title == "" {
			title = firstNonEmpty(doc.Title, "Project instructions")
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

// importRule maps a .claude/rules file to a canonical rule.
//
// The directory name makes the intent structurally explicit, which is why
// these files become rules rather than context documents.
func importRule(c *adapters.ImportCtx, project *canonical.Project, file adapters.SourceFile) {
	doc, ok := c.ParseDocument(file)
	if !ok {
		return
	}
	title := adapters.TitleFor(doc, file, "description")
	id := c.IDs.Allocate(canonical.EntityRule, canonical.Slug(title), file.Path)

	activation := canonical.Always()
	if doc.FrontMatter.Has("paths") {
		patterns, valid := doc.FrontMatter.StringList("paths")
		switch {
		case !valid:
			c.Bag.Add(diagnostics.New(diagnostics.InvalidFrontMatter, diagnostics.SeverityError,
				"the paths field must be a string or a list of strings").
				WithPath(file.Path).WithEntity(id).WithPosition(doc.FrontMatter.StartLine, 1))
			return
		case len(patterns) == 0:
			c.Bag.Add(diagnostics.New(diagnostics.InvalidGlob, diagnostics.SeverityWarning,
				"paths is present but empty; the rule is imported as always-on").
				WithPath(file.Path).WithEntity(id))
		default:
			for _, p := range patterns {
				if err := globs.Validate(p); err != nil {
					c.Bag.Add(diagnostics.New(diagnostics.InvalidGlob, diagnostics.SeverityError,
						"invalid path pattern in rule front matter").
						WithPath(file.Path).WithEntity(id).
						WithPosition(doc.FrontMatter.StartLine, 1).
						WithDetail("%v", err))
					return
				}
			}
			activation = canonical.PathScoped(patterns, nil)
		}
	}

	priority := canonical.PriorityShould
	if v, has := doc.FrontMatter.String("priority"); has {
		p := canonical.Priority(strings.ToLower(strings.TrimSpace(v)))
		if canonical.KnownPriority(p) {
			priority = p
		} else {
			c.Bag.Add(diagnostics.New(diagnostics.InvalidFrontMatter, diagnostics.SeverityWarning,
				"unknown priority in rule front matter; defaulting to \"should\"").
				WithPath(file.Path).WithEntity(id).WithPosition(doc.FrontMatter.StartLine, 1))
		}
	}
	enabled := true
	if v, has := doc.FrontMatter.Bool("enabled"); has {
		enabled = v
	}

	instruction := adapters.BodyWithoutTitle(doc)
	if strings.TrimSpace(instruction) == "" {
		c.AddOpaque(file, string(file.Data), "rule file has no body content",
			adapters.FullSpan(file, doc), true)
		return
	}
	rule := canonical.Rule{
		ID:          id,
		Title:       title,
		Instruction: instruction,
		Priority:    priority,
		Enabled:     enabled,
		Activation:  activation,
		Provenance:  c.Provenance(file, adapters.FullSpan(file, doc), provenance.DispositionParsed),
	}
	rule.Extensions.Set(string(canonical.TargetClaude), "stemma.ruleFile",
		strings.TrimPrefix(file.Path, RulesDir+"/"))
	if desc, has := doc.FrontMatter.String("description"); has && strings.TrimSpace(desc) != "" {
		rule.Extensions.Set(string(canonical.TargetClaude), "description", strings.TrimSpace(desc))
	}
	c.PreserveUnknownKeys(&rule.Extensions, doc, file, id, "paths", "description", "priority", "enabled")
	project.Rules = append(project.Rules, rule)
}

// findImports lists @path references outside code spans and fences.
func findImports(body string) []string {
	var out []string
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		stripped := stripCodeSpans(line)
		for _, field := range strings.Fields(stripped) {
			if len(field) > 1 && strings.HasPrefix(field, "@") {
				out = append(out, strings.Trim(field, "@.,;:"))
			}
		}
	}
	return out
}

func stripCodeSpans(line string) string {
	var b strings.Builder
	inSpan := false
	for _, r := range line {
		if r == '`' {
			inSpan = !inSpan
			continue
		}
		if !inSpan {
			b.WriteRune(r)
		}
	}
	return b.String()
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
