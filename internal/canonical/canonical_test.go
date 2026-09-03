package canonical

import (
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/provenance"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"API layer conventions": "api-layer-conventions",
		"  Testing!  ":          "testing",
		"C++ / Rust":            "c-rust",
		"already-slugged":       "already-slugged",
		"":                      "",
		"日本語":                   "",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugOrHashIsDeterministic(t *testing.T) {
	a := SlugOrHash("日本語", "path/x.md")
	b := SlugOrHash("日本語", "path/x.md")
	if a != b {
		t.Fatalf("SlugOrHash is not deterministic: %q vs %q", a, b)
	}
	if !ValidIDSlug(a) {
		t.Fatalf("SlugOrHash produced an invalid slug: %q", a)
	}
	if c := SlugOrHash("日本語", "other.md"); c == a {
		t.Error("different fallbacks should produce different slugs")
	}
}

func TestAllocatorResolvesCollisions(t *testing.T) {
	a := NewAllocator()
	first := a.Allocate(EntityRule, "testing", "a.md")
	second := a.Allocate(EntityRule, "testing", "b.md")
	third := a.Allocate(EntityRule, "testing", "c.md")
	if first != "rule.testing" || second != "rule.testing-2" || third != "rule.testing-3" {
		t.Fatalf("ids = %q, %q, %q", first, second, third)
	}
}

func TestParseID(t *testing.T) {
	kind, slug, err := ParseID("rule.api-conventions")
	if err != nil || kind != EntityRule || slug != "api-conventions" {
		t.Fatalf("ParseID = %v %q %v", kind, slug, err)
	}
	for _, bad := range []string{"", "rule", ".x", "rule.", "unknown.x"} {
		if _, _, err := ParseID(bad); err == nil {
			t.Errorf("ParseID(%q) should fail", bad)
		}
	}
}

func TestActivationValidation(t *testing.T) {
	if err := Always().Validate(); err != nil {
		t.Errorf("always: %v", err)
	}
	if err := PathScoped([]string{"src/**"}, nil).Validate(); err != nil {
		t.Errorf("path-scoped: %v", err)
	}
	if err := (Activation{Type: ActivationPathScoped}).Validate(); err == nil {
		t.Error("path-scoped with no include must be invalid")
	}
	if err := (Activation{Type: ActivationAlways, Include: []string{"x"}}).Validate(); err == nil {
		t.Error("always with include patterns must be invalid")
	}
	if err := (Activation{Type: "made-up"}).Validate(); err == nil {
		t.Error("unknown activation type must be invalid")
	}
	if err := (Activation{}).Validate(); err == nil {
		t.Error("the zero activation must be invalid")
	}
	if err := PathScoped([]string{"../escape"}, nil).Validate(); err == nil {
		t.Error("an escaping glob must be invalid")
	}
}

func TestActivationRejectsUnknownTagInJSON(t *testing.T) {
	var a Activation
	if err := a.UnmarshalJSON([]byte(`{"type":"whatever"}`)); err == nil {
		t.Fatal("decoding an unknown activation type must fail")
	}
}

func sampleProject() Project {
	p := NewProject("prj_test", "Test")
	p.Targets = []TargetFormat{TargetClaude}
	p.ContextDocuments = []ContextDocument{{
		ID: "context.architecture", Title: "Architecture", Kind: KindArchitecture,
		Content: "Layers.", Audience: AudienceAgent, Activation: Always(),
		Provenance: provenance.Provenance{
			SourceFormat: "claude", SourcePath: "CLAUDE.md", SourceHash: "sha256:x",
			ImporterVersion: "1", Disposition: provenance.DispositionParsed,
		},
	}}
	p.Rules = []Rule{{
		ID: "rule.validate-input", Title: "Validate input", Instruction: "Validate every request.",
		Priority: PriorityMust, Enabled: true, Activation: PathScoped([]string{"src/api/**"}, nil),
	}}
	return p
}

func TestValidateAcceptsAGoodProject(t *testing.T) {
	if diags := Validate(sampleProject()); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
}

func TestValidateRejectsDuplicateIDs(t *testing.T) {
	p := sampleProject()
	p.Rules = append(p.Rules, p.Rules[0])
	found := false
	for _, d := range Validate(p) {
		if d.Code == diagnostics.DuplicateEntityID {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a duplicate id diagnostic")
	}
}

func TestValidateRejectsUnsafeToolNames(t *testing.T) {
	p := sampleProject()
	p.Agents = []Agent{{
		ID: "agent.bad", Name: "bad", Instructions: "do things",
		Tools: []string{"../../bin/sh"},
	}}
	found := false
	for _, d := range Validate(p) {
		if d.Code == diagnostics.AgentToolsNeedReview && d.Severity == diagnostics.SeverityError {
			found = true
		}
	}
	if !found {
		t.Fatal("expected an unsafe tool name diagnostic")
	}
}

func TestValidateChecksOpaqueBlockHashes(t *testing.T) {
	p := sampleProject()
	p.OpaqueBlocks = []OpaqueBlock{{
		ID: "opaque.x", Provider: "claude", SourcePath: "CLAUDE.md",
		Content: "text", Reason: "unknown", Hash: "sha256:wrong",
	}}
	found := false
	for _, d := range Validate(p) {
		if d.Code == diagnostics.ManifestInvalid {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a hash mismatch diagnostic for the opaque block")
	}
}

func TestMarshalIsDeterministicAndSorted(t *testing.T) {
	p := sampleProject()
	p.ContextDocuments = append(p.ContextDocuments, ContextDocument{
		ID: "context.aaa", Title: "A", Kind: KindOther, Content: "x",
		Audience: AudienceAgent, Activation: Always(),
	})
	first, err := MarshalProject(p)
	if err != nil {
		t.Fatal(err)
	}
	// Reverse the input order; output must not change.
	p.ContextDocuments[0], p.ContextDocuments[1] = p.ContextDocuments[1], p.ContextDocuments[0]
	second, err := MarshalProject(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("marshalling depends on input order")
	}
	if !strings.Contains(string(first), `"schemaVersion": 2`) {
		t.Error("schema version missing from output")
	}
}

func TestRoundTripJSON(t *testing.T) {
	p := sampleProject()
	data, err := MarshalProject(p)
	if err != nil {
		t.Fatal(err)
	}
	back, err := UnmarshalProject(data)
	if err != nil {
		t.Fatalf("UnmarshalProject: %v", err)
	}
	again, err := MarshalProject(back)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(again) {
		t.Fatal("canonical JSON is not stable across a decode/encode round trip")
	}
}

func TestUnmarshalRejectsUnknownFieldsAndVersions(t *testing.T) {
	if _, err := UnmarshalProject([]byte(`{"schemaVersion":2,"nope":true}`)); err == nil {
		t.Error("unknown fields must be rejected")
	}
	if _, err := UnmarshalProject([]byte(`{"schemaVersion":99}`)); err == nil {
		t.Error("unsupported schema versions must be rejected")
	}
	if _, err := UnmarshalProject([]byte(`{"schemaVersion":2} {"schemaVersion":2}`)); err == nil {
		t.Error("trailing content must be rejected")
	}
}

func TestExtensionsAreSortedInJSON(t *testing.T) {
	p := sampleProject()
	p.ContextDocuments[0].Extensions.Set("claude", "zeta", "1")
	p.ContextDocuments[0].Extensions.Set("claude", "alpha", "2")
	data, err := MarshalProject(p)
	if err != nil {
		t.Fatal(err)
	}
	alpha := strings.Index(string(data), `"alpha"`)
	zeta := strings.Index(string(data), `"zeta"`)
	if alpha < 0 || zeta < 0 || alpha > zeta {
		t.Fatal("extension keys are not sorted")
	}
}

func FuzzUnmarshalProject(f *testing.F) {
	p := sampleProject()
	data, _ := MarshalProject(p)
	f.Add(string(data))
	f.Add(`{"schemaVersion":2}`)
	f.Add(`{`)
	f.Fuzz(func(t *testing.T, s string) {
		project, err := UnmarshalProject([]byte(s))
		if err != nil {
			return
		}
		// Anything that decodes must survive validation without panicking and
		// must re-encode deterministically.
		_ = Validate(project)
		a, err := MarshalProject(project)
		if err != nil {
			t.Fatalf("re-encoding a decoded project failed: %v", err)
		}
		b, err := MarshalProject(project)
		if err != nil || string(a) != string(b) {
			t.Fatal("re-encoding is not deterministic")
		}
	})
}
