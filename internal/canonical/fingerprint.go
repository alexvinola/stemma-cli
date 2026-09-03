package canonical

import (
	"encoding/json"

	"github.com/alexvinola/stemma-cli/internal/provenance"
)

// EntityFingerprint returns a digest of an entity's canonical content,
// ignoring its provenance.
//
// It answers one question: is this entity still exactly what the importer
// produced? Re-emitting a source file verbatim is only safe when the answer is
// yes for every entity that file produced, so that a hand edit to
// .stemma/project.json can never be silently discarded.
func EntityFingerprint(p Project, id string) (string, bool) {
	var value any
	switch {
	case matchID(p.ContextDocuments, id, func(e ContextDocument) any { e.Provenance = provenance.Provenance{}; return e }, &value):
	case matchID(p.Rules, id, func(e Rule) any { e.Provenance = provenance.Provenance{}; return e }, &value):
	case matchID(p.Procedures, id, func(e Procedure) any { e.Provenance = provenance.Provenance{}; return e }, &value):
	case matchID(p.Skills, id, func(e Skill) any { e.Provenance = provenance.Provenance{}; return e }, &value):
	case matchID(p.Agents, id, func(e Agent) any { e.Provenance = provenance.Provenance{}; return e }, &value):
	case matchID(p.Decisions, id, func(e Decision) any { e.Provenance = provenance.Provenance{}; return e }, &value):
	default:
		return "", false
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return provenance.HashBytes(data), true
}

// identified is implemented by every canonical entity.
type identified interface {
	ContextDocument | Rule | Procedure | Skill | Agent | Decision
}

func matchID[T identified](items []T, id string, strip func(T) any, out *any) bool {
	for _, item := range items {
		if entityID(item) != id {
			continue
		}
		*out = strip(item)
		return true
	}
	return false
}

func entityID(v any) string {
	switch e := v.(type) {
	case ContextDocument:
		return e.ID
	case Rule:
		return e.ID
	case Procedure:
		return e.ID
	case Skill:
		return e.ID
	case Agent:
		return e.ID
	case Decision:
		return e.ID
	default:
		return ""
	}
}

// StampContentHashes fills in provenance.ContentHash for every entity that
// records a source path. It runs once, right after an import.
func StampContentHashes(p *Project) {
	for i := range p.ContextDocuments {
		p.ContextDocuments[i].Provenance.ContentHash = ""
		if h, ok := EntityFingerprint(*p, p.ContextDocuments[i].ID); ok {
			p.ContextDocuments[i].Provenance.ContentHash = h
		}
	}
	for i := range p.Rules {
		p.Rules[i].Provenance.ContentHash = ""
		if h, ok := EntityFingerprint(*p, p.Rules[i].ID); ok {
			p.Rules[i].Provenance.ContentHash = h
		}
	}
	for i := range p.Procedures {
		p.Procedures[i].Provenance.ContentHash = ""
		if h, ok := EntityFingerprint(*p, p.Procedures[i].ID); ok {
			p.Procedures[i].Provenance.ContentHash = h
		}
	}
	for i := range p.Skills {
		p.Skills[i].Provenance.ContentHash = ""
		if h, ok := EntityFingerprint(*p, p.Skills[i].ID); ok {
			p.Skills[i].Provenance.ContentHash = h
		}
	}
	for i := range p.Agents {
		p.Agents[i].Provenance.ContentHash = ""
		if h, ok := EntityFingerprint(*p, p.Agents[i].ID); ok {
			p.Agents[i].Provenance.ContentHash = h
		}
	}
	for i := range p.Decisions {
		p.Decisions[i].Provenance.ContentHash = ""
		if h, ok := EntityFingerprint(*p, p.Decisions[i].ID); ok {
			p.Decisions[i].Provenance.ContentHash = h
		}
	}
}
