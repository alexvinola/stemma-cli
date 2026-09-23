package diagnostics

import (
	"sort"
	"testing"
)

func TestFingerprintIsStableAcrossMessageChanges(t *testing.T) {
	a := New(ExcludeNotRepresent, SeverityWarning, "first wording").
		WithEntity("rule.x").WithTarget("claude").WithPath("a.md")
	b := New(ExcludeNotRepresent, SeverityWarning, "a much better wording").
		WithEntity("rule.x").WithTarget("claude").WithPath("a.md").
		WithDetail("extra detail").WithSuggestion("do this")
	if a.Fingerprint != b.Fingerprint {
		t.Fatal("improving prose must not change a diagnostic fingerprint")
	}
	c := a.WithEntity("rule.y")
	if c.Fingerprint == a.Fingerprint {
		t.Fatal("a different entity must produce a different fingerprint")
	}
}

func TestFieldIsPartOfTheFingerprintOnlyWhenSet(t *testing.T) {
	// Pinned from testdata/canonical/exclude-and-disabled/expected-codex-diagnostics.json
	// (and docs/diagnostics.md): adding the field component must not change an
	// existing fingerprint, or acceptances recorded in profiles would stop matching.
	legacy := New(ExcludeNotRepresent, SeverityWarning, "x").
		WithPath("src/api/AGENTS.md").WithEntity("rule.controller-repository").WithTarget("codex")
	if legacy.Fingerprint != "dg_184cf4cbcf883cfa" {
		t.Fatalf("fingerprint without a field changed: %s", legacy.Fingerprint)
	}
	a := New(ExtensionNotProjected, SeverityWarning, "x").
		WithEntity("agent.reviewer").WithTarget("claude").WithField("extensions.kiro.resources")
	b := a.WithField("extensions.kiro.toolAliases")
	if a.Fingerprint == b.Fingerprint {
		t.Fatal("different fields must produce different fingerprints")
	}
	if a.Fingerprint == a.WithField("").Fingerprint {
		t.Fatal("a field must contribute to the fingerprint")
	}
	if a.WithDetail("better wording").Fingerprint != a.Fingerprint {
		t.Fatal("prose must not change a fingerprint that carries a field")
	}
	items := []Diagnostic{b, a}
	Sort(items)
	if items[0].Field != "extensions.kiro.resources" {
		t.Fatalf("field must order otherwise equal diagnostics: %+v", items)
	}
	var bag Bag
	bag.Add(a)
	bag.Add(New(ExtensionNotProjected, SeverityWarning, "x").
		WithEntity("agent.reviewer").WithTarget("claude").WithField("extensions.kiro.toolAliases"))
	if got := len(bag.Items()); got != 2 {
		t.Fatalf("diagnostics for different fields must not be deduplicated, got %d", got)
	}
}

func TestSeverityDrivesBlocking(t *testing.T) {
	if !New(InvalidGlob, SeverityError, "x").Blocking {
		t.Error("errors must block by default")
	}
	if New(InvalidGlob, SeverityWarning, "x").Blocking {
		t.Error("warnings must not block by default")
	}
	if !New(InvalidGlob, SeverityWarning, "x").WithBlocking(true).Blocking {
		t.Error("explicit blocking must be honoured")
	}
}

func TestSortIsDeterministic(t *testing.T) {
	items := []Diagnostic{
		New(InvalidGlob, SeverityInfo, "i").WithPath("b.md"),
		New(PathEscape, SeverityError, "e").WithPath("z.md"),
		New(InvalidGlob, SeverityWarning, "w").WithPath("a.md"),
		New(InvalidGlob, SeverityError, "e2").WithPath("a.md"),
	}
	Sort(items)
	if items[0].Severity != SeverityError || items[0].Code != InvalidGlob {
		t.Fatalf("order = %+v", items)
	}
	if items[1].Code != PathEscape {
		t.Fatalf("errors must be ordered by code: %+v", items)
	}
	if items[2].Severity != SeverityWarning || items[3].Severity != SeverityInfo {
		t.Fatalf("severity order is wrong: %+v", items)
	}
}

func TestBagDeduplicates(t *testing.T) {
	var bag Bag
	d := New(InvalidGlob, SeverityWarning, "same").WithPath("a.md")
	bag.Add(d)
	bag.Add(d)
	bag.Add(New(InvalidGlob, SeverityWarning, "other").WithPath("a.md"))
	if got := len(bag.Items()); got != 2 {
		t.Fatalf("items = %d, want 2", got)
	}
}

func TestBagItemsOnEmptyBagIsNotNil(t *testing.T) {
	var bag Bag
	if bag.Items() == nil {
		t.Fatal("Items must return an empty slice, never nil, so JSON output stays stable")
	}
}

func TestAcceptDowngrades(t *testing.T) {
	d := New(ExcludeNotRepresent, SeverityError, "x").WithEntity("rule.x")
	out := Accept([]Diagnostic{d}, []string{d.Fingerprint})
	if out[0].Severity != SeverityInfo || out[0].Blocking {
		t.Fatalf("accepted diagnostic = %+v", out[0])
	}
	untouched := Accept([]Diagnostic{d}, []string{"dg_other"})
	if untouched[0].Severity != SeverityError {
		t.Fatal("an unrelated fingerprint must not change anything")
	}
}

func TestCodesAreUnique(t *testing.T) {
	codes := []Code{
		UnrecognizedFormat, FileLimitReached, FileUnreadable, InvalidEncoding,
		InvalidFrontMatter, FrontMatterTooLarge, UnsafeYAMLConstruct, UnknownSectionKept,
		UnknownKeysKept, OpaqueBlockKept, MultipleSources, NoSourcesDetected, MixedLineEndings,
		InvalidAgentJSON, DuplicateJSONKey, DuplicateEntityID, InvalidEntityID, MissingRequired,
		UnknownSchema, InvalidActivation, InvalidGlob, GlobExpansionLimit, DanglingProvenance, ProfileInvalid,
		ProfileUnknownID, ManifestInvalid, TargetUnavailable, TargetNotEnabled, ExcludeNotRepresent,
		PatternNotRepresent,
		DirectoryScopeAmbig, DirectoryScopeBroader, AgentToolsNeedReview, AgentNotNative,
		OnDemandAdapted, OpaqueNotReemitted, TargetOverridesContent,
		RegeneratedFile, ExtensionNotProjected, SecurityExtensionNotProjected, PathEscape, SymlinkRejected, StalePlan, WriteRolledBack,
		RecoveryDataWritten, UntrackedDestConfl, ImportRoundTripUnverified, ImportOwnershipRevoked, DeleteProposed, OutputStale,
		TokenBudgetExceeded, AlwaysOnContextLarge, InternalInvariant,
	}
	seen := map[Code]struct{}{}
	var names []string
	for _, c := range codes {
		if _, dup := seen[c]; dup {
			t.Errorf("duplicate diagnostic code %s", c)
		}
		seen[c] = struct{}{}
		names = append(names, string(c))
	}
	sort.Strings(names)
	for _, n := range names {
		if len(n) < 12 || n[:6] != "STEMMA" {
			t.Errorf("diagnostic code %q does not follow the STEMMA<number>_<NAME> convention", n)
		}
	}
}
