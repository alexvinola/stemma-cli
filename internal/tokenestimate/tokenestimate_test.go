package tokenestimate

import "testing"

func TestHeuristicIsConservative(t *testing.T) {
	e := Heuristic{}
	if got := e.Estimate(""); got != 0 {
		t.Errorf("empty = %d", got)
	}
	// "hello world" is 11 characters (ceil 11/4 = 3) and 2 words (ceil 2.6 = 3).
	if got := e.Estimate("hello world"); got != 3 {
		t.Errorf("hello world = %d, want 3", got)
	}
	// Many short words: the word-based bound dominates.
	if got := e.Estimate("a a a a a a a a a a"); got != 13 {
		t.Errorf("short words = %d, want 13", got)
	}
	// One long token: the character bound dominates.
	if got := e.Estimate("aaaaaaaaaaaaaaaaaaaa"); got != 5 {
		t.Errorf("long token = %d, want 5", got)
	}
}

func TestEstimateIsDeterministic(t *testing.T) {
	e := Default()
	text := "Validate every request body at the boundary.\nReturn problem+json errors."
	first := e.Estimate(text)
	for i := 0; i < 100; i++ {
		if e.Estimate(text) != first {
			t.Fatal("estimation is not deterministic")
		}
	}
}

func TestReportBuild(t *testing.T) {
	b := NewBuilder(nil)
	b.AddSourceAlwaysOn("aaaa aaaa aaaa aaaa aaaa aaaa aaaa aaaa")
	b.AddAlwaysOn("aaaa aaaa")
	b.AddScoped("src/api/**", "rule.a", "aaaa aaaa aaaa")
	b.AddScoped("src/api/**", "rule.b", "aaaa")
	b.AddScoped("docs/**", "context.c", "aaaa")
	b.AddOnDemand("aaaa")
	b.AddDocumentationOnly("aaaa")
	r := b.Build()

	if !r.Approximate {
		t.Error("reports must always be marked approximate")
	}
	if r.LargestScopeName != "src/api/**" {
		t.Errorf("largest scope = %q", r.LargestScopeName)
	}
	if r.WorstCaseRequest != r.TargetAlwaysOn+r.LargestScope {
		t.Errorf("worst case = %d, want %d", r.WorstCaseRequest, r.TargetAlwaysOn+r.LargestScope)
	}
	if r.ReductionPercent == nil {
		t.Fatal("expected a reduction percentage when a source baseline exists")
	}
	if len(r.Scopes) != 2 {
		t.Fatalf("scopes = %+v", r.Scopes)
	}
	if r.Scopes[0].Tokens < r.Scopes[1].Tokens {
		t.Error("scopes must be sorted by cost, descending")
	}
	for i := 1; i < len(r.Scopes[0].EntityIDs); i++ {
		if r.Scopes[0].EntityIDs[i-1] > r.Scopes[0].EntityIDs[i] {
			t.Error("entity ids inside a scope must be sorted")
		}
	}
}

func TestNoReductionWithoutBaseline(t *testing.T) {
	b := NewBuilder(nil)
	b.AddAlwaysOn("aaaa")
	if r := b.Build(); r.ReductionPercent != nil {
		t.Error("a reduction must not be reported without a source baseline")
	}
}

func TestScopeNameIsOrderIndependent(t *testing.T) {
	if ScopeName([]string{"b/**", "a/**"}) != ScopeName([]string{"a/**", "b/**"}) {
		t.Fatal("scope names must not depend on pattern order")
	}
}

type doubleEstimator struct{}

func (doubleEstimator) Name() string          { return "double" }
func (doubleEstimator) Estimate(s string) int { return len(s) * 2 }

func TestEstimatorIsReplaceable(t *testing.T) {
	b := NewBuilder(doubleEstimator{})
	b.AddAlwaysOn("abc")
	r := b.Build()
	if r.TargetAlwaysOn != 6 || r.Method != "double" {
		t.Fatalf("custom estimator not used: %+v", r)
	}
}
