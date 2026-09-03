// Package provenance records where canonical content came from.
package provenance

import (
	"crypto/sha256"
	"encoding/hex"
)

// Disposition records how the source content reached the canonical model.
type Disposition string

const (
	// DispositionParsed means the content was understood and modelled.
	DispositionParsed Disposition = "parsed"
	// DispositionAdapted means the content was modelled with a structural
	// transformation that is not a byte-level round trip.
	DispositionAdapted Disposition = "adapted"
	// DispositionOpaque means the content was preserved verbatim without
	// being interpreted.
	DispositionOpaque Disposition = "preserved-opaque"
)

// Valid reports whether d is a known disposition.
func (d Disposition) Valid() bool {
	switch d {
	case DispositionParsed, DispositionAdapted, DispositionOpaque:
		return true
	default:
		return false
	}
}

// Span identifies a byte and line range inside a source file. Zero values mean
// "unknown" and are omitted from JSON.
type Span struct {
	ByteStart int `json:"byteStart,omitempty"`
	ByteEnd   int `json:"byteEnd,omitempty"`
	LineStart int `json:"lineStart,omitempty"`
	LineEnd   int `json:"lineEnd,omitempty"`
}

// Provenance describes the origin of a canonical entity.
type Provenance struct {
	// SourceFormat is the provider format identifier (for example "claude").
	SourceFormat string `json:"sourceFormat"`
	// SourcePath is the repository-relative path of the source file.
	SourcePath string `json:"sourcePath"`
	// SourceHash is the "sha256:<hex>" digest of the whole source file.
	SourceHash string `json:"sourceHash"`
	// Span locates the content inside the source file when known.
	Span Span `json:"span,omitzero"`
	// ImporterVersion identifies the importer generation.
	ImporterVersion string `json:"importerVersion"`
	// Disposition records how faithfully the content was modelled.
	Disposition Disposition `json:"disposition"`
	// ContentHash is the digest of the canonical entity as it was produced by
	// the importer, ignoring provenance itself. It is what lets Stemma tell
	// "nothing changed since import" from "the canonical project was edited",
	// which is a precondition for re-emitting the original bytes of a file.
	ContentHash string `json:"contentHash,omitempty"`
	// Mappings lists the canonical entity IDs derived from the same source
	// region, so that a file can be traced to everything it produced.
	Mappings []string `json:"mappings,omitempty"`
}

// HashBytes returns the canonical "sha256:<hex>" digest of b.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// HashString returns the canonical digest of s.
func HashString(s string) string {
	return HashBytes([]byte(s))
}
