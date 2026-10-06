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

func TestDeduplicationPreservesWhitespaceDifferences(t *testing.T) {
	for _, tc := range []struct{ name, first, second string }{
		{"fenced literal", "```text\na  b\n```", "```text\na b\n```"},
		{"indented code", "    a  b", "    a b"},
		{"inline literal", "Use `a  b`.", "Use `a b`."},
		{"quoted prose", "Preserve \"a  b\".", "Preserve \"a b\"."},
		{"markdown hard break", "First  \nSecond", "First\nSecond"},
		{"blank lines", "do x\n\n\n", "do x\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := canonical.NewProject("prj", "x")
			for i, content := range []string{tc.first, tc.second} {
				id := []string{"a", "b"}[i]
				p.Rules = append(p.Rules, canonical.Rule{ID: "rule." + id,
					Instruction: content, Priority: canonical.PriorityMust, Enabled: true,
					Activation: canonical.Always()})
				p.ContextDocuments = append(p.ContextDocuments, canonical.ContextDocument{
					ID: "context." + id, Content: content, Kind: canonical.KindOther,
					Audience: canonical.AudienceAgent, Activation: canonical.Always()})
			}
			got := Run(p, DefaultOptions())
			if len(got.Project.Rules) != 2 || len(got.Project.ContextDocuments) != 2 ||
				len(got.Dropped) != 0 || len(got.Diagnostics) != 0 {
				t.Fatalf("distinct text was deduplicated: %+v", got)
			}
		})
	}
}

func TestDeduplicateRequiresProjectionEquivalence(t *testing.T) {
	falseValue := false
	baseDoc := canonical.ContextDocument{
		Title: "Guide", Kind: canonical.KindConventions, Content: "Keep this guidance.",
		Audience: canonical.AudienceAgent, Activation: canonical.Always(),
	}
	baseRule := canonical.Rule{
		Title: "Rule", Instruction: "Keep this instruction.", Priority: canonical.PriorityMust,
		Enabled: true, Activation: canonical.Always(),
	}

	tests := []struct {
		name     string
		project  func() canonical.Project
		options  Options
		wantDocs int
		wantRule int
	}{
		{
			name: "document nil enablement equals true",
			project: func() canonical.Project {
				p := canonical.NewProject("prj", "x")
				a, b := baseDoc, baseDoc
				a.ID, b.ID = "context.a", "context.b"
				enabled := true
				b.Enabled = &enabled
				p.ContextDocuments = []canonical.ContextDocument{a, b}
				return p
			},
			options: DefaultOptions(), wantDocs: 1,
		},
		{
			name: "document disabled differs from default",
			project: func() canonical.Project {
				p := canonical.NewProject("prj", "x")
				a, b := baseDoc, baseDoc
				a.ID, b.ID = "context.a", "context.b"
				a.Enabled = &falseValue
				p.ContextDocuments = []canonical.ContextDocument{a, b}
				return p
			},
			options: DefaultOptions(), wantDocs: 2,
		},
		{
			name: "document audience differs",
			project: func() canonical.Project {
				p := canonical.NewProject("prj", "x")
				a, b := baseDoc, baseDoc
				a.ID, b.ID = "context.a", "context.b"
				a.Audience = canonical.AudienceHuman
				p.ContextDocuments = []canonical.ContextDocument{a, b}
				return p
			},
			options: DefaultOptions(), wantDocs: 2,
		},
		{
			name: "document extension is uncertain",
			project: func() canonical.Project {
				p := canonical.NewProject("prj", "x")
				a, b := baseDoc, baseDoc
				a.ID, b.ID = "context.a", "context.b"
				a.Extensions = canonical.Extensions{"future": {"mode": "one"}}
				b.Extensions = canonical.Extensions{"future": {"mode": "two"}}
				p.ContextDocuments = []canonical.ContextDocument{a, b}
				return p
			},
			options: DefaultOptions(), wantDocs: 2,
		},
		{
			name: "rule enabled differs",
			project: func() canonical.Project {
				p := canonical.NewProject("prj", "x")
				a, b := baseRule, baseRule
				a.ID, b.ID = "rule.a", "rule.b"
				a.Enabled = false
				p.Rules = []canonical.Rule{a, b}
				return p
			},
			options: DefaultOptions(), wantRule: 2,
		},
		{
			name: "rule extension is uncertain",
			project: func() canonical.Project {
				p := canonical.NewProject("prj", "x")
				a, b := baseRule, baseRule
				a.ID, b.ID = "rule.a", "rule.b"
				a.Extensions = canonical.Extensions{"future": {"mode": "one"}}
				b.Extensions = canonical.Extensions{"future": {"mode": "two"}}
				p.Rules = []canonical.Rule{a, b}
				return p
			},
			options: DefaultOptions(), wantRule: 2,
		},
		{
			name: "profile-sensitive rule is preserved",
			project: func() canonical.Project {
				p := canonical.NewProject("prj", "x")
				a, b := baseRule, baseRule
				a.ID, b.ID = "rule.a", "rule.b"
				p.Rules = []canonical.Rule{a, b}
				return p
			},
			options: Options{
				DeduplicateExact:  true,
				PreserveEntityIDs: map[string]struct{}{"rule.a": {}},
			},
			wantRule: 2,
		},
		{
			name: "on-demand rules are preserved",
			project: func() canonical.Project {
				p := canonical.NewProject("prj", "x")
				a, b := baseRule, baseRule
				a.ID, b.ID = "rule.a", "rule.b"
				a.Activation = canonical.OnDemand("review", "")
				b.Activation = canonical.OnDemand("review", "")
				p.Rules = []canonical.Rule{a, b}
				return p
			},
			options: DefaultOptions(), wantRule: 2,
		},
		{
			name: "different invocation names are preserved",
			project: func() canonical.Project {
				p := canonical.NewProject("prj", "x")
				a, b := baseRule, baseRule
				a.ID, b.ID = "rule.a", "rule.b"
				a.Activation = canonical.OnDemand("review", "first")
				b.Activation = canonical.OnDemand("review", "second")
				p.Rules = []canonical.Rule{a, b}
				return p
			},
			options: DefaultOptions(), wantRule: 2,
		},
		{
			name: "scope fields cannot collide through delimiters",
			project: func() canonical.Project {
				p := canonical.NewProject("prj", "x")
				a, b := baseRule, baseRule
				a.ID, b.ID = "rule.a", "rule.b"
				a.Activation = canonical.PathScoped([]string{"a,b"}, nil)
				b.Activation = canonical.PathScoped([]string{"a", "b"}, nil)
				p.Rules = []canonical.Rule{a, b}
				return p
			},
			options: DefaultOptions(), wantRule: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project := tt.project()
			before, err := canonical.MarshalProject(project)
			if err != nil {
				t.Fatal(err)
			}
			preservedBefore := copyIDSet(tt.options.PreserveEntityIDs)
			result := Run(project, tt.options)
			if got := len(result.Project.ContextDocuments); got != tt.wantDocs {
				t.Errorf("documents = %d, want %d", got, tt.wantDocs)
			}
			if got := len(result.Project.Rules); got != tt.wantRule {
				t.Errorf("rules = %d, want %d", got, tt.wantRule)
			}
			after, err := canonical.MarshalProject(project)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("optimizer mutated its canonical input")
			}
			if !equalIDSets(tt.options.PreserveEntityIDs, preservedBefore) {
				t.Fatal("optimizer mutated PreserveEntityIDs")
			}
		})
	}
}

func copyIDSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for id := range in {
		out[id] = struct{}{}
	}
	return out
}

func equalIDSets(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if _, ok := b[id]; !ok {
			return false
		}
	}
	return true
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
