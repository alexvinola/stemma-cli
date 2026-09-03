package optimizer

import (
	"testing"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/tokenestimate"
)

func TestDeduplicateExactContext(t *testing.T) {
	p := canonical.NewProject("prj", "x")
	doc := canonical.ContextDocument{
		Title: "Testing", Kind: canonical.KindTesting, Content: "Run the tests.",
		Audience: canonical.AudienceAgent, Activation: canonical.Always(),
	}
	a, b := doc, doc
	a.ID, b.ID = "context.b-copy", "context.a-original"
	p.ContextDocuments = []canonical.ContextDocument{a, b}

	res := Run(p, DefaultOptions())
	if len(res.Project.ContextDocuments) != 1 {
		t.Fatalf("documents = %d", len(res.Project.ContextDocuments))
	}
	// The lexicographically smallest id is kept, so the result is stable.
	if res.Project.ContextDocuments[0].ID != "context.a-original" {
		t.Errorf("kept %s", res.Project.ContextDocuments[0].ID)
	}
	if len(res.Dropped) != 1 || res.Dropped[0].ID != "context.b-copy" {
		t.Errorf("dropped = %+v", res.Dropped)
	}
	if len(res.Diagnostics) == 0 || res.Diagnostics[0].Severity != diagnostics.SeverityInfo {
		t.Errorf("expected an informational diagnostic: %+v", res.Diagnostics)
	}
}

func TestDeduplicateRespectsActivation(t *testing.T) {
	p := canonical.NewProject("prj", "x")
	base := canonical.Rule{
		Title: "X", Instruction: "do x", Priority: canonical.PriorityMust, Enabled: true,
	}
	a, b := base, base
	a.ID, b.ID = "rule.a", "rule.b"
	a.Activation = canonical.Always()
	b.Activation = canonical.PathScoped([]string{"src/**"}, nil)
	p.Rules = []canonical.Rule{a, b}

	res := Run(p, DefaultOptions())
	if len(res.Project.Rules) != 2 {
		t.Fatal("rules with different activations must not be merged")
	}
}

func TestNormalizedDeduplication(t *testing.T) {
	p := canonical.NewProject("prj", "x")
	a := canonical.Rule{ID: "rule.a", Title: "A", Instruction: "do   x\n\n\n", Priority: canonical.PriorityMust,
		Enabled: true, Activation: canonical.Always()}
	b := canonical.Rule{ID: "rule.b", Title: "B", Instruction: "do x", Priority: canonical.PriorityMust,
		Enabled: true, Activation: canonical.Always()}
	p.Rules = []canonical.Rule{a, b}

	if got := Run(p, Options{DeduplicateExact: true}); len(got.Project.Rules) != 2 {
		t.Error("whitespace-only differences must survive exact deduplication")
	}
	if got := Run(p, DefaultOptions()); len(got.Project.Rules) != 1 {
		t.Error("normalized deduplication should collapse whitespace-only differences")
	}
}

func TestOptimizerIsOrderIndependent(t *testing.T) {
	p := canonical.NewProject("prj", "x")
	mk := func(id, text string) canonical.Rule {
		return canonical.Rule{ID: id, Title: id, Instruction: text, Priority: canonical.PriorityShould,
			Enabled: true, Activation: canonical.Always()}
	}
	p.Rules = []canonical.Rule{mk("rule.c", "same"), mk("rule.a", "same"), mk("rule.b", "other")}
	first := Run(p, DefaultOptions())
	p.Rules = []canonical.Rule{mk("rule.b", "other"), mk("rule.c", "same"), mk("rule.a", "same")}
	second := Run(p, DefaultOptions())
	if len(first.Project.Rules) != len(second.Project.Rules) {
		t.Fatal("optimization depends on input order")
	}
	for i := range first.Project.Rules {
		if first.Project.Rules[i].ID != second.Project.Rules[i].ID {
			t.Fatalf("optimization depends on input order: %s vs %s",
				first.Project.Rules[i].ID, second.Project.Rules[i].ID)
		}
	}
}

func TestNormalizeWhitespace(t *testing.T) {
	got := NormalizeWhitespace("  a   b  \n\n\n  c  \n\n")
	if got != "a b\n\nc" {
		t.Errorf("NormalizeWhitespace = %q", got)
	}
}

func TestBudgetDiagnostics(t *testing.T) {
	report := tokenestimate.Report{TargetAlwaysOn: 100, LargestScope: 50, WorstCaseRequest: 150}
	if diags := BudgetDiagnostics(canonical.TokenBudgets{}, report, "claude"); len(diags) != 0 {
		t.Errorf("no budget should mean no diagnostics for a small project: %+v", diags)
	}
	diags := BudgetDiagnostics(canonical.TokenBudgets{AlwaysOn: 50}, report, "claude")
	if len(diags) != 1 || diags[0].Code != diagnostics.TokenBudgetExceeded {
		t.Fatalf("diags = %+v", diags)
	}
	diags = BudgetDiagnostics(canonical.TokenBudgets{WorstCaseRequest: 100}, report, "claude")
	if len(diags) != 1 || diags[0].Code != diagnostics.TokenBudgetExceeded {
		t.Fatalf("diags = %+v", diags)
	}
	big := tokenestimate.Report{TargetAlwaysOn: 20000}
	diags = BudgetDiagnostics(canonical.TokenBudgets{}, big, "claude")
	if len(diags) != 1 || diags[0].Code != diagnostics.AlwaysOnContextLarge {
		t.Fatalf("large always-on context should be reported: %+v", diags)
	}
}
