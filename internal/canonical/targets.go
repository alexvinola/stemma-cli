// Package canonical defines Stemma's provider-neutral semantic model.
//
// The canonical model is the single source of truth once a project has been
// imported. Provider files are projections of it, never the reverse.
package canonical

import "sort"

// TargetFormat identifies a coding-agent ecosystem.
type TargetFormat string

const (
	TargetCopilot TargetFormat = "github-copilot"
	TargetClaude  TargetFormat = "claude"
	TargetCodex   TargetFormat = "codex"
	TargetKiro    TargetFormat = "kiro"
	TargetCursor  TargetFormat = "cursor"
)

// AllTargets lists every target identifier Stemma knows about, including
// targets that are declared but not yet implemented.
func AllTargets() []TargetFormat {
	return []TargetFormat{TargetClaude, TargetCodex, TargetCopilot, TargetCursor, TargetKiro}
}

// KnownTarget reports whether t is a recognised target identifier.
func KnownTarget(t TargetFormat) bool {
	for _, k := range AllTargets() {
		if k == t {
			return true
		}
	}
	return false
}

// SortTargets orders targets deterministically by identifier.
func SortTargets(ts []TargetFormat) {
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
}

// EntityType names a canonical entity kind. Entity IDs are prefixed with it.
type EntityType string

const (
	EntityContext   EntityType = "context"
	EntityRule      EntityType = "rule"
	EntityProcedure EntityType = "procedure"
	EntitySkill     EntityType = "skill"
	EntityAgent     EntityType = "agent"
	EntityDecision  EntityType = "decision"
	EntityOpaque    EntityType = "opaque"
)

// AllEntityTypes lists every canonical entity type.
func AllEntityTypes() []EntityType {
	return []EntityType{
		EntityAgent, EntityContext, EntityDecision, EntityOpaque,
		EntityProcedure, EntityRule, EntitySkill,
	}
}
