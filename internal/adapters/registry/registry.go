// Package registry maps target identifiers to provider adapters.
//
// The mapping is an explicit switch rather than init-time registration, so
// there is no global mutable state and the set of implemented providers is
// visible in one place.
package registry

import (
	"github.com/alexvinola/stemma/internal/adapters"
	"github.com/alexvinola/stemma/internal/adapters/claude"
	"github.com/alexvinola/stemma/internal/adapters/codex"
	"github.com/alexvinola/stemma/internal/adapters/copilot"
	"github.com/alexvinola/stemma/internal/adapters/kiro"
	"github.com/alexvinola/stemma/internal/canonical"
)

// Importer returns the importer for a format, if one is implemented.
func Importer(f canonical.TargetFormat) (adapters.Importer, bool) {
	switch f {
	case canonical.TargetCopilot:
		return copilot.Importer{}, true
	case canonical.TargetClaude:
		return claude.Importer{}, true
	case canonical.TargetCodex:
		return codex.Importer{}, true
	case canonical.TargetKiro:
		return kiro.Importer{}, true
	default:
		// Cursor is declared but not implemented.
		return nil, false
	}
}

// Exporter returns the exporter for a format, if one is implemented.
func Exporter(f canonical.TargetFormat) (adapters.Exporter, bool) {
	switch f {
	case canonical.TargetCopilot:
		return copilot.Exporter{}, true
	case canonical.TargetClaude:
		return claude.Exporter{}, true
	case canonical.TargetCodex:
		return codex.Exporter{}, true
	case canonical.TargetKiro:
		return kiro.Exporter{}, true
	default:
		return nil, false
	}
}

// Implemented lists the formats with both an importer and an exporter.
func Implemented() []canonical.TargetFormat {
	var out []canonical.TargetFormat
	for _, f := range canonical.AllTargets() {
		_, imp := Importer(f)
		_, exp := Exporter(f)
		if imp && exp {
			out = append(out, f)
		}
	}
	return out
}
