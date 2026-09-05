package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/version"
)

// validPlan is the smallest plan that passes every structural check, so each
// test below can change exactly one thing and know what caused the rejection.
func validPlan() compiler.Plan {
	return compiler.Plan{
		SchemaVersion:       version.PlanSchemaVersion,
		StemmaVersion:       version.Version,
		Baseline:            version.CompatibilityBaseline,
		Target:              canonical.TargetClaude,
		AcceptedDiagnostics: []string{},
	}
}

func TestVerifyPlanStructureAcceptsAPlanThisBuildProduced(t *testing.T) {
	if err := compiler.VerifyPlanStructure(validPlan()); err != nil {
		t.Fatalf("a valid plan was rejected: %v", err)
	}
}

func TestVerifyPlanStructureRejectsDrift(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*compiler.Plan)
		want string
	}{
		{
			name: "schema from a different build",
			mut:  func(p *compiler.Plan) { p.SchemaVersion = version.PlanSchemaVersion + 1 },
			want: "plan schema version",
		},
		{
			name: "built by another stemma version",
			mut:  func(p *compiler.Plan) { p.StemmaVersion = "0.0.1-other" },
			want: "built by stemma",
		},
		{
			name: "stemma version absent",
			mut:  func(p *compiler.Plan) { p.StemmaVersion = "" },
			want: "(absent)",
		},
		{
			name: "built against another provider baseline",
			mut:  func(p *compiler.Plan) { p.Baseline = "2020-01-made-up" },
			want: "provider baseline",
		},
		{
			name: "unknown target",
			mut:  func(p *compiler.Plan) { p.Target = "not-a-provider" },
			want: "unknown target",
		},
		{
			// Cursor is a declared identifier with no adapter. A saved plan is
			// the one path that could otherwise reach the writer without going
			// through target resolution.
			name: "target declared but not implemented",
			mut:  func(p *compiler.Plan) { p.Target = canonical.TargetCursor },
			want: "declared but not implemented",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := validPlan()
			tc.mut(&p)
			err := compiler.VerifyPlanStructure(p)
			if err == nil {
				t.Fatal("expected a rejection")
			}
			if !errors.Is(err, compiler.ErrPlanRejected) {
				t.Errorf("error should wrap ErrPlanRejected, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should mention %q, got %q", tc.want, err)
			}
		})
	}
}

func TestVerifyPlanStructureChecksContentAgainstItsHash(t *testing.T) {
	// The hash a plan declares must describe the content it carries. Editing
	// one without the other is the cheapest signal that a plan was rewritten.
	for _, kind := range []compiler.ChangeKind{compiler.ChangeCreate, compiler.ChangeUpdate} {
		p := validPlan()
		p.Changes = []compiler.Change{{
			Path: "CLAUDE.md", Kind: kind,
			Content: "real content\n",
			NewHash: "sha256:" + strings.Repeat("0", 64),
		}}
		err := compiler.VerifyPlanStructure(p)
		if err == nil {
			t.Fatalf("%s: a mismatched content hash was accepted", kind)
		}
		if !strings.Contains(err.Error(), "newHash") {
			t.Errorf("%s: error should name the field, got %q", kind, err)
		}
	}
}

func TestVerifyPlanMatchesNamesWhatDrifted(t *testing.T) {
	base := validPlan()
	base.ProjectHash = "sha256:aaa"
	base.ProfileHash = "sha256:bbb"
	base.Changes = []compiler.Change{{
		Path: "CLAUDE.md", Kind: compiler.ChangeCreate,
		Content: "one\n", NewHash: "sha256:one",
	}}

	for _, tc := range []struct {
		name string
		mut  func(*compiler.Plan)
		want string
	}{
		{"project", func(p *compiler.Plan) { p.ProjectHash = "sha256:zzz" }, "canonical project changed"},
		{"profile", func(p *compiler.Plan) { p.ProfileHash = "sha256:zzz" }, "target profile changed"},
		{"path", func(p *compiler.Plan) { p.Changes[0].Path = "OTHER.md" }, "writes"},
		{"kind", func(p *compiler.Plan) { p.Changes[0].Kind = compiler.ChangeConflict }, "records"},
		{"content", func(p *compiler.Plan) { p.Changes[0].Content = "two\n" }, "content planned"},
		{"count", func(p *compiler.Plan) { p.Changes = nil }, "file change"},
		{"ownership", func(p *compiler.Plan) { p.Changes[0].TrackedHash = "sha256:ccc" }, "ownership"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rebuilt := base
			rebuilt.Changes = append([]compiler.Change{}, base.Changes...)
			tc.mut(&rebuilt)

			err := compiler.VerifyPlanMatches(base, rebuilt)
			if err == nil {
				t.Fatal("expected a rejection")
			}
			if !errors.Is(err, compiler.ErrPlanRejected) {
				t.Errorf("error should wrap ErrPlanRejected, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should mention %q, got %q", tc.want, err)
			}
		})
	}

	if err := compiler.VerifyPlanMatches(base, base); err != nil {
		t.Fatalf("a plan should match itself: %v", err)
	}
}

func TestVerifyPlanMatchesAllowsDiagnosticWordingChanges(t *testing.T) {
	d := diagnostics.New(diagnostics.DirectoryScopeAmbig, diagnostics.SeverityWarning, "original wording")
	rebuilt := validPlan()
	rebuilt.Diagnostics = []diagnostics.Diagnostic{d}
	saved := rebuilt
	saved.Diagnostics = []diagnostics.Diagnostic{
		d.WithDetail("older detail").WithSuggestion("older suggestion"),
	}
	saved.Diagnostics[0].Summary = "older summary"
	if err := compiler.VerifyPlanMatches(saved, rebuilt); err != nil {
		t.Fatalf("wording alone must not invalidate a saved plan: %v", err)
	}
}
