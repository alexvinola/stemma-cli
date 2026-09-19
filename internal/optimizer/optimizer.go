// Package optimizer implements the safe, deterministic optimization passes.
//
// Every pass here must be explainable without a language model. Passes never
// paraphrase, summarise or merge content on the basis of assumed meaning: they
// only remove provably identical duplicates and report structural facts.
package optimizer

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/tokenestimate"
)

// Dropped records an entity removed by an optimization pass.
type Dropped struct {
	// ID is the removed entity.
	ID string
	// Type is its entity type.
	Type canonical.EntityType
	// Reason explains the removal in user-facing terms.
	Reason string
	// KeptID is the entity that survived.
	KeptID string
}

// Result is the outcome of running the optimization passes.
type Result struct {
	Project     canonical.Project
	Dropped     []Dropped
	Diagnostics []diagnostics.Diagnostic
}

// Options selects which passes run.
type Options struct {
	// DeduplicateExact removes byte-identical entities.
	DeduplicateExact bool
	// DeduplicateNormalized also removes entities that are identical after
	// conservative whitespace normalization.
	DeduplicateNormalized bool
	// PreserveEntityIDs excludes projection-sensitive entities from
	// deduplication. The compiler populates this for profile overrides, whose
	// target-specific effects are resolved by adapters after optimization.
	PreserveEntityIDs map[string]struct{}
}

// DefaultOptions enables the passes that are always safe.
func DefaultOptions() Options {
	return Options{DeduplicateExact: true, DeduplicateNormalized: true}
}

// Run applies the enabled passes and returns a new project.
func Run(p canonical.Project, opts Options) Result {
	var bag diagnostics.Bag
	out := p
	var dropped []Dropped

	if opts.DeduplicateExact || opts.DeduplicateNormalized {
		out, dropped = deduplicate(out, opts, &bag)
	}
	sort.Slice(dropped, func(i, j int) bool { return dropped[i].ID < dropped[j].ID })
	return Result{Project: out, Dropped: dropped, Diagnostics: bag.Items()}
}

// deduplicate considers only active, agent-facing entities without extensions,
// profile-sensitive IDs or on-demand delivery (whose name can depend on the
// entity's identity). It compares content, activation and canonical metadata;
// uncertain candidates remain for the exporter to resolve independently.
// The lexicographically smallest eligible ID is kept, independent of input order.
func deduplicate(p canonical.Project, opts Options, bag *diagnostics.Bag) (canonical.Project, []Dropped) {
	var dropped []Dropped

	docs := append([]canonical.ContextDocument{}, p.ContextDocuments...)
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	seen := map[string]string{}
	keptDocs := docs[:0:0]
	for _, d := range docs {
		if !documentCandidate(d, opts.PreserveEntityIDs) {
			keptDocs = append(keptDocs, d)
			continue
		}
		key := documentDedupeKey(d, opts.DeduplicateNormalized)
		if prev, ok := seen[key]; ok {
			dropped = append(dropped, Dropped{
				ID: d.ID, Type: canonical.EntityContext, KeptID: prev,
				Reason: fmt.Sprintf("identical content and activation to %s", prev),
			})
			bag.Add(duplicateDiag(d.ID, prev, d.Provenance.SourcePath, opts.DeduplicateNormalized))
			continue
		}
		seen[key] = d.ID
		keptDocs = append(keptDocs, d)
	}
	p.ContextDocuments = keptDocs

	rules := append([]canonical.Rule{}, p.Rules...)
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	seenRules := map[string]string{}
	keptRules := rules[:0:0]
	for _, r := range rules {
		if !ruleCandidate(r, opts.PreserveEntityIDs) {
			keptRules = append(keptRules, r)
			continue
		}
		key := ruleDedupeKey(r, opts.DeduplicateNormalized)
		if prev, ok := seenRules[key]; ok {
			dropped = append(dropped, Dropped{
				ID: r.ID, Type: canonical.EntityRule, KeptID: prev,
				Reason: fmt.Sprintf("identical instruction, priority and activation to %s", prev),
			})
			bag.Add(duplicateDiag(r.ID, prev, r.Provenance.SourcePath, opts.DeduplicateNormalized))
			continue
		}
		seenRules[key] = r.ID
		keptRules = append(keptRules, r)
	}
	p.Rules = keptRules

	return p, dropped
}

func documentCandidate(d canonical.ContextDocument, preserve map[string]struct{}) bool {
	if _, ok := preserve[d.ID]; ok {
		return false
	}
	return canonical.IsEnabled(d.Enabled) && d.Audience != canonical.AudienceHuman &&
		d.Activation.AgentFacing() && d.Activation.Type != canonical.ActivationOnDemand &&
		len(d.Extensions) == 0
}

func ruleCandidate(r canonical.Rule, preserve map[string]struct{}) bool {
	if _, ok := preserve[r.ID]; ok {
		return false
	}
	return r.Enabled && r.Activation.AgentFacing() &&
		r.Activation.Type != canonical.ActivationOnDemand && len(r.Extensions) == 0
}

func duplicateDiag(id, kept, path string, normalized bool) diagnostics.Diagnostic {
	how := "byte-identical"
	if normalized {
		how = "identical after whitespace normalization"
	}
	return diagnostics.New(diagnostics.DuplicateEntityID, diagnostics.SeverityInfo,
		fmt.Sprintf("%s duplicates %s and is not projected", id, kept)).
		WithEntity(id).WithPath(path).
		WithDetail("The two entities are %s and have the same activation.", how).
		WithSuggestion("Remove one of them from .stemma/project.json to silence this notice.").
		WithBlocking(false)
}

func documentDedupeKey(d canonical.ContextDocument, normalize bool) string {
	return dedupeKey(d.Content, d.Activation, normalize, string(d.Kind), string(d.Audience))
}

func ruleDedupeKey(r canonical.Rule, normalize bool) string {
	return dedupeKey(r.Instruction, r.Activation, normalize, string(r.Priority))
}

func dedupeKey(content string, a canonical.Activation, normalize bool, metadata ...string) string {
	if normalize {
		content = NormalizeWhitespace(content)
	}
	var key []byte
	appendPart := func(value string) {
		key = strconv.AppendInt(key, int64(len(value)), 10)
		key = append(key, ':')
		key = append(key, value...)
	}
	for _, value := range metadata {
		appendPart(value)
	}
	appendPart(string(a.Type))
	for _, patterns := range [][]string{sortedCopy(a.Include), sortedCopy(a.Exclude)} {
		appendPart(strconv.Itoa(len(patterns)))
		for _, pattern := range patterns {
			appendPart(pattern)
		}
	}
	appendPart(a.Trigger)
	appendPart(a.InvocationName)
	appendPart(content)
	return string(key)
}

func sortedCopy(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

// NormalizeWhitespace collapses runs of spaces and trims each line. It is the
// only text transformation the optimizer is allowed to apply, and it is used
// solely for comparison, never for rewriting stored content.
func NormalizeWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		l = strings.Join(strings.Fields(l), " ")
		if l == "" && len(out) > 0 && out[len(out)-1] == "" {
			continue
		}
		out = append(out, l)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// BudgetDiagnostics reports token budget problems for a compiled target.
func BudgetDiagnostics(
	budgets canonical.TokenBudgets, report tokenestimate.Report, target string,
) []diagnostics.Diagnostic {
	var bag diagnostics.Bag
	if budgets.AlwaysOn > 0 && report.TargetAlwaysOn > budgets.AlwaysOn {
		bag.Add(diagnostics.New(diagnostics.TokenBudgetExceeded, diagnostics.SeverityWarning,
			fmt.Sprintf("always-on context is about %d tokens, over the budget of %d",
				report.TargetAlwaysOn, budgets.AlwaysOn)).
			WithTarget(target).
			WithDetail("Estimates are approximate: %s. No provider tokenizer was used.", report.Method).
			WithSuggestion("Move content to path-scoped or on-demand delivery in the target profile."))
	}
	if budgets.WorstCaseRequest > 0 && report.WorstCaseRequest > budgets.WorstCaseRequest {
		bag.Add(diagnostics.New(diagnostics.TokenBudgetExceeded, diagnostics.SeverityWarning,
			fmt.Sprintf("worst-case request is about %d tokens, over the budget of %d",
				report.WorstCaseRequest, budgets.WorstCaseRequest)).
			WithTarget(target).
			WithDetail("Worst case is always-on (%d) plus the largest single scope (%d, %s).",
				report.TargetAlwaysOn, report.LargestScope, report.LargestScopeName).
			WithSuggestion("Split the largest scope, or reduce always-on content."))
	}
	// An advisory threshold for always-on context, independent of any budget.
	const largeAlwaysOn = 8000
	if budgets.AlwaysOn == 0 && report.TargetAlwaysOn > largeAlwaysOn {
		bag.Add(diagnostics.New(diagnostics.AlwaysOnContextLarge, diagnostics.SeverityInfo,
			fmt.Sprintf("always-on context is about %d tokens", report.TargetAlwaysOn)).
			WithTarget(target).
			WithDetail("Every request carries this content. The estimate is approximate.").
			WithSuggestion("Consider path-scoped delivery for parts of it, or set tokenBudgets.alwaysOn."))
	}
	return bag.Items()
}
