// Package codex implements the AGENTS.md (Codex) adapter.
//
// Recognised paths:
//
//	AGENTS.md                  always-on instructions
//	<dir>/AGENTS.md            instructions scoped to a directory subtree
//	AGENTS.override.md         takes precedence over AGENTS.md in its directory
//	<dir>/AGENTS.override.md   likewise, for a nested directory
//	.agents/skills/*/SKILL.md  skills
//
// Scoping in this ecosystem is expressed purely by file location. Codex reads
// at most one instructions file per directory, checking AGENTS.override.md
// before AGENTS.md, so the importer resolves the effective file of every
// directory before interpreting anything: see resolveDirectories.
package codex

import (
	"context"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/discovery"
	"github.com/alexvinola/stemma-cli/internal/parser"
	"github.com/alexvinola/stemma-cli/internal/provenance"
)

// RootFile is the root instructions file.
const RootFile = "AGENTS.md"

// OverrideFile is the per-directory file Codex reads instead of AGENTS.md.
const OverrideFile = "AGENTS.override.md"

// SkillsDir is where Codex-style skills live.
const SkillsDir = ".agents/skills"

// shadowedKeyPrefix is the project-level Codex extension key prefix that
// marks a preserved AGENTS.md as shadowed. The full key is the prefix
// followed by the shadowed file's path; the value is the path of the override
// that shadows it. It is Stemma bookkeeping, like every "stemma." key.
const shadowedKeyPrefix = "stemma.shadowed."

// preservedFileKeyPrefix is the project-level Codex extension key prefix that
// marks an instructions file preserved as a whole because it had nothing to
// model. The full key is the prefix followed by the file's path; the value is
// the ID of the opaque block holding its complete bytes. Only a block marked
// this way is ever written back as a file of its own: a fragment of a file
// (such as a heading without content) never is.
const preservedFileKeyPrefix = "stemma.preservedFile."

// Importer converts AGENTS.md configuration into canonical entities.
type Importer struct{}

// Format implements adapters.Importer.
func (Importer) Format() canonical.TargetFormat { return canonical.TargetCodex }

// directoryFiles is what one directory holds: its AGENTS.md and its
// AGENTS.override.md, either of which may be absent.
type directoryFiles struct {
	base, override *adapters.SourceFile
}

// resolveDirectories groups the instruction files by directory.
//
// Codex checks AGENTS.override.md, then AGENTS.md, in each directory and
// includes at most one of them. The file it selects is the first one that
// exists; its content is only inspected afterwards, and an empty file is
// skipped rather than replaced by the next candidate. So whenever an override
// exists, the sibling AGENTS.md is never read, even if the override is empty.
func resolveDirectories(files []adapters.SourceFile) map[string]*directoryFiles {
	dirs := map[string]*directoryFiles{}
	for i := range files {
		f := &files[i]
		var isOverride bool
		switch f.Role {
		case discovery.RoleRootInstructions, discovery.RoleNestedInstructions:
		case discovery.RoleOverride:
			isOverride = true
		default:
			continue
		}
		dir := instructionsDir(f.Path)
		d := dirs[dir]
		if d == nil {
			d = &directoryFiles{}
			dirs[dir] = d
		}
		if isOverride {
			d.override = f
		} else {
			d.base = f
		}
	}
	return dirs
}

// instructionsDir returns the directory of an instructions file, "" for the
// repository root.
func instructionsDir(rel string) string {
	dir := path.Dir(rel)
	if dir == "." {
		return ""
	}
	return dir
}

// isEmptyInstructions reports whether Codex skips a file as empty. Codex
// ignores a file whose text is empty after trimming whitespace; Go's
// strings.TrimSpace uses the same Unicode White_Space property as Rust's
// str::trim, and a byte order mark is not whitespace in either.
func isEmptyInstructions(data []byte) bool {
	return strings.TrimSpace(string(data)) == ""
}

// Import implements adapters.Importer.
func (Importer) Import(ctx context.Context, in adapters.ImportInput) (adapters.ImportResult, error) {
	var bag diagnostics.Bag
	c := &adapters.ImportCtx{Provider: canonical.TargetCodex, IDs: in.IDs, Bag: &bag}
	project := canonical.NewProject("", "")
	dirs := resolveDirectories(in.Files)

	for _, file := range in.Files {
		if err := ctx.Err(); err != nil {
			return adapters.ImportResult{}, err
		}
		switch file.Role {
		case discovery.RoleRootInstructions, discovery.RoleNestedInstructions:
			dir := instructionsDir(file.Path)
			if d := dirs[dir]; d != nil && d.override != nil {
				preserveShadowed(c, &project, file, *d.override)
				continue
			}
			importInstructions(c, &project, file, dir)
		case discovery.RoleOverride:
			if isEmptyInstructions(file.Data) {
				// Codex skips an empty file, and it still keeps the sibling
				// AGENTS.md from being read. The bytes are preserved so that
				// the override keeps doing that after a round trip.
				c.AddOpaque(file, string(file.Data),
					"the override file is empty, so Codex loads no instructions from this directory; "+
						"it is preserved verbatim because it still takes precedence over AGENTS.md",
					adapters.FullSpan(file, parser.Document{}), true)
				continue
			}
			importInstructions(c, &project, file, instructionsDir(file.Path))
		case discovery.RoleSkill:
			doc, ok := c.ParseDocument(file, adapters.SkillFields()...)
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

// preserveShadowed keeps an AGENTS.md that Codex never reads, because an
// AGENTS.override.md exists in the same directory. It is not interpreted:
// its content must never become active guidance for Codex or any other
// target. The bytes are preserved verbatim, marked as shadowed, and written
// back unchanged for Codex.
func preserveShadowed(c *adapters.ImportCtx, project *canonical.Project, file, override adapters.SourceFile) {
	if !utf8.Valid(file.Data) {
		// Canonical storage is UTF-8 text, so these bytes cannot be kept
		// losslessly. Refuse, exactly as for any other non-UTF-8 file.
		c.Bag.Add(diagnostics.New(diagnostics.InvalidEncoding, diagnostics.SeverityError,
			"file is not valid UTF-8").
			WithPath(file.Path).
			WithDetail("Codex does not read this file because %s takes precedence, but Stemma must "+
				"still preserve it and can only store UTF-8 text.", override.Path).
			WithSuggestion("Re-encode the file as UTF-8, or remove it."))
		c.AddOpaque(file, "", "the file is not valid UTF-8 and could not be preserved", provenance.Span{}, true)
		return
	}
	reason := "inactive under Codex precedence: " + override.Path + " exists in the same directory, " +
		"so Codex never reads this file; it is preserved verbatim and never projected as guidance"
	id := c.PreserveOpaque(file, string(file.Data), reason, adapters.FullSpan(file, parser.Document{}), true)
	project.Extensions.Set(string(canonical.TargetCodex), shadowedKeyPrefix+file.Path, override.Path)

	detail := "Codex reads at most one instructions file per directory and checks %s before %s, " +
		"so it never reads this file. Stemma preserves it verbatim as an inactive opaque block, " +
		"writes it back unchanged for Codex, and does not project its content to any target."
	if isEmptyInstructions(override.Data) {
		detail += " The override is empty, so Codex loads no project instructions from this " +
			"directory at all."
	}
	c.Bag.Add(diagnostics.New(diagnostics.ShadowedFilePreserved, diagnostics.SeverityWarning,
		"this AGENTS.md is inactive because an AGENTS.override.md in the same directory takes precedence").
		WithPath(file.Path).WithEntity(id).
		WithDetail(detail, override.Path, file.Path).
		WithSuggestion("If this guidance should apply, move it into %s or remove the override, then re-import.",
			override.Path))
}

// importInstructions maps an AGENTS.md file to context documents. A nested
// file becomes path-scoped context for its own directory subtree.
func importInstructions(c *adapters.ImportCtx, project *canonical.Project, file adapters.SourceFile, dir string) {
	doc, ok := c.ParseDocument(file)
	if !ok {
		return
	}
	units := adapters.SplitDocument(doc)
	if !hasActiveUnit(units) {
		// Nothing here can become guidance: no body text at all, or only
		// headings without content. Codex still reads the file when it is not
		// empty, so the complete original bytes are preserved as one block and
		// written back as the file itself. Reconstructing fragments ("## Title")
		// would lose the H1, front matter, spacing and line endings, and front
		// matter is kept only inside the block, never also as an extension.
		if !isEmptyInstructions(file.Data) {
			id := c.AddOpaque(file, string(file.Data),
				"the instructions file has no body text that could be modelled; the whole file is preserved verbatim",
				adapters.FullSpan(file, doc), true)
			project.Extensions.Set(string(canonical.TargetCodex), preservedFileKeyPrefix+file.Path, id)
		}
		return
	}

	activation := canonical.Always()
	if dir != "" {
		scoped, ok := c.DirectoryActivation(file, doc, dir)
		if !ok {
			return
		}
		activation = scoped
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

// hasActiveUnit reports whether any unit carries body text, which is what
// becomes a canonical entity.
func hasActiveUnit(units []adapters.Unit) bool {
	for _, u := range units {
		if strings.TrimSpace(u.Content) != "" {
			return true
		}
	}
	return false
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
