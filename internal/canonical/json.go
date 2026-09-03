package canonical

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/alexvinola/stemma-cli/internal/provenance"
	"github.com/alexvinola/stemma-cli/internal/version"
)

const schemaVersion = version.CanonicalSchemaVersion

// MaxProjectBytes bounds the size of a canonical project document.
const MaxProjectBytes = 32 << 20 // 32 MiB

// Sort orders every collection deterministically by entity ID. Stemma never
// relies on input order or map iteration order for output stability.
func (p *Project) Sort() {
	SortTargets(p.Targets)
	sort.SliceStable(p.ContextDocuments, func(i, j int) bool {
		return p.ContextDocuments[i].ID < p.ContextDocuments[j].ID
	})
	sort.SliceStable(p.Rules, func(i, j int) bool { return p.Rules[i].ID < p.Rules[j].ID })
	sort.SliceStable(p.Procedures, func(i, j int) bool { return p.Procedures[i].ID < p.Procedures[j].ID })
	sort.SliceStable(p.Skills, func(i, j int) bool { return p.Skills[i].ID < p.Skills[j].ID })
	sort.SliceStable(p.Agents, func(i, j int) bool { return p.Agents[i].ID < p.Agents[j].ID })
	sort.SliceStable(p.Decisions, func(i, j int) bool { return p.Decisions[i].ID < p.Decisions[j].ID })
	sort.SliceStable(p.OpaqueBlocks, func(i, j int) bool {
		if p.OpaqueBlocks[i].SourcePath != p.OpaqueBlocks[j].SourcePath {
			return p.OpaqueBlocks[i].SourcePath < p.OpaqueBlocks[j].SourcePath
		}
		if p.OpaqueBlocks[i].Span.ByteStart != p.OpaqueBlocks[j].Span.ByteStart {
			return p.OpaqueBlocks[i].Span.ByteStart < p.OpaqueBlocks[j].Span.ByteStart
		}
		return p.OpaqueBlocks[i].ID < p.OpaqueBlocks[j].ID
	})
}

// normalizeSlices replaces nil collections with empty ones so that JSON output
// is stable regardless of how the project was constructed.
func (p *Project) normalizeSlices() {
	if p.Targets == nil {
		p.Targets = []TargetFormat{}
	}
	if p.ContextDocuments == nil {
		p.ContextDocuments = []ContextDocument{}
	}
	if p.Rules == nil {
		p.Rules = []Rule{}
	}
	if p.Procedures == nil {
		p.Procedures = []Procedure{}
	}
	if p.Skills == nil {
		p.Skills = []Skill{}
	}
	if p.Agents == nil {
		p.Agents = []Agent{}
	}
	if p.Decisions == nil {
		p.Decisions = []Decision{}
	}
	if p.OpaqueBlocks == nil {
		p.OpaqueBlocks = []OpaqueBlock{}
	}
}

// MarshalProject renders a project as canonical, deterministic JSON with a
// trailing newline. Map keys are sorted by encoding/json; slices are sorted by
// Sort before encoding.
func MarshalProject(p Project) ([]byte, error) {
	p.SchemaVersion = schemaVersion
	p.normalizeSlices()
	p.Sort()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return nil, fmt.Errorf("encode canonical project: %w", err)
	}
	return buf.Bytes(), nil
}

// UnmarshalProject decodes a canonical project, rejecting unknown fields and
// unsupported schema versions.
func UnmarshalProject(data []byte) (Project, error) {
	var p Project
	if len(data) > MaxProjectBytes {
		return p, fmt.Errorf("canonical project exceeds %d bytes", MaxProjectBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Project{}, fmt.Errorf("decode canonical project: %w", err)
	}
	if dec.More() {
		return Project{}, fmt.Errorf("decode canonical project: trailing content after JSON document")
	}
	if p.SchemaVersion != schemaVersion {
		return Project{}, fmt.Errorf("unsupported canonical schema version %d (this build supports %d)",
			p.SchemaVersion, schemaVersion)
	}
	p.normalizeSlices()
	return p, nil
}

// Hash returns the digest of the canonical serialization of the project.
func Hash(p Project) (string, error) {
	b, err := MarshalProject(p)
	if err != nil {
		return "", err
	}
	return provenance.HashBytes(b), nil
}
