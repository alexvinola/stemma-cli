package adapters

import (
	"fmt"
	"path"
	"strings"

	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/diagnostics"
	"github.com/alexvinola/stemma/internal/parser"
	"github.com/alexvinola/stemma/internal/provenance"
	"github.com/alexvinola/stemma/internal/version"
)

// ImportCtx carries the state shared by every importer.
type ImportCtx struct {
	// Provider is the source format identifier.
	Provider canonical.TargetFormat
	// IDs allocates unique entity identifiers.
	IDs *canonical.Allocator
	// Bag collects diagnostics.
	Bag *diagnostics.Bag
	// Opaque collects preserved-but-uninterpreted content.
	Opaque []canonical.OpaqueBlock
}

// Provenance builds a provenance record for a source region.
func (c *ImportCtx) Provenance(file SourceFile, span provenance.Span, disp provenance.Disposition) provenance.Provenance {
	return provenance.Provenance{
		SourceFormat:    string(c.Provider),
		SourcePath:      file.Path,
		SourceHash:      file.Hash,
		Span:            span,
		ImporterVersion: version.ImporterVersion,
		Disposition:     disp,
	}
}

// AddOpaque preserves content Stemma refuses to interpret.
func (c *ImportCtx) AddOpaque(file SourceFile, content, reason string, span provenance.Span, reemit bool) {
	id := c.IDs.Allocate(canonical.EntityOpaque, canonical.Slug(file.Path), file.Path)
	c.Opaque = append(c.Opaque, canonical.OpaqueBlock{
		ID:                 id,
		Provider:           string(c.Provider),
		SourcePath:         file.Path,
		Content:            content,
		Span:               span,
		Reason:             reason,
		Hash:               provenance.HashString(content),
		ReemitForRoundTrip: reemit,
	})
	c.Bag.Add(diagnostics.New(diagnostics.OpaqueBlockKept, diagnostics.SeverityInfo,
		fmt.Sprintf("content preserved without interpretation: %s", reason)).
		WithPath(file.Path).WithEntity(id))
}

// ParseDocument parses a Markdown source file.
//
// When parsing produces a blocking error the whole file is preserved as an
// opaque block and ok is false, so no importer can silently drop it.
func (c *ImportCtx) ParseDocument(file SourceFile) (parser.Document, bool) {
	doc := parser.Parse(file.Path, file.Data)
	c.Bag.Extend(doc.Diagnostics)
	if diagnostics.HasSeverity(doc.Diagnostics, diagnostics.SeverityError) {
		content := string(file.Data)
		if !parserValidUTF8(file.Data) {
			content = ""
		}
		c.AddOpaque(file, content,
			"the file could not be parsed safely; it is preserved verbatim", provenance.Span{}, true)
		return doc, false
	}
	return doc, true
}

func parserValidUTF8(b []byte) bool {
	return strings.ToValidUTF8(string(b), "�") == string(b)
}

// TitleFor derives a human title from front matter, an H1 heading, or a path.
func TitleFor(doc parser.Document, file SourceFile, fallbackKeys ...string) string {
	for _, key := range fallbackKeys {
		if v, ok := doc.FrontMatter.String(key); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	if doc.Title != "" {
		return doc.Title
	}
	base := path.Base(file.Path)
	base = strings.TrimSuffix(base, ".md")
	base = strings.TrimSuffix(base, ".instructions")
	base = strings.TrimSuffix(base, ".prompt")
	base = strings.ReplaceAll(base, "-", " ")
	base = strings.ReplaceAll(base, "_", " ")
	return strings.TrimSpace(base)
}

// BodyWithoutTitle returns the document body with a leading level-1 heading
// removed, so a title is not duplicated inside content.
func BodyWithoutTitle(doc parser.Document) string {
	body := strings.TrimSpace(doc.Body)
	if doc.Title == "" {
		return body
	}
	lines := strings.Split(body, "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "# ") {
		return strings.TrimSpace(strings.Join(lines[1:], "\n"))
	}
	return body
}

// ToolList reads a tool list from front matter under any of the given keys.
func ToolList(fm *parser.FrontMatter, keys ...string) (tools []string, key string, ok bool) {
	for _, k := range keys {
		if !fm.Has(k) {
			continue
		}
		list, valid := fm.StringList(k)
		if !valid {
			continue
		}
		out := make([]string, 0, len(list))
		for _, item := range list {
			for _, part := range strings.Split(item, ",") {
				part = strings.TrimSpace(part)
				if part != "" {
					out = append(out, part)
				}
			}
		}
		return out, k, true
	}
	return nil, "", false
}

// PreserveUnknownKeys stores unrecognised front matter keys as provider
// extensions and reports them.
func (c *ImportCtx) PreserveUnknownKeys(
	ext *canonical.Extensions,
	doc parser.Document,
	file SourceFile,
	entityID string,
	known ...string,
) {
	unknown := doc.FrontMatter.UnknownKeys(known...)
	if len(unknown) == 0 {
		return
	}
	for _, k := range unknown {
		ext.Set(string(c.Provider), k, doc.FrontMatter.Fields[k])
	}
	c.Bag.Add(diagnostics.New(diagnostics.UnknownKeysKept, diagnostics.SeverityInfo,
		fmt.Sprintf("unrecognised front matter keys preserved as %s extensions: %s",
			c.Provider, strings.Join(unknown, ", "))).
		WithPath(file.Path).WithEntity(entityID).
		WithPosition(doc.FrontMatter.StartLine, 1))
}

// FullSpan returns a span covering the whole document.
func FullSpan(file SourceFile, doc parser.Document) provenance.Span {
	return provenance.Span{
		ByteStart: 0,
		ByteEnd:   len(file.Data),
		LineStart: 1,
		LineEnd:   strings.Count(string(file.Data), "\n") + 1,
	}
}

// SkillFromDocument builds a canonical skill from a SKILL.md document.
func (c *ImportCtx) SkillFromDocument(file SourceFile, doc parser.Document, dirName string) canonical.Skill {
	name := dirName
	if v, ok := doc.FrontMatter.String("name"); ok && strings.TrimSpace(v) != "" {
		name = strings.TrimSpace(v)
	}
	id := c.IDs.Allocate(canonical.EntitySkill, canonical.Slug(name), file.Path)
	desc, _ := doc.FrontMatter.String("description")
	tools, toolKey, _ := ToolList(doc.FrontMatter, "allowed-tools", "allowedTools", "tools")
	skill := canonical.Skill{
		ID:           id,
		Name:         name,
		Description:  strings.TrimSpace(desc),
		Content:      BodyWithoutTitle(doc),
		AllowedTools: tools,
		Provenance:   c.Provenance(file, FullSpan(file, doc), provenance.DispositionParsed),
	}
	if toolKey != "" && toolKey != "allowed-tools" {
		skill.Extensions.Set(string(c.Provider), "stemma.toolsKey", toolKey)
	}
	skill.Extensions.Set(string(c.Provider), "stemma.sourceDir", dirName)
	c.PreserveUnknownKeys(&skill.Extensions, doc, file, id,
		"name", "description", "allowed-tools", "allowedTools", "tools")
	if skill.Content == "" {
		skill.Content = strings.TrimSpace(doc.Body)
	}
	return skill
}

// AgentFromDocument builds a canonical agent from a Markdown agent file.
func (c *ImportCtx) AgentFromDocument(file SourceFile, doc parser.Document) canonical.Agent {
	name := strings.TrimSuffix(path.Base(file.Path), ".md")
	if v, ok := doc.FrontMatter.String("name"); ok && strings.TrimSpace(v) != "" {
		name = strings.TrimSpace(v)
	}
	id := c.IDs.Allocate(canonical.EntityAgent, canonical.Slug(name), file.Path)
	desc, _ := doc.FrontMatter.String("description")
	tools, _, _ := ToolList(doc.FrontMatter, "tools", "allowed-tools", "allowedTools")
	model, _ := doc.FrontMatter.String("model")
	agent := canonical.Agent{
		ID:              id,
		Name:            name,
		Description:     strings.TrimSpace(desc),
		Instructions:    BodyWithoutTitle(doc),
		Tools:           tools,
		ModelPreference: strings.TrimSpace(model),
		Provenance:      c.Provenance(file, FullSpan(file, doc), provenance.DispositionParsed),
	}
	agent.Extensions.Set(string(c.Provider), "stemma.sourceFile", path.Base(file.Path))
	c.PreserveUnknownKeys(&agent.Extensions, doc, file, id,
		"name", "description", "tools", "allowed-tools", "allowedTools", "model")
	if agent.Instructions == "" {
		agent.Instructions = strings.TrimSpace(doc.Body)
	}
	return agent
}
