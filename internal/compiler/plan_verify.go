package compiler

import (
	"errors"
	"fmt"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/capabilities"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/provenance"
	"github.com/alexvinola/stemma-cli/internal/version"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// ErrPlanRejected reports that a saved plan cannot be used.
//
// A plan file is input, never authority. It arrives from the filesystem, it may
// have been edited by hand, and the workflow it exists for — save a plan,
// review it, replay it in CI — is precisely the one where a pull request can
// change it. Stemma therefore never writes the bytes a saved plan carries: it
// rebuilds the plan from the canonical project and uses the saved document only
// as an assertion about what that rebuild should produce.
//
// Every rejection happens before the transaction opens, so a rejected plan
// leaves the repository untouched.
var ErrPlanRejected = errors.New("saved plan rejected")

// KnownChangeKind reports whether k is one of the five change kinds.
func KnownChangeKind(k ChangeKind) bool {
	switch k {
	case ChangeCreate, ChangeUpdate, ChangeUnchanged, ChangeDeleteProposed, ChangeConflict:
		return true
	default:
		return false
	}
}

// VerifyPlanStructure checks the invariants that hold for every plan Stemma
// produces.
//
// It never reads the workspace, so it runs before any I/O and before the plan
// is compared against a rebuild. The checks are deliberately independent of the
// comparison: a plan can be structurally impossible (a path that escapes the
// workspace, a content hash that does not match its content) in ways that
// should be named precisely rather than reported as "does not match".
func VerifyPlanStructure(p Plan) error {
	if p.SchemaVersion != version.PlanSchemaVersion {
		return fmt.Errorf("%w: plan schema version %d, this build writes and reads %d",
			ErrPlanRejected, p.SchemaVersion, version.PlanSchemaVersion)
	}
	if p.StemmaVersion != version.Version {
		return fmt.Errorf("%w: plan was built by stemma %s, this is stemma %s; re-run `stemma plan`",
			ErrPlanRejected, describePlanValue(p.StemmaVersion), version.Version)
	}
	if p.Baseline != version.CompatibilityBaseline {
		return fmt.Errorf("%w: plan was built against provider baseline %s, this build uses %s",
			ErrPlanRejected, describePlanValue(p.Baseline), version.CompatibilityBaseline)
	}
	if !canonical.KnownTarget(p.Target) {
		return fmt.Errorf("%w: unknown target %q", ErrPlanRejected, p.Target)
	}
	caps, ok := capabilities.For(p.Target)
	if !ok || !caps.Available {
		return fmt.Errorf("%w: target %q is declared but not implemented", ErrPlanRejected, p.Target)
	}

	seen := make(map[string]struct{}, len(p.Changes))
	for _, c := range p.Changes {
		normalized, err := workspace.NormalizeRel(c.Path)
		if err != nil {
			return fmt.Errorf("%w: change path %q is not a valid repository path: %w",
				ErrPlanRejected, c.Path, err)
		}
		if normalized != c.Path {
			// A path that survives normalization but changes shape ("a/./b")
			// would let two entries name one file while comparing as distinct.
			return fmt.Errorf("%w: change path %q is not in normalized form (%q)",
				ErrPlanRejected, c.Path, normalized)
		}
		if _, dup := seen[c.Path]; dup {
			return fmt.Errorf("%w: %q appears in more than one change", ErrPlanRejected, c.Path)
		}
		seen[c.Path] = struct{}{}

		if !KnownChangeKind(c.Kind) {
			return fmt.Errorf("%w: unknown change kind %q for %q", ErrPlanRejected, c.Kind, c.Path)
		}
		// Only these two kinds carry content that would be written. A hash that
		// does not describe its own content is the signature of an edited plan.
		if c.Kind == ChangeCreate || c.Kind == ChangeUpdate {
			if got := provenance.HashString(c.Content); got != c.NewHash {
				return fmt.Errorf("%w: %q declares newHash %s but its content hashes to %s",
					ErrPlanRejected, c.Path, describeHash(c.NewHash), describeHash(got))
			}
		}
	}
	return nil
}

// VerifyPlanMatches requires a saved plan to describe exactly what compiling the
// current project produces.
//
// The rebuilt plan is the one that gets applied, so this comparison is not the
// security boundary — it is what turns a silent divergence into an explicit
// refusal, and it names the first thing that drifted rather than printing a
// diff. Anything that changes the bytes written, the files touched, or whether
// applying is allowed at all is compared here.
func VerifyPlanMatches(saved, rebuilt Plan) error {
	if saved.Target != rebuilt.Target {
		return fmt.Errorf("%w: plan targets %q, rebuild targets %q",
			ErrPlanRejected, saved.Target, rebuilt.Target)
	}
	if saved.ProjectHash != rebuilt.ProjectHash {
		return fmt.Errorf("%w: the canonical project changed after the plan was saved; "+
			"re-run `stemma plan --target %s` and review the new plan", ErrPlanRejected, saved.Target)
	}
	if saved.ProfileHash != rebuilt.ProfileHash {
		return fmt.Errorf("%w: the target profile changed after the plan was saved; "+
			"re-run `stemma plan --target %s` and review the new plan", ErrPlanRejected, saved.Target)
	}
	if err := verifyStringSlice("acceptedDiagnostics", saved.AcceptedDiagnostics, rebuilt.AcceptedDiagnostics); err != nil {
		return err
	}

	if len(saved.Changes) != len(rebuilt.Changes) {
		return fmt.Errorf("%w: plan describes %d file change(s), the current project produces %d",
			ErrPlanRejected, len(saved.Changes), len(rebuilt.Changes))
	}
	// Both sides are sorted by path when built, and VerifyPlanStructure has
	// already rejected duplicate paths, so a positional comparison is exact.
	for i := range saved.Changes {
		a, b := saved.Changes[i], rebuilt.Changes[i]
		switch {
		case a.Path != b.Path:
			return fmt.Errorf("%w: plan writes %q where the current project writes %q",
				ErrPlanRejected, a.Path, b.Path)
		case a.Kind != b.Kind:
			return fmt.Errorf("%w: plan records %q as %s, the current project records it as %s",
				ErrPlanRejected, a.Path, a.Kind, b.Kind)
		case a.NewHash != b.NewHash || a.Content != b.Content:
			return fmt.Errorf("%w: the content planned for %q is not what the current project produces",
				ErrPlanRejected, a.Path)
		case a.ExistingHash != b.ExistingHash:
			return fmt.Errorf("%w: %q changed on disk after the plan was saved",
				ErrPlanRejected, a.Path)
		case a.TrackedHash != b.TrackedHash:
			return fmt.Errorf("%w: ownership of %q changed after the plan was saved",
				ErrPlanRejected, a.Path)
		case a.ReusedSource != b.ReusedSource:
			return fmt.Errorf("%w: %q would no longer be re-emitted from its original source",
				ErrPlanRejected, a.Path)
		}
	}

	// Diagnostics decide whether applying is allowed at all, so a plan that
	// carries a different set is not the plan that was reviewed. Fingerprints
	// exclude prose, so wording changes within a build cannot trip this.
	savedFP := fingerprints(saved.Diagnostics)
	rebuiltFP := fingerprints(rebuilt.Diagnostics)
	if err := verifyStringSlice("diagnostics", savedFP, rebuiltFP); err != nil {
		return err
	}
	return nil
}

func fingerprints(ds []diagnostics.Diagnostic) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Fingerprint)
	}
	return out
}

func verifyStringSlice(what string, saved, rebuilt []string) error {
	if len(saved) != len(rebuilt) {
		return fmt.Errorf("%w: plan carries %d %s, the current project produces %d",
			ErrPlanRejected, len(saved), what, len(rebuilt))
	}
	for i := range saved {
		if saved[i] != rebuilt[i] {
			return fmt.Errorf("%w: %s differ from the current project (%q vs %q)",
				ErrPlanRejected, what, saved[i], rebuilt[i])
		}
	}
	return nil
}

// describePlanValue renders a field that may legitimately be absent in a
// hand-written document, so the error says "absent" rather than showing "".
func describePlanValue(s string) string {
	if s == "" {
		return "(absent)"
	}
	return s
}
