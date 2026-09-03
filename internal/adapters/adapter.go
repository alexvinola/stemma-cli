// Package adapters defines the contract every provider adapter implements.
//
// Importers turn provider files into canonical entities; exporters turn
// canonical entities into provider files. Neither touches the filesystem: the
// compiler reads inputs and the workspace layer writes outputs.
package adapters

import (
	"context"
	"io/fs"
	"sort"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/capabilities"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/discovery"
	"github.com/alexvinola/stemma-cli/internal/profiles"
	"github.com/alexvinola/stemma-cli/internal/provenance"
	"github.com/alexvinola/stemma-cli/internal/tokenestimate"
)

// SourceFile is a configuration file handed to an importer.
type SourceFile struct {
	// Path is the repository-relative path.
	Path string
	// Data is the raw file content.
	Data []byte
	// Hash is the digest of Data.
	Hash string
	// Role is what discovery classified the file as.
	Role discovery.Role
	// Mode is the file mode on disk.
	Mode fs.FileMode
}

// ImportInput is everything an importer needs.
type ImportInput struct {
	Files []SourceFile
	// IDs allocates unique, stable entity identifiers.
	IDs *canonical.Allocator
}

// ImportResult is the outcome of importing one provider's files.
type ImportResult struct {
	// Project carries only the entities produced by this importer.
	Project     canonical.Project
	Diagnostics []diagnostics.Diagnostic
}

// Importer converts provider files into canonical entities.
type Importer interface {
	Format() canonical.TargetFormat
	Import(ctx context.Context, in ImportInput) (ImportResult, error)
}

// GeneratedFile is a file an exporter wants written.
type GeneratedFile struct {
	// Path is the repository-relative destination.
	Path string `json:"path"`
	// Content is the exact bytes to write.
	Content []byte `json:"-"`
	// Text mirrors Content for JSON output; generated files are always UTF-8.
	Text string `json:"content"`
	// Mode is the permission bits for newly created files.
	Mode fs.FileMode `json:"-"`
	// ReusedSource reports that the original source bytes were re-emitted
	// verbatim instead of being regenerated.
	ReusedSource bool `json:"reusedSource"`
	// Entities lists the canonical entities that contributed to the file.
	Entities []string `json:"entities"`
}

// Outcome is the exhaustive projection result for one entity and one target.
type Outcome string

const (
	// OutcomeExact: the target represents the canonical entity faithfully.
	OutcomeExact Outcome = "exact"
	// OutcomeAdapted: the meaning survives but the delivery mechanism differs.
	OutcomeAdapted Outcome = "adapted"
	// OutcomeLossy: some canonical information cannot be represented.
	OutcomeLossy Outcome = "lossy"
	// OutcomeBlocked: the entity cannot be projected safely at all.
	OutcomeBlocked Outcome = "blocked"
	// OutcomeSkipped: the entity was excluded on purpose.
	OutcomeSkipped Outcome = "skipped-explicitly"
)

// KnownOutcome reports whether o is one of the five outcomes.
func KnownOutcome(o Outcome) bool {
	switch o {
	case OutcomeExact, OutcomeAdapted, OutcomeLossy, OutcomeBlocked, OutcomeSkipped:
		return true
	default:
		return false
	}
}

// AppliedOverride summarises the profile override used for an entity.
type AppliedOverride struct {
	Include         *bool                 `json:"include,omitempty"`
	Activation      *canonical.Activation `json:"activation,omitempty"`
	Directory       string                `json:"directory,omitempty"`
	Filename        string                `json:"filename,omitempty"`
	AcceptLossy     bool                  `json:"acceptLossy,omitempty"`
	ContentOverride bool                  `json:"contentOverride,omitempty"`
}

// ProjectionMapping explains how one entity reached one target.
type ProjectionMapping struct {
	EntityID    string                 `json:"entityId"`
	EntityType  canonical.EntityType   `json:"entityType"`
	Target      canonical.TargetFormat `json:"target"`
	Outcome     Outcome                `json:"outcome"`
	Files       []string               `json:"files"`
	Diagnostics []string               `json:"diagnostics"`
	Explanation string                 `json:"explanation"`
	Activation  canonical.Activation   `json:"activation"`
	Source      provenance.Provenance  `json:"source,omitzero"`
	Override    *AppliedOverride       `json:"appliedOverride,omitempty"`
	Tokens      int                    `json:"estimatedTokens"`
}

// ExportInput is everything an exporter needs.
type ExportInput struct {
	Project      canonical.Project
	Profile      profiles.Profile
	Capabilities capabilities.Capabilities
	// Tokens accumulates the approximate context cost of the projection.
	Tokens *tokenestimate.Builder
	// Originals holds the current bytes of the target's existing configuration
	// files, keyed by repository-relative path. It enables byte-identical
	// round trips when nothing relevant changed.
	Originals map[string]SourceFile
	// SourceIndex maps a source path to every entity imported from it.
	SourceIndex map[string][]string
}

// ExportResult is the outcome of compiling one target.
type ExportResult struct {
	Files       []GeneratedFile
	Mappings    []ProjectionMapping
	Diagnostics []diagnostics.Diagnostic
}

// Exporter renders canonical entities as provider files.
type Exporter interface {
	Format() canonical.TargetFormat
	Export(ctx context.Context, in ExportInput) (ExportResult, error)
}

// SortFiles orders generated files by path.
func SortFiles(files []GeneratedFile) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
}

// SortMappings orders mappings by entity type then entity ID.
func SortMappings(ms []ProjectionMapping) {
	sort.Slice(ms, func(i, j int) bool {
		if ms[i].EntityType != ms[j].EntityType {
			return ms[i].EntityType < ms[j].EntityType
		}
		return ms[i].EntityID < ms[j].EntityID
	})
}
