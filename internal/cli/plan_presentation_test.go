package cli

import (
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/compiler"
)

// No filesystem calls: even platforms rejecting these filenames must render
// a supplied plan safely, including the entity details and every change kind.
func TestPrintPlanSanitizesUntrustedFields(t *testing.T) {
	for _, kind := range []compiler.ChangeKind{compiler.ChangeCreate, compiler.ChangeUpdate,
		compiler.ChangeUnchanged, compiler.ChangeConflict, compiler.ChangeDeleteProposed} {
		t.Run(string(kind), func(t *testing.T) {
			var out strings.Builder
			value := "unsafe\x1b[31m\r\nFORGED\u0085.md"
			p := compiler.Plan{Target: canonical.TargetClaude,
				Changes: []compiler.Change{{Path: value, Kind: kind, Reason: value}},
				Mappings: []compiler.ProjectionMapping{{EntityID: value, Files: []string{value},
					Outcome: adapters.OutcomeExact, Activation: canonical.OnDemand(value, value)}}}
			printPlan(Env{Stdout: &out}, p, true, true)
			if strings.ContainsAny(out.String(), "\x1b\r\u0085") || strings.Contains(out.String(), "\nFORGED") {
				t.Fatalf("unsanitized output: %q", out.String())
			}
			if !strings.Contains(out.String(), SanitizeLine(value)) {
				t.Fatalf("sanitized path missing: %q", out.String())
			}
		})
	}
}
