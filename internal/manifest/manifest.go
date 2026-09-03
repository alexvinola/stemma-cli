// Package manifest records what Stemma imported and generated.
//
// The manifest lets Stemma tell "a file I generated" apart from "a file the
// user wrote", which is what makes safe updates and conflict detection
// possible. Timestamps are recorded as metadata only: they never take part in
// hashing, planning or generated output.
package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/alexvinola/stemma/internal/provenance"
	"github.com/alexvinola/stemma/internal/version"
)

// MaxManifestBytes bounds a manifest document.
const MaxManifestBytes = 16 << 20

// SourceRecord is an imported provider file.
type SourceRecord struct {
	Path   string `json:"path"`
	Hash   string `json:"hash"`
	Format string `json:"format"`
}

// GeneratedRecord is a file Stemma wrote for a target.
type GeneratedRecord struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	// Entities lists the canonical entity IDs that contributed to the file.
	Entities []string `json:"entities"`
}

// TargetRecord is the state of one compiled target.
type TargetRecord struct {
	ProfileHash string `json:"profileHash"`
	// ProjectHash is the canonical project hash at the last successful apply.
	ProjectHash    string            `json:"projectHash"`
	GeneratedFiles []GeneratedRecord `json:"generatedFiles"`
	// AcceptedDiagnostics records fingerprints accepted at apply time.
	AcceptedDiagnostics []string `json:"acceptedDiagnostics"`
	// CompatibilityBaseline identifies the provider baseline used.
	CompatibilityBaseline string `json:"compatibilityBaseline"`
	// StemmaVersion is the compiler version that produced the files.
	StemmaVersion string `json:"stemmaVersion"`
	// AppliedAt is metadata only and never affects compilation.
	AppliedAt string `json:"appliedAt,omitempty"`
}

// Manifest is the repository-local record of Stemma's activity.
type Manifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	StemmaVersion string `json:"stemmaVersion"`
	// ImportedSources lists the provider files the project was imported from.
	ImportedSources []SourceRecord `json:"importedSources"`
	// ImportedFormat is the provider format the project was imported from.
	ImportedFormat string `json:"importedFormat,omitempty"`
	// ProjectHash is the canonical project hash at the last import.
	ProjectHash string `json:"projectHash,omitempty"`
	// Targets is keyed by target identifier.
	Targets map[string]TargetRecord `json:"targets"`
	// LastTarget is the most recently applied target.
	LastTarget string `json:"lastTarget,omitempty"`
}

// New returns an empty manifest.
func New() Manifest {
	return Manifest{
		SchemaVersion:   version.ManifestSchemaVersion,
		StemmaVersion:   version.Version,
		ImportedSources: []SourceRecord{},
		Targets:         map[string]TargetRecord{},
	}
}

// Marshal renders the manifest deterministically.
func Marshal(m Manifest) ([]byte, error) {
	m.SchemaVersion = version.ManifestSchemaVersion
	if m.ImportedSources == nil {
		m.ImportedSources = []SourceRecord{}
	}
	if m.Targets == nil {
		m.Targets = map[string]TargetRecord{}
	}
	sort.Slice(m.ImportedSources, func(i, j int) bool {
		return m.ImportedSources[i].Path < m.ImportedSources[j].Path
	})
	normalized := make(map[string]TargetRecord, len(m.Targets))
	for k, t := range m.Targets {
		if t.GeneratedFiles == nil {
			t.GeneratedFiles = []GeneratedRecord{}
		}
		if t.AcceptedDiagnostics == nil {
			t.AcceptedDiagnostics = []string{}
		}
		files := append([]GeneratedRecord{}, t.GeneratedFiles...)
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		for i := range files {
			ents := append([]string{}, files[i].Entities...)
			sort.Strings(ents)
			if ents == nil {
				ents = []string{}
			}
			files[i].Entities = ents
		}
		t.GeneratedFiles = files
		accepted := append([]string{}, t.AcceptedDiagnostics...)
		sort.Strings(accepted)
		t.AcceptedDiagnostics = accepted
		normalized[k] = t
	}
	m.Targets = normalized
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	return buf.Bytes(), nil
}

// Unmarshal decodes a manifest, rejecting unknown fields.
func Unmarshal(data []byte) (Manifest, error) {
	if len(data) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("manifest exceeds %d bytes", MaxManifestBytes)
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if dec.More() {
		return Manifest{}, fmt.Errorf("decode manifest: trailing content after JSON document")
	}
	if m.SchemaVersion != version.ManifestSchemaVersion {
		return Manifest{}, fmt.Errorf("unsupported manifest schema version %d (this build supports %d)",
			m.SchemaVersion, version.ManifestSchemaVersion)
	}
	if m.Targets == nil {
		m.Targets = map[string]TargetRecord{}
	}
	if m.ImportedSources == nil {
		m.ImportedSources = []SourceRecord{}
	}
	return m, nil
}

// Hash returns the digest of the serialized manifest.
func Hash(m Manifest) (string, error) {
	b, err := Marshal(m)
	if err != nil {
		return "", err
	}
	return provenance.HashBytes(b), nil
}

// Tracked reports whether a path was generated by Stemma for the target, and
// the hash it had when it was written.
func (m Manifest) Tracked(target, path string) (hash string, ok bool) {
	rec, ok := m.Targets[target]
	if !ok {
		return "", false
	}
	for _, f := range rec.GeneratedFiles {
		if f.Path == path {
			return f.Hash, true
		}
	}
	return "", false
}

// TrackedAnyTarget reports whether a path is tracked for any target.
func (m Manifest) TrackedAnyTarget(path string) (hash string, ok bool) {
	targets := make([]string, 0, len(m.Targets))
	for t := range m.Targets {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	for _, t := range targets {
		if h, found := m.Tracked(t, path); found {
			return h, true
		}
	}
	return "", false
}

// TrackedPaths lists the paths generated for a target, sorted.
func (m Manifest) TrackedPaths(target string) []string {
	rec, ok := m.Targets[target]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(rec.GeneratedFiles))
	for _, f := range rec.GeneratedFiles {
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return out
}
