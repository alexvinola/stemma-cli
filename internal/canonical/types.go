package canonical

import (
	"github.com/alexvinola/stemma/internal/provenance"
)

// Extensions carries provider-specific data that Stemma preserves but does not
// interpret. Keys are provider identifiers ("claude", "kiro", ...) mapping to
// arbitrary decoded values. encoding/json marshals map keys in sorted order,
// which keeps output deterministic.
type Extensions map[string]map[string]any

// Set stores a provider-specific value, allocating as needed.
func (e *Extensions) Set(provider, key string, value any) {
	if *e == nil {
		*e = Extensions{}
	}
	if (*e)[provider] == nil {
		(*e)[provider] = map[string]any{}
	}
	(*e)[provider][key] = value
}

// Get returns a provider-specific value.
func (e Extensions) Get(provider, key string) (any, bool) {
	if e == nil {
		return nil, false
	}
	m, ok := e[provider]
	if !ok {
		return nil, false
	}
	v, ok := m[key]
	return v, ok
}

// GetString returns a provider-specific string value.
func (e Extensions) GetString(provider, key string) (string, bool) {
	v, ok := e.Get(provider, key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// ContextKind classifies a context document. Kinds are only assigned when the
// importer has a deterministic structural reason; otherwise KindOther is used.
type ContextKind string

const (
	KindProduct      ContextKind = "product"
	KindTechnology   ContextKind = "technology"
	KindArchitecture ContextKind = "architecture"
	KindStructure    ContextKind = "structure"
	KindDomain       ContextKind = "domain"
	KindConventions  ContextKind = "conventions"
	KindSecurity     ContextKind = "security"
	KindTesting      ContextKind = "testing"
	KindOperations   ContextKind = "operations"
	KindOther        ContextKind = "other"
)

// AllContextKinds lists every recognised context kind.
func AllContextKinds() []ContextKind {
	return []ContextKind{
		KindArchitecture, KindConventions, KindDomain, KindOperations, KindOther,
		KindProduct, KindSecurity, KindStructure, KindTechnology, KindTesting,
	}
}

// KnownContextKind reports whether k is recognised.
func KnownContextKind(k ContextKind) bool {
	for _, c := range AllContextKinds() {
		if c == k {
			return true
		}
	}
	return false
}

// Audience says who the content is written for.
type Audience string

const (
	// AudienceAgent: content is intended to be loaded by a coding agent.
	AudienceAgent Audience = "agent"
	// AudienceHuman: content is rationale or background for people.
	AudienceHuman Audience = "human"
	// AudienceBoth: useful to both, exported to agents when activation allows.
	AudienceBoth Audience = "both"
)

// KnownAudience reports whether a is recognised.
func KnownAudience(a Audience) bool {
	switch a {
	case AudienceAgent, AudienceHuman, AudienceBoth:
		return true
	default:
		return false
	}
}

// ContextDocument is durable prose guidance.
type ContextDocument struct {
	ID         string                `json:"id"`
	Title      string                `json:"title"`
	Kind       ContextKind           `json:"kind"`
	Content    string                `json:"content"`
	Audience   Audience              `json:"audience"`
	Activation Activation            `json:"activation"`
	Enabled    *bool                 `json:"enabled,omitempty"`
	Provenance provenance.Provenance `json:"provenance"`
	Extensions Extensions            `json:"extensions,omitempty"`
}

// Priority is the normative strength of a rule.
type Priority string

const (
	PriorityMust   Priority = "must"
	PriorityShould Priority = "should"
	PriorityMay    Priority = "may"
)

// KnownPriority reports whether p is recognised.
func KnownPriority(p Priority) bool {
	switch p {
	case PriorityMust, PriorityShould, PriorityMay:
		return true
	default:
		return false
	}
}

// PriorityRank orders priorities for stable output (strongest first).
func PriorityRank(p Priority) int {
	switch p {
	case PriorityMust:
		return 0
	case PriorityShould:
		return 1
	case PriorityMay:
		return 2
	default:
		return 3
	}
}

// Rule is a single actionable instruction.
//
// Only Instruction is necessarily agent-facing. Rationale and examples are
// preserved for humans and are not exported into permanent agent context
// unless a target profile explicitly asks for them.
type Rule struct {
	ID           string                `json:"id"`
	Title        string                `json:"title"`
	Instruction  string                `json:"instruction"`
	Priority     Priority              `json:"priority"`
	Enabled      bool                  `json:"enabled"`
	Activation   Activation            `json:"activation"`
	Rationale    string                `json:"rationale,omitempty"`
	GoodExamples []string              `json:"goodExamples,omitempty"`
	BadExamples  []string              `json:"badExamples,omitempty"`
	Provenance   provenance.Provenance `json:"provenance"`
	Extensions   Extensions            `json:"extensions,omitempty"`
}

// Procedure is an ordered, invocable workflow.
type Procedure struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Trigger     string                `json:"trigger,omitempty"`
	Content     string                `json:"content"`
	Enabled     *bool                 `json:"enabled,omitempty"`
	Provenance  provenance.Provenance `json:"provenance"`
	Extensions  Extensions            `json:"extensions,omitempty"`
}

// Skill is reusable on-demand capability documentation.
type Skill struct {
	ID               string                `json:"id"`
	Name             string                `json:"name"`
	Description      string                `json:"description"`
	Content          string                `json:"content"`
	AllowedTools     []string              `json:"allowedTools,omitempty"`
	InvocationPolicy string                `json:"invocationPolicy,omitempty"`
	Enabled          *bool                 `json:"enabled,omitempty"`
	Provenance       provenance.Provenance `json:"provenance"`
	Extensions       Extensions            `json:"extensions,omitempty"`
}

// Agent is a specialist agent definition.
//
// ModelPreference is opaque provider metadata. Stemma never translates model
// names between providers.
type Agent struct {
	ID              string                `json:"id"`
	Name            string                `json:"name"`
	Description     string                `json:"description"`
	Instructions    string                `json:"instructions"`
	Tools           []string              `json:"tools,omitempty"`
	ModelPreference string                `json:"modelPreference,omitempty"`
	Enabled         *bool                 `json:"enabled,omitempty"`
	Provenance      provenance.Provenance `json:"provenance"`
	Extensions      Extensions            `json:"extensions,omitempty"`
}

// DecisionStatus is the lifecycle state of an architecture decision.
type DecisionStatus string

const (
	DecisionProposed   DecisionStatus = "proposed"
	DecisionAccepted   DecisionStatus = "accepted"
	DecisionDeprecated DecisionStatus = "deprecated"
	DecisionSuperseded DecisionStatus = "superseded"
)

// KnownDecisionStatus reports whether s is recognised.
func KnownDecisionStatus(s DecisionStatus) bool {
	switch s {
	case DecisionProposed, DecisionAccepted, DecisionDeprecated, DecisionSuperseded:
		return true
	default:
		return false
	}
}

// Decision is an architecture decision record.
//
// Only AgentConstraints is normally projected into agent-facing context; the
// remaining fields are human documentation.
type Decision struct {
	ID               string                `json:"id"`
	Title            string                `json:"title"`
	Status           DecisionStatus        `json:"status"`
	Context          string                `json:"context,omitempty"`
	Decision         string                `json:"decision,omitempty"`
	Consequences     string                `json:"consequences,omitempty"`
	AgentConstraints []string              `json:"agentConstraints,omitempty"`
	Provenance       provenance.Provenance `json:"provenance"`
	Extensions       Extensions            `json:"extensions,omitempty"`
}

// OpaqueBlock is source content Stemma deliberately did not interpret.
type OpaqueBlock struct {
	ID string `json:"id"`
	// Provider is the format the block belongs to.
	Provider string `json:"provider"`
	// SourcePath is the repository-relative path it came from.
	SourcePath string `json:"sourcePath"`
	// Content is the lossless text of the block.
	Content string `json:"content"`
	// Span locates the block inside the source file.
	Span provenance.Span `json:"span,omitzero"`
	// Reason explains why it was not interpreted.
	Reason string `json:"reason"`
	// Hash is the digest of Content.
	Hash string `json:"hash"`
	// ReemitForRoundTrip marks blocks that must be written back when the same
	// format is regenerated.
	ReemitForRoundTrip bool `json:"reemitForRoundTrip"`
}

// TokenBudgets are optional advisory limits. Zero means "no budget".
type TokenBudgets struct {
	AlwaysOn         int `json:"alwaysOn,omitempty"`
	WorstCaseRequest int `json:"worstCaseRequest,omitempty"`
}

// Project is the canonical, provider-neutral source of truth.
type Project struct {
	SchemaVersion     int            `json:"schemaVersion"`
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Description       string         `json:"description,omitempty"`
	Language          string         `json:"language,omitempty"`
	Framework         string         `json:"framework,omitempty"`
	ArchitectureStyle string         `json:"architectureStyle,omitempty"`
	Targets           []TargetFormat `json:"targets"`
	TokenBudgets      TokenBudgets   `json:"tokenBudgets,omitzero"`

	ContextDocuments []ContextDocument `json:"contextDocuments"`
	Rules            []Rule            `json:"rules"`
	Procedures       []Procedure       `json:"procedures"`
	Skills           []Skill           `json:"skills"`
	Agents           []Agent           `json:"agents"`
	Decisions        []Decision        `json:"decisions"`

	Extensions   Extensions    `json:"extensions,omitempty"`
	OpaqueBlocks []OpaqueBlock `json:"opaqueBlocks"`
}

// NewProject returns an empty but valid project.
func NewProject(id, name string) Project {
	return Project{
		SchemaVersion:    schemaVersion,
		ID:               id,
		Name:             name,
		Targets:          []TargetFormat{},
		ContextDocuments: []ContextDocument{},
		Rules:            []Rule{},
		Procedures:       []Procedure{},
		Skills:           []Skill{},
		Agents:           []Agent{},
		Decisions:        []Decision{},
		OpaqueBlocks:     []OpaqueBlock{},
	}
}

// IsEnabled reports whether an optional enabled flag allows projection.
func IsEnabled(flag *bool) bool { return flag == nil || *flag }

// EntityRef is a lightweight handle to any canonical entity.
type EntityRef struct {
	ID   string
	Type EntityType
}

// Entities returns every entity reference in deterministic order.
func (p Project) Entities() []EntityRef {
	out := make([]EntityRef, 0,
		len(p.ContextDocuments)+len(p.Rules)+len(p.Procedures)+
			len(p.Skills)+len(p.Agents)+len(p.Decisions))
	for _, e := range p.ContextDocuments {
		out = append(out, EntityRef{e.ID, EntityContext})
	}
	for _, e := range p.Rules {
		out = append(out, EntityRef{e.ID, EntityRule})
	}
	for _, e := range p.Procedures {
		out = append(out, EntityRef{e.ID, EntityProcedure})
	}
	for _, e := range p.Skills {
		out = append(out, EntityRef{e.ID, EntitySkill})
	}
	for _, e := range p.Agents {
		out = append(out, EntityRef{e.ID, EntityAgent})
	}
	for _, e := range p.Decisions {
		out = append(out, EntityRef{e.ID, EntityDecision})
	}
	return out
}

// HasTarget reports whether the target is enabled for the project.
func (p Project) HasTarget(t TargetFormat) bool {
	for _, x := range p.Targets {
		if x == t {
			return true
		}
	}
	return false
}
