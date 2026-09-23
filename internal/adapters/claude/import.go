// Package claude implements the Claude Code adapter.
//
// Recognised paths:
//
//	CLAUDE.md, .claude/CLAUDE.md   always-on project instructions
//	<dir>/CLAUDE.md                instructions scoped to a directory subtree
//	.claude/rules/**/*.md          rules, path-scoped when they declare paths
//	.claude/skills/*/SKILL.md      skills
//	.claude/agents/*.md            subagents
package claude

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
		case discovery.RoleNestedInstructions:
			importNestedMemory(c, &project, file, path.Dir(file.Path))
		case discovery.RoleRule:
			importRule(c, &project, file)
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
	warnImports(c, file, doc.Body, "at launch")

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

// importNestedMemory maps a CLAUDE.md below the repository root to context
// scoped to its own directory subtree. Claude Code loads such a file on demand
// when it reads files in that directory, which is what <dir>/** expresses,
// with the directory quoted as a literal (see adapters.DirectoryActivation).
func importNestedMemory(c *adapters.ImportCtx, project *canonical.Project, file adapters.SourceFile, dir string) {
	doc, ok := c.ParseDocument(file)
	if !ok {
		return
	}
	if doc.FrontMatter != nil && len(doc.FrontMatter.Keys) > 0 {
		for _, k := range doc.FrontMatter.Keys {
			project.Extensions.Set(string(canonical.TargetClaude),
				"frontMatter."+file.Path+"."+k, doc.FrontMatter.Fields[k])
		}
		c.Bag.Add(diagnostics.New(diagnostics.UnknownKeysKept, diagnostics.SeverityInfo,
			"front matter on a nested CLAUDE.md file was preserved as a project extension").
			WithPath(file.Path))
	}
	warnImports(c, file, doc.Body, "together with this file")

	activation, scoped := c.DirectoryActivation(file, doc, dir)
	if !scoped {
		return
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
			title = firstNonEmpty(doc.Title, "Instructions for "+dir)
		}
		if strings.TrimSpace(u.Content) == "" {
			c.AddOpaque(file, strings.Repeat("#", maxInt(u.Level, 2))+" "+u.Title,
				"heading with no content", u.Span, true)
			continue
		}
		id := c.IDs.Allocate(canonical.EntityContext, canonical.Slug(dir+"-"+title), file.Path+"#"+title)
		entity := canonical.ContextDocument{
			ID:         id,
			Title:      title,
			Kind:       adapters.KindFromHeading(title),
			Content:    u.Content,
			Audience:   canonical.AudienceAgent,
			Activation: activation,
			Provenance: c.Provenance(file, u.Span, provenance.DispositionParsed),
		}
		entity.Extensions.Set(string(canonical.TargetClaude), "stemma.directory", dir)
		project.ContextDocuments = append(project.ContextDocuments, entity)
	}
}

// warnImports reports @path imports, which Claude Code expands into context
// when the memory file loads. Stemma keeps them as text and never follows them.
func warnImports(c *adapters.ImportCtx, file adapters.SourceFile, body, when string) {
	imports := findImports(body)
	if len(imports) == 0 {
		return
	}
	c.Bag.Add(diagnostics.New(diagnostics.UnknownSectionKept, diagnostics.SeverityWarning,
		"the memory file uses @-imports, which are preserved verbatim but not resolved").
		WithPath(file.Path).
		WithDetail("Imported files (%s) are loaded into context %s by Claude Code, so "+
			"they still cost tokens. Stemma keeps the import lines as ordinary text and does "+
			"not follow them.", strings.Join(imports, ", "), when).
		WithSuggestion("Import those files with Stemma separately if you want them modelled."))
}

// importRule maps a .claude/rules file to a canonical rule.
//
// The directory name makes the intent structurally explicit, which is why
// these files become rules rather than context documents.
func importRule(c *adapters.ImportCtx, project *canonical.Project, file adapters.SourceFile) {
	doc, ok := c.ParseDocument(file,
		parser.FieldSpec{Key: "paths", Type: parser.StringListField},
		parser.FieldSpec{Key: "description", Type: parser.StringField},
		parser.FieldSpec{Key: "priority", Type: parser.StringField},
		parser.FieldSpec{Key: "enabled", Type: parser.BoolField},
	)
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
					c.Bag.Add(diagnostics.New(adapters.GlobErrorCode(err), diagnostics.SeverityError,
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
