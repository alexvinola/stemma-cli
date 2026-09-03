// Package optimizer implements the safe, deterministic optimization passes.
//
// Every pass here must be explainable without a language model. Passes never
// paraphrase, summarise or merge content on the basis of assumed meaning: they
// only remove provably identical duplicates and report structural facts.
package optimizer

import (
	"fmt"
	"sort"
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
		out, dropped = deduplicate(out, opts.DeduplicateNormalized, &bag)
	}
	sort.Slice(dropped, func(i, j int) bool { return dropped[i].ID < dropped[j].ID })
	return Result{Project: out, Dropped: dropped, Diagnostics: bag.Items()}
}

// deduplicate removes entities whose agent-facing content and activation are
// identical to an earlier entity. The entity with the lexicographically
// smallest ID is kept, so the result does not depend on input order.
func deduplicate(p canonical.Project, normalize bool, bag *diagnostics.Bag) (canonical.Project, []Dropped) {
	var dropped []Dropped

	docs := append([]canonical.ContextDocument{}, p.ContextDocuments...)
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	seen := map[string]string{}
	keptDocs := docs[:0:0]
	for _, d := range docs {
		key := dedupeKey(d.Content, d.Activation, normalize)
		if prev, ok := seen[key]; ok {
			dropped = append(dropped, Dropped{
				ID: d.ID, Type: canonical.EntityContext, KeptID: prev,
				Reason: fmt.Sprintf("identical content and activation to %s", prev),
			})
			bag.Add(duplicateDiag(d.ID, prev, d.Provenance.SourcePath, normalize))
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
		key := dedupeKey(string(r.Priority)+"\x00"+r.Instruction, r.Activation, normalize)
		if prev, ok := seenRules[key]; ok {
			dropped = append(dropped, Dropped{
				ID: r.ID, Type: canonical.EntityRule, KeptID: prev,
				Reason: fmt.Sprintf("identical instruction, priority and activation to %s", prev),
			})
			bag.Add(duplicateDiag(r.ID, prev, r.Provenance.SourcePath, normalize))
			continue
		}
		seenRules[key] = r.ID
		keptRules = append(keptRules, r)
	}
	p.Rules = keptRules

	return p, dropped
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

func dedupeKey(content string, a canonical.Activation, normalize bool) string {
	if normalize {
		content = NormalizeWhitespace(content)
	}
	scope := string(a.Type) + "|" +
		strings.Join(sortedCopy(a.Include), ",") + "|" +
		strings.Join(sortedCopy(a.Exclude), ",") + "|" + a.Trigger
	return scope + "\x00" + content
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
