// Package codex implements the AGENTS.md (Codex) adapter.
//
// Recognised paths:
//
//	AGENTS.md                  always-on instructions
//	<dir>/AGENTS.md            instructions scoped to a directory subtree
//	AGENTS.override.md         preserved verbatim; its semantics are not modelled
//	.agents/skills/*/SKILL.md  skills
//
// Scoping in this ecosystem is expressed purely by file location: the agent
// reads the nearest AGENTS.md in the directory tree.
package codex

import (
	"context"
	"path"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/discovery"
	"github.com/alexvinola/stemma-cli/internal/provenance"
)

// RootFile is the root instructions file.
const RootFile = "AGENTS.md"

// SkillsDir is where Codex-style skills live.
const SkillsDir = ".agents/skills"

// Importer converts AGENTS.md configuration into canonical entities.
type Importer struct{}

// Format implements adapters.Importer.
func (Importer) Format() canonical.TargetFormat { return canonical.TargetCodex }

// Import implements adapters.Importer.
func (Importer) Import(ctx context.Context, in adapters.ImportInput) (adapters.ImportResult, error) {
	var bag diagnostics.Bag
	c := &adapters.ImportCtx{Provider: canonical.TargetCodex, IDs: in.IDs, Bag: &bag}
	project := canonical.NewProject("", "")

	for _, file := range in.Files {
		if err := ctx.Err(); err != nil {
			return adapters.ImportResult{}, err
		}
		switch file.Role {
		case discovery.RoleRootInstructions:
			importInstructions(c, &project, file, "")
		case discovery.RoleNestedInstructions:
			importInstructions(c, &project, file, path.Dir(file.Path))
		case discovery.RoleOverride:
			// Override semantics are not part of the canonical model. The file
			// is preserved verbatim so that it can be written back unchanged.
			c.AddOpaque(file, string(file.Data),
				"AGENTS.override.md semantics are not modelled by Stemma; the file is preserved verbatim",
				provenance.Span{ByteStart: 0, ByteEnd: len(file.Data), LineStart: 1,
					LineEnd: strings.Count(string(file.Data), "\n") + 1},
				true)
		case discovery.RoleSkill:
			doc, ok := c.ParseDocument(file)
			if !ok {
				continue
			}
			project.Skills = append(project.Skills, c.SkillFromDocument(file, doc, discovery.SkillName(file.Path)))
		default:
			bag.Add(diagnostics.New(diagnostics.UnrecognizedFormat, diagnostics.SeverityWarning,
				"file matched the Codex registry but has no importer for its role").WithPath(file.Path))
		}
	}
	project.OpaqueBlocks = append(project.OpaqueBlocks, c.Opaque...)
	return adapters.ImportResult{Project: project, Diagnostics: bag.Items()}, nil
}

// importInstructions maps an AGENTS.md file to context documents. A nested
// file becomes path-scoped context for its own directory subtree.
func importInstructions(c *adapters.ImportCtx, project *canonical.Project, file adapters.SourceFile, dir string) {
	doc, ok := c.ParseDocument(file)
	if !ok {
		return
	}
	if doc.FrontMatter != nil && len(doc.FrontMatter.Keys) > 0 {
		for _, k := range doc.FrontMatter.Keys {
			project.Extensions.Set(string(canonical.TargetCodex),
				"frontMatter."+file.Path+"."+k, doc.FrontMatter.Fields[k])
		}
		c.Bag.Add(diagnostics.New(diagnostics.UnknownKeysKept, diagnostics.SeverityInfo,
			"front matter on an AGENTS.md file was preserved as a project extension").
			WithPath(file.Path))
	}

	activation := canonical.Always()
	if dir != "" {
		activation = canonical.PathScoped([]string{dir + "/**"}, nil)
	}

	units := adapters.SplitDocument(doc)
	if len(units) == 0 {
		if strings.TrimSpace(string(file.Data)) != "" {
			c.AddOpaque(file, string(file.Data),
				"the instructions file has no headings or body text that could be modelled",
				adapters.FullSpan(file, doc), true)
		}
		return
	}
	for _, u := range units {
		title := u.Title
		if title == "" {
			title = firstNonEmpty(doc.Title, defaultTitle(dir))
		}
		if strings.TrimSpace(u.Content) == "" {
			c.AddOpaque(file, strings.Repeat("#", maxInt(u.Level, 2))+" "+u.Title,
				"heading with no content", u.Span, true)
			continue
		}
		slug := canonical.Slug(title)
		if dir != "" {
			slug = canonical.Slug(dir + "-" + title)
		}
		id := c.IDs.Allocate(canonical.EntityContext, slug, file.Path+"#"+title)
		entity := canonical.ContextDocument{
			ID:         id,
			Title:      title,
			Kind:       adapters.KindFromHeading(title),
			Content:    u.Content,
			Audience:   canonical.AudienceAgent,
			Activation: activation,
			Provenance: c.Provenance(file, u.Span, provenance.DispositionParsed),
		}
		if dir != "" {
			entity.Extensions.Set(string(canonical.TargetCodex), "stemma.directory", dir)
		}
		project.ContextDocuments = append(project.ContextDocuments, entity)
	}
}

func defaultTitle(dir string) string {
	if dir == "" {
		return "Repository instructions"
	}
	return "Instructions for " + dir
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
