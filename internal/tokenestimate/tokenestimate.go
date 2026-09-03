// Package tokenestimate provides a local, deterministic and explicitly
// approximate token estimator.
//
// Stemma never calls a provider tokenizer and never makes network requests.
// Every number produced here is an approximation and must be labelled as such
// in user-facing output.
package tokenestimate

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Estimator converts text into an approximate token count.
type Estimator interface {
	// Name identifies the estimation method in reports.
	Name() string
	// Estimate returns an approximate token count for s.
	Estimate(s string) int
}

// Heuristic is the default estimator:
//
//	tokens = max(characters / 4, words * 1.3)
//
// rounded up. It is deliberately conservative (it prefers over-estimating) and
// is documented in docs/architecture.md.
type Heuristic struct{}

// Name implements Estimator.
func (Heuristic) Name() string { return "heuristic-v1 (max(chars/4, words*1.3), rounded up)" }

// Estimate implements Estimator.
func (Heuristic) Estimate(s string) int {
	if s == "" {
		return 0
	}
	chars := len([]rune(s))
	words := countWords(s)
	byChars := math.Ceil(float64(chars) / 4.0)
	byWords := math.Ceil(float64(words) * 1.3)
	return int(math.Max(byChars, byWords))
}

func countWords(s string) int {
	n := 0
	inWord := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			inWord = false
			continue
		}
		if !inWord {
			n++
			inWord = true
		}
	}
	return n
}

// Default returns the estimator used unless a caller supplies another.
func Default() Estimator { return Heuristic{} }

// ScopeCost is the estimated cost of one path-scoped context.
type ScopeCost struct {
	// Scope is a stable, human-readable identifier for the scope (the sorted
	// include pattern list joined with ", ").
	Scope string `json:"scope"`
	// EntityIDs lists the canonical entities contributing to the scope.
	EntityIDs []string `json:"entityIds"`
	// Tokens is the approximate cost of the whole scope.
	Tokens int `json:"tokens"`
}

// Report summarises approximate context cost. All values are approximations.
type Report struct {
	// Method documents the estimator used.
	Method string `json:"method"`
	// Approximate is always true; it exists so JSON consumers cannot mistake
	// these values for exact tokenizer output.
	Approximate bool `json:"approximate"`

	// SourceAlwaysOn is the always-on cost of the imported provider files, when
	// a comparison baseline is available (0 otherwise).
	SourceAlwaysOn int `json:"sourceAlwaysOn"`
	// TargetAlwaysOn is the always-on cost of the generated target files.
	TargetAlwaysOn int `json:"targetAlwaysOn"`
	// ScopedTotal is the sum of every path-scoped context.
	ScopedTotal int `json:"scopedTotal"`
	// Scopes lists per-scope costs in deterministic order.
	Scopes []ScopeCost `json:"scopes"`
	// LargestScope is the most expensive single scope.
	LargestScope int `json:"largestScope"`
	// LargestScopeName identifies the most expensive scope.
	LargestScopeName string `json:"largestScopeName,omitempty"`
	// WorstCaseRequest is TargetAlwaysOn + LargestScope.
	WorstCaseRequest int `json:"worstCaseRequest"`
	// OnDemand is the cost of content loaded only when invoked.
	OnDemand int `json:"onDemand"`
	// DocumentationOnly is the cost of content never sent to an agent.
	DocumentationOnly int `json:"documentationOnly"`
	// GeneratedTotal is the estimated cost of every generated file.
	GeneratedTotal int `json:"generatedTotal"`
	// ReductionPercent compares SourceAlwaysOn with TargetAlwaysOn. It is nil
	// when no source baseline is available.
	ReductionPercent *int `json:"reductionPercent,omitempty"`
}

// Builder accumulates costs while a target is compiled.
type Builder struct {
	est    Estimator
	report Report
	scopes map[string]*ScopeCost
}

// NewBuilder returns a builder using est (or the default estimator when nil).
func NewBuilder(est Estimator) *Builder {
	if est == nil {
		est = Default()
	}
	return &Builder{
		est:    est,
		report: Report{Method: est.Name(), Approximate: true},
		scopes: map[string]*ScopeCost{},
	}
}

// Estimate exposes the underlying estimator.
func (b *Builder) Estimate(s string) int { return b.est.Estimate(s) }

// AddAlwaysOn records always-on target content.
func (b *Builder) AddAlwaysOn(s string) { b.report.TargetAlwaysOn += b.est.Estimate(s) }

// AddSourceAlwaysOn records the always-on cost of the original source files.
func (b *Builder) AddSourceAlwaysOn(s string) { b.report.SourceAlwaysOn += b.est.Estimate(s) }

// AddScoped records content belonging to a named path scope.
func (b *Builder) AddScoped(scope, entityID, s string) {
	cost := b.est.Estimate(s)
	b.report.ScopedTotal += cost
	sc, ok := b.scopes[scope]
	if !ok {
		sc = &ScopeCost{Scope: scope}
		b.scopes[scope] = sc
	}
	sc.Tokens += cost
	sc.EntityIDs = append(sc.EntityIDs, entityID)
}

// AddOnDemand records on-demand content.
func (b *Builder) AddOnDemand(s string) { b.report.OnDemand += b.est.Estimate(s) }

// AddDocumentationOnly records content that never reaches an agent.
func (b *Builder) AddDocumentationOnly(s string) { b.report.DocumentationOnly += b.est.Estimate(s) }

// AddGeneratedFile records the cost of a rendered target file.
func (b *Builder) AddGeneratedFile(s string) { b.report.GeneratedTotal += b.est.Estimate(s) }

// Build finalises the report deterministically.
func (b *Builder) Build() Report {
	r := b.report
	r.Scopes = make([]ScopeCost, 0, len(b.scopes))
	for _, sc := range b.scopes {
		ids := append([]string{}, sc.EntityIDs...)
		sort.Strings(ids)
		r.Scopes = append(r.Scopes, ScopeCost{Scope: sc.Scope, EntityIDs: ids, Tokens: sc.Tokens})
	}
	sort.Slice(r.Scopes, func(i, j int) bool {
		if r.Scopes[i].Tokens != r.Scopes[j].Tokens {
			return r.Scopes[i].Tokens > r.Scopes[j].Tokens
		}
		return r.Scopes[i].Scope < r.Scopes[j].Scope
	})
	if len(r.Scopes) > 0 {
		r.LargestScope = r.Scopes[0].Tokens
		r.LargestScopeName = r.Scopes[0].Scope
	}
	r.WorstCaseRequest = r.TargetAlwaysOn + r.LargestScope
	if r.SourceAlwaysOn > 0 {
		reduction := int(math.Round(
			(float64(r.SourceAlwaysOn) - float64(r.TargetAlwaysOn)) / float64(r.SourceAlwaysOn) * 100))
		r.ReductionPercent = &reduction
	}
	return r
}

// ScopeName builds the stable scope identifier for a set of include patterns.
func ScopeName(include []string) string {
	cp := append([]string{}, include...)
	sort.Strings(cp)
	return strings.Join(cp, ", ")
}
