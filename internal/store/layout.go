package store

import (
	"path"

	"github.com/alexvinola/stemma-cli/internal/canonical"
)

// The canonical project is stored as a directory of Markdown files, one per
// entity, plus a small JSON file for project-level metadata.
//
// The reason is ergonomic and deliberate: the canonical project is meant to be
// edited by hand, and almost everything in it is multi-line Markdown. Holding
// that inside JSON strings made the source of truth the least pleasant file in
// the repository to work with, and produced diffs where changing one word
// rewrote an entire line.
//
// Machine bookkeeping (provenance, hashes, preserved opaque content) is kept
// out of those files, in provenance.json, so that what a person edits contains
// only what a person wrote.
const (
	// ProvenanceFile records where each entity came from. It is written by
	// import and never edited by hand; losing it costs byte-identical round
	// trips, not correctness.
	ProvenanceFile = ".stemma/provenance.json"

	ContextDir    = ".stemma/context"
	RulesDir      = ".stemma/rules"
	ProceduresDir = ".stemma/procedures"
	SkillsDir     = ".stemma/skills"
	AgentsDir     = ".stemma/agents"
	DecisionsDir  = ".stemma/decisions"
)

// entityDirs maps each entity type to its directory.
var entityDirs = map[canonical.EntityType]string{
	canonical.EntityContext:   ContextDir,
	canonical.EntityRule:      RulesDir,
	canonical.EntityProcedure: ProceduresDir,
	canonical.EntitySkill:     SkillsDir,
	canonical.EntityAgent:     AgentsDir,
	canonical.EntityDecision:  DecisionsDir,
}

// EntityDirs returns the entity directories in deterministic order.
func EntityDirs() []struct {
	Type canonical.EntityType
	Dir  string
} {
	out := make([]struct {
		Type canonical.EntityType
		Dir  string
	}, 0, len(entityDirs))
	for _, t := range canonical.AllEntityTypes() {
		dir, ok := entityDirs[t]
		if !ok {
			continue
		}
		out = append(out, struct {
			Type canonical.EntityType
			Dir  string
		}{Type: t, Dir: dir})
	}
	return out
}

// EntityPath returns the file an entity is stored in. The slug of the id is
// the file name, so the filesystem mirrors the entity ids.
func EntityPath(id string) (string, error) {
	kind, slug, err := canonical.ParseID(id)
	if err != nil {
		return "", err
	}
	dir, ok := entityDirs[kind]
	if !ok {
		return "", err
	}
	return path.Join(dir, slug+".md"), nil
}

// IDFromPath derives an entity id from its file path.
func IDFromPath(rel string) (string, bool) {
	dir := path.Dir(rel)
	base := path.Base(rel)
	if len(base) <= 3 || base[len(base)-3:] != ".md" {
		return "", false
	}
	slug := base[:len(base)-3]
	for kind, d := range entityDirs {
		if d == dir {
			return canonical.MakeID(kind, slug), true
		}
	}
	return "", false
}
