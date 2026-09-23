package codex

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/provenance"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// DefaultProjectDocMaxBytes is Codex's documented default for
// project_doc_max_bytes: "32 KiB by default"
// (https://learn.chatgpt.com/docs/agent-configuration/agents-md).
const DefaultProjectDocMaxBytes = 32 * 1024

// layout decides which file carries each directory's instructions.
//
// Codex reads AGENTS.override.md instead of AGENTS.md when both exist in a
// directory. A directory whose effective file was an override at import keeps
// writing to the override, so that an unchanged project is reproduced byte
// for byte, an edit is written back where it came from, and a preserved
// shadowed AGENTS.md stays shadowed. Every other directory, including every
// directory of a project that was not imported from Codex, uses AGENTS.md.
type layout struct {
	// overrideDirs holds the directories ("" is the root) whose
	// instructions are written to AGENTS.override.md.
	overrideDirs map[string]bool
	// shadowed maps each preserved, shadowed AGENTS.md path to the override
	// path that shadows it.
	shadowed map[string]string
	// preservedFiles maps an instructions file preserved as a whole, because
	// it had nothing to model, to the ID of the opaque block holding its bytes.
	preservedFiles map[string]string
	// legacyOverrides holds override paths of a project imported before
	// override precedence was modelled; each one's only opaque block is the
	// complete file.
	legacyOverrides map[string]bool
	// written records the instruction files this export generated, so that a
	// preserved file is never mistaken for one and an unrelated file (a skill
	// pinned to the same path) still collides visibly.
	written map[string]bool
}

func newLayout(p canonical.Project) layout {
	l := layout{
		overrideDirs: map[string]bool{}, shadowed: map[string]string{}, written: map[string]bool{},
		preservedFiles: map[string]string{}, legacyOverrides: map[string]bool{},
	}
	// Reading a map only builds sets here; nothing is emitted in map order.
	for key, value := range p.Extensions[string(canonical.TargetCodex)] {
		if rel, ok := strings.CutPrefix(key, preservedFileKeyPrefix); ok {
			id, isString := value.(string)
			if isString && (isInstructionsPath(rel, RootFile) || isInstructionsPath(rel, OverrideFile)) {
				l.preservedFiles[rel] = id
			}
			continue
		}
		rel, ok := strings.CutPrefix(key, shadowedKeyPrefix)
		if !ok || !isInstructionsPath(rel, RootFile) {
			continue
		}
		dir := instructionsDir(rel)
		l.shadowed[rel] = joinDir(dir, OverrideFile)
		l.overrideDirs[dir] = true
	}
	// Entities imported from an override keep their directory on the
	// override. Entities imported from an AGENTS.md mean that file was the
	// effective one when the project was imported.
	baseSourced := map[string]bool{}
	mark := func(pv provenance.Provenance) {
		if pv.SourceFormat != string(canonical.TargetCodex) {
			return
		}
		switch {
		case isInstructionsPath(pv.SourcePath, OverrideFile):
			l.overrideDirs[instructionsDir(pv.SourcePath)] = true
		case isInstructionsPath(pv.SourcePath, RootFile):
			baseSourced[instructionsDir(pv.SourcePath)] = true
		}
	}
	for _, e := range p.ContextDocuments {
		mark(e.Provenance)
	}
	for _, e := range p.Rules {
		mark(e.Provenance)
	}
	for _, e := range p.Decisions {
		mark(e.Provenance)
	}
	for _, e := range p.Agents {
		mark(e.Provenance)
	}
	// A preserved override (an empty one, or one with nothing to model) also
	// keeps its directory on the override. The exception is a directory whose
	// AGENTS.md was imported as active guidance: that is a project imported
	// before override precedence was modelled, where the whole override was
	// kept verbatim. Its layout is left exactly as it was, both files
	// included, until the repository is imported again.
	for _, blk := range p.OpaqueBlocks {
		if blk.Provider != string(canonical.TargetCodex) || !isInstructionsPath(blk.SourcePath, OverrideFile) {
			continue
		}
		if dir := instructionsDir(blk.SourcePath); !baseSourced[dir] {
			l.overrideDirs[dir] = true
		} else {
			l.legacyOverrides[blk.SourcePath] = true
		}
	}
	return l
}

// isInstructionsPath reports whether rel is a normalized repository path whose
// file name is name.
func isInstructionsPath(rel, name string) bool {
	clean, err := workspace.NormalizeRel(rel)
	return err == nil && clean == rel && path.Base(rel) == name
}

// joinDir joins a directory ("" for the root) and a file name. The directory
// comes from a normalized path or a validated scope; a failure yields "", which
// the builder refuses to emit.
func joinDir(dir, name string) string {
	p, err := workspace.JoinRel(dir, name)
	if err != nil {
		return ""
	}
	return p
}

// file returns the instructions file for a directory.
func (l layout) file(dir string) string {
	return joinDir(dir, l.name(dir))
}

// name returns the base name of a directory's instructions file. A directory
// pinned by a profile may be spelled differently ("./src/"), so it is
// normalized before the lookup.
func (l layout) name(dir string) string {
	if clean, err := workspace.NormalizeRel(dir); err == nil && l.overrideDirs[clean] {
		return OverrideFile
	}
	if dir == "" && l.overrideDirs[""] {
		return OverrideFile
	}
	return RootFile
}

// supersedeEmpty records the empty override files preserved for dest that a
// generated file now replaces. It must run before the preserved blocks of dest
// are re-emitted into the generated content.
func supersedeEmpty(b *adapters.Builder, dest string) {
	if path.Base(dest) != OverrideFile {
		return
	}
	for _, blk := range b.OpaqueBlocksFor() {
		if blk.SourcePath == dest && blk.ReemitForRoundTrip && isEmptyInstructions([]byte(blk.Content)) {
			b.SupersedeOpaque(blk, dest,
				"The preserved empty override is replaced by generated instructions at the same path, "+
					"which still take precedence over AGENTS.md in that directory.")
		}
	}
}

// emitPreserved writes back preserved override files that no generated file
// replaced, then every shadowed AGENTS.md. It runs after all generated
// instruction files are emitted.
func (l layout) emitPreserved(b *adapters.Builder) {
	blocks := b.OpaqueBlocksFor()

	// A file preserved as a whole is written back as its own file when no
	// generated file took its path (if one did, the block was re-emitted
	// into it). A fragment of a file, such as a heading without content, is
	// never written as if it were the whole file.
	for _, blk := range blocks {
		p := blk.SourcePath
		if !blk.ReemitForRoundTrip || l.written[p] || !l.wholeFile(blk) {
			continue
		}
		if path.Base(p) == OverrideFile && isEmptyInstructions([]byte(blk.Content)) {
			b.EmitInactiveOpaqueFile(blk, adapters.OutcomeExact,
				"The preserved empty override was written back verbatim. Codex skips it as empty, "+
					"and it still keeps the AGENTS.md in its directory from being read.", nil)
		} else {
			b.EmitOpaqueFile(blk)
		}
		l.written[p] = true
	}

	for _, blk := range blocks {
		override, ok := l.shadowed[blk.SourcePath]
		if !ok || !blk.ReemitForRoundTrip {
			// A block that must not be re-emitted is reported by
			// ReportUnemittedOpaque like any other.
			continue
		}
		if l.written[override] {
			b.EmitInactiveOpaqueFile(blk, adapters.OutcomeExact,
				fmt.Sprintf("Preserved inactive content was written back to its own file verbatim. %s "+
					"takes precedence in the same directory, so Codex does not read it.", override), nil)
			continue
		}
		fp := b.Diag(diagnostics.New(diagnostics.ShadowingFileNotGenerated, diagnostics.SeverityWarning,
			"a preserved inactive AGENTS.md is written back, but nothing generated keeps it inactive").
			WithEntity(blk.ID).WithPath(blk.SourcePath).
			WithDetail("%s was imported as inactive because %s took precedence over it. This export "+
				"produces no %s, so if that file is removed, Codex will read the preserved content as "+
				"active guidance.", blk.SourcePath, override, override).
			WithSuggestion("Keep %s, or move the guidance you want into the canonical project and remove "+
				"the preserved block from .stemma/provenance.json.", override))
		b.EmitInactiveOpaqueFile(blk, adapters.OutcomeLossy,
			fmt.Sprintf("Preserved inactive content was written back verbatim, but no %s is generated "+
				"for its directory, so its inactive status depends on a file outside this export.", override),
			[]string{fp})
	}
}

// wholeFile reports whether a preserved block is a complete instructions file
// rather than a fragment of one: a file marked as preserved whole, an empty
// override (whose content is always the whole file), or the verbatim override
// of a project imported before override precedence was modelled.
func (l layout) wholeFile(blk canonical.OpaqueBlock) bool {
	p := blk.SourcePath
	if id, ok := l.preservedFiles[p]; ok && id == blk.ID {
		return true
	}
	if path.Base(p) != OverrideFile {
		return false
	}
	return isEmptyInstructions([]byte(blk.Content)) || l.legacyOverrides[p]
}

// checkChainSize reports where the instruction files Stemma generates would
// exceed Codex's default load limit. Codex concatenates one file per
// directory from the project root down to its working directory, skips empty
// files, and stops adding files once their combined size reaches
// project_doc_max_bytes. Only generated files are counted, so the result is a
// lower bound: files Stemma does not write, and Codex's global file, can only
// add to it. The warning is reported once, on the file where a chain first
// crosses the limit.
func (l layout) checkChainSize(b *adapters.Builder, dirs []string) {
	size := map[string]int{}
	file := map[string]string{}
	for _, dir := range dirs {
		if dir != "" {
			clean, err := workspace.NormalizeRel(dir)
			if err != nil {
				continue
			}
			dir = clean
		}
		f, n, ok := l.effective(b, dir)
		if ok {
			file[dir], size[dir] = f, n
		}
	}
	ordered := make([]string, 0, len(file))
	for dir := range file {
		ordered = append(ordered, dir)
	}
	sort.Strings(ordered)
	for _, dir := range ordered {
		var chain []string
		before := 0
		for _, anc := range ancestors(dir) {
			if f, ok := file[anc]; ok {
				before += size[anc]
				chain = append(chain, fmt.Sprintf("%s (%d bytes)", f, size[anc]))
			}
		}
		total := before + size[dir]
		if total <= DefaultProjectDocMaxBytes || before > DefaultProjectDocMaxBytes {
			continue
		}
		chain = append(chain, fmt.Sprintf("%s (%d bytes)", file[dir], size[dir]))
		b.Diag(diagnostics.New(diagnostics.InstructionChainTooLarge, diagnostics.SeverityWarning,
			"Codex would not load all of these instructions under its default size limit").
			WithPath(file[dir]).
			WithDetail("Codex concatenates the instruction files from the project root down to its "+
				"working directory and stops adding files once their combined size reaches "+
				"project_doc_max_bytes, %d bytes (32 KiB) by default. The generated files on the way "+
				"to %s total %d bytes: %s. With the default configuration this file is truncated or "+
				"not loaded, and instruction files below it are not loaded.",
				DefaultProjectDocMaxBytes, file[dir], total, strings.Join(chain, ", ")).
			WithSuggestion("Shorten the always-on guidance or move it into nested directories. If your " +
				"Codex configuration raises project_doc_max_bytes, accept this diagnostic's fingerprint " +
				"in .stemma/profiles/codex.json."))
	}
}

// effective returns the generated file Codex would read for a directory and
// the bytes it counts against the limit (none for an empty file).
func (l layout) effective(b *adapters.Builder, dir string) (string, int, bool) {
	for _, candidate := range []string{joinDir(dir, OverrideFile), joinDir(dir, RootFile)} {
		if candidate == "" {
			continue
		}
		content, ok := b.FileContent(candidate)
		if !ok {
			continue
		}
		if _, shadowed := l.shadowed[candidate]; shadowed {
			return "", 0, false
		}
		if isEmptyInstructions(content) {
			return candidate, 0, true
		}
		return candidate, len(content), true
	}
	return "", 0, false
}

// ancestors lists the proper ancestors of a directory, root ("") first.
func ancestors(dir string) []string {
	if dir == "" {
		return nil
	}
	out := []string{""}
	parts := strings.Split(dir, "/")
	for i := 1; i < len(parts); i++ {
		out = append(out, strings.Join(parts[:i], "/"))
	}
	return out
}
