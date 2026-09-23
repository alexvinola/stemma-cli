package adapters

import (
	"reflect"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/capabilities"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/provenance"
)

func TestUnprojectedExtensionsClassifiesAndSorts(t *testing.T) {
	ext := canonical.Extensions{
		"kiro": {
			"welcomeMessage":     "hi",
			"allowedTools":       []any{"read"},
			"resources":          []any{"file://src"},
			"stemma.sourceFile":  "reviewer.json",
			"somethingInvented":  true,
			"stemma.anythingNew": "x",
		},
		"claude": {"permissionMode": "plan"},
	}
	got := UnprojectedExtensions(ext, func(provider, key string) bool {
		return provider == "claude" && key == "permissionMode"
	}, nil)
	var keys []string
	for _, l := range got {
		keys = append(keys, l.Provider+"."+l.Key+"="+string(l.Kind))
	}
	want := []string{
		"kiro.allowedTools=security",
		"kiro.resources=context",
		"kiro.somethingInvented=behaviour",
		"kiro.welcomeMessage=presentation",
	}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("losses = %v, want %v", keys, want)
	}
	if got[2].Classified || !got[0].Classified {
		t.Fatalf("classification flags are wrong: %+v", got)
	}
	if got[0].Field() != "extensions.kiro.allowedTools" {
		t.Fatalf("field = %q", got[0].Field())
	}
	if len(UnprojectedExtensions(nil, nil, nil)) != 0 {
		t.Fatal("an entity without extensions loses nothing")
	}
}

func TestUnprojectedExtensionsClassifiesByValueAndPreservation(t *testing.T) {
	for _, tc := range []struct {
		value     any
		preserved bool
		want      string // "" means not listed
	}{
		{"always", false, "presentation"},
		{"fileMatch", false, "presentation"},
		{"manual", false, "behaviour"},
		{"auto", false, "behaviour"},
		{"manual", true, ""},
		{"sometimes", false, "behaviour unclassified"},
	} {
		ext := canonical.Extensions{"kiro": {"inclusion": tc.value}}
		got := UnprojectedExtensions(ext, nil, func(f capabilities.ExtensionField) bool {
			return tc.preserved && f.PreservedOnDemandBy != ""
		})
		desc := ""
		if len(got) == 1 {
			desc = string(got[0].Kind)
			if !got[0].Classified {
				desc += " unclassified"
			}
			if got[0].Value != tc.value {
				t.Errorf("%v: loss does not carry its value: %+v", tc.value, got[0])
			}
		}
		if desc != tc.want {
			t.Errorf("inclusion=%v preserved=%v: got %q, want %q", tc.value, tc.preserved, desc, tc.want)
		}
	}
}

func TestExtensionLossOutcome(t *testing.T) {
	presentation := ExtensionLoss{Provider: "kiro", Key: "welcomeMessage", Kind: capabilities.ExtensionPresentation, Classified: true}
	context := ExtensionLoss{Provider: "kiro", Key: "resources", Kind: capabilities.ExtensionContext, Classified: true}
	behaviour := ExtensionLoss{Provider: "kiro", Key: "toolAliases", Kind: capabilities.ExtensionBehaviour, Classified: true}
	unknown := ExtensionLoss{Provider: "kiro", Key: "invented", Kind: capabilities.UnclassifiedExtensionKind}
	security := ExtensionLoss{Provider: "kiro", Key: "allowedTools", Kind: capabilities.ExtensionSecurity, Classified: true}
	cases := []struct {
		in     Outcome
		losses []ExtensionLoss
		want   Outcome
	}{
		{OutcomeExact, nil, OutcomeExact},
		{OutcomeExact, []ExtensionLoss{presentation}, OutcomeExact},
		{OutcomeAdapted, []ExtensionLoss{presentation}, OutcomeAdapted},
		{OutcomeExact, []ExtensionLoss{context}, OutcomeLossy},
		{OutcomeAdapted, []ExtensionLoss{behaviour}, OutcomeLossy},
		{OutcomeAdapted, []ExtensionLoss{unknown}, OutcomeLossy},
		{OutcomeExact, []ExtensionLoss{presentation, security}, OutcomeLossy},
		{OutcomeLossy, []ExtensionLoss{security}, OutcomeLossy},
		{OutcomeBlocked, []ExtensionLoss{security}, OutcomeBlocked},
		{OutcomeSkipped, []ExtensionLoss{security}, OutcomeSkipped},
	}
	for _, tc := range cases {
		if got := ExtensionLossOutcome(tc.in, tc.losses); got != tc.want {
			t.Errorf("ExtensionLossOutcome(%s, %+v) = %s, want %s", tc.in, tc.losses, got, tc.want)
		}
	}
}

func TestExtensionLossDiagnostic(t *testing.T) {
	src := provenance.Provenance{SourcePath: ".kiro/agents/reviewer.json"}
	cases := []struct {
		loss     ExtensionLoss
		report   bool
		code     diagnostics.Code
		severity diagnostics.Severity
		blocking bool
	}{
		{ExtensionLoss{Provider: "kiro", Key: "welcomeMessage", Kind: capabilities.ExtensionPresentation, Classified: true}, false, "", "", false},
		{ExtensionLoss{Provider: "kiro", Key: "resources", Kind: capabilities.ExtensionContext, Classified: true},
			true, diagnostics.ExtensionNotProjected, diagnostics.SeverityWarning, false},
		{ExtensionLoss{Provider: "kiro", Key: "toolAliases", Kind: capabilities.ExtensionBehaviour, Classified: true},
			true, diagnostics.ExtensionNotProjected, diagnostics.SeverityWarning, false},
		{ExtensionLoss{Provider: "kiro", Key: "invented", Kind: capabilities.UnclassifiedExtensionKind},
			true, diagnostics.ExtensionNotProjected, diagnostics.SeverityWarning, false},
		{ExtensionLoss{Provider: "kiro", Key: "allowedTools", Kind: capabilities.ExtensionSecurity, Classified: true},
			true, diagnostics.SecurityExtensionNotProjected, diagnostics.SeverityError, true},
	}
	for _, tc := range cases {
		d, report := ExtensionLossDiagnostic("agent.reviewer", canonical.TargetClaude, src, tc.loss)
		if report != tc.report {
			t.Errorf("%s: report = %v", tc.loss.Key, report)
			continue
		}
		if !report {
			continue
		}
		if d.Code != tc.code || d.Severity != tc.severity || d.Blocking != tc.blocking {
			t.Errorf("%s: diagnostic = %s/%s blocking=%v", tc.loss.Key, d.Code, d.Severity, d.Blocking)
		}
		if d.EntityID != "agent.reviewer" || d.Target != "claude" || d.Field != "extensions.kiro."+tc.loss.Key {
			t.Errorf("%s: diagnostic is not anchored to entity, target and field: %+v", tc.loss.Key, d)
		}
		text := d.Summary + " " + d.Detail
		for _, want := range []string{"kiro", tc.loss.Key, "claude", "agent.reviewer", src.SourcePath} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: diagnostic text does not name %q: %s", tc.loss.Key, want, text)
			}
		}
	}
}

func extensionProject() canonical.Project {
	p := canonical.NewProject("prj", "Test")
	agent := canonical.Agent{ID: "agent.reviewer", Name: "reviewer", Instructions: "Review."}
	agent.Extensions.Set("kiro", "allowedTools", []any{"read"})
	agent.Extensions.Set("kiro", "resources", []any{"file://src"})
	agent.Extensions.Set("kiro", "welcomeMessage", "hi")
	agent.Extensions.Set("kiro", "stemma.sourceFile", "reviewer.json")
	p.Agents = append(p.Agents, agent)
	return p
}

func TestBuilderReportsEachUnwrittenExtensionOnce(t *testing.T) {
	b := NewBuilder(canonical.TargetClaude, ExportInput{Project: extensionProject()})
	b.Emit(".claude/agents/reviewer.md", "body\n", []string{"agent.reviewer"})
	b.Adapted("agent.reviewer", canonical.EntityAgent, Resolution{}, provenance.Provenance{},
		[]string{".claude/agents/reviewer.md"}, "Re-rendered.")
	out := b.Result()
	m := out.Mappings[0]
	if m.Outcome != OutcomeLossy || len(m.Diagnostics) != 2 {
		t.Fatalf("mapping = %+v", m)
	}
	want := "Re-rendered. Provider-specific fields not written for claude: kiro.allowedTools (security), kiro.resources (context)."
	if m.Explanation != want {
		t.Fatalf("explanation = %q", m.Explanation)
	}
	var fields []string
	for _, d := range out.Diagnostics {
		fields = append(fields, d.Field+"/"+string(d.Severity))
	}
	if !reflect.DeepEqual(fields, []string{"extensions.kiro.allowedTools/error", "extensions.kiro.resources/warning"}) {
		t.Fatalf("diagnostics = %v", fields)
	}
}

func TestBuilderCountsSameProviderExtensionsOnlyWhenWritten(t *testing.T) {
	// Written through the builder: nothing is lost.
	b := NewBuilder(canonical.TargetKiro, ExportInput{Project: extensionProject()})
	entries := b.ExtensionEntries("agent.reviewer", extensionProject().Agents[0].Extensions)
	if len(entries) != 3 {
		t.Fatalf("entries = %+v", entries)
	}
	b.Emit(".kiro/agents/reviewer.json", "{}\n", []string{"agent.reviewer"})
	b.Exact("agent.reviewer", canonical.EntityAgent, Resolution{}, provenance.Provenance{},
		[]string{".kiro/agents/reviewer.json"}, "Native.")
	if out := b.Result(); out.Mappings[0].Outcome != OutcomeExact || len(out.Diagnostics) != 0 {
		t.Fatalf("same-provider extensions that were written must not be reported: %+v", out)
	}

	// Same provider, but the exporter forgot a key: that loss is visible too.
	b = NewBuilder(canonical.TargetKiro, ExportInput{Project: extensionProject()})
	b.MarkExtensionProjected("agent.reviewer", "resources")
	b.Emit(".kiro/agents/reviewer.json", "{}\n", []string{"agent.reviewer"})
	b.Exact("agent.reviewer", canonical.EntityAgent, Resolution{}, provenance.Provenance{},
		[]string{".kiro/agents/reviewer.json"}, "Native.")
	out := b.Result()
	if out.Mappings[0].Outcome != OutcomeLossy || len(out.Diagnostics) != 1 ||
		out.Diagnostics[0].Field != "extensions.kiro.allowedTools" {
		t.Fatalf("an unwritten same-provider key must be reported: %+v", out)
	}

	// Re-emitted source bytes carry every key of the target's own namespace.
	b = NewBuilder(canonical.TargetKiro, ExportInput{Project: extensionProject()})
	b.EmitReused(".kiro/agents/reviewer.json", []byte("{}\n"), []string{"agent.reviewer"})
	b.Exact("agent.reviewer", canonical.EntityAgent, Resolution{}, provenance.Provenance{},
		[]string{".kiro/agents/reviewer.json"}, "Native.")
	if out := b.Result(); out.Mappings[0].Outcome != OutcomeExact || len(out.Diagnostics) != 0 {
		t.Fatalf("reused source bytes lose nothing: %+v", out)
	}
}

func TestBuilderExtensionLossRespectsSkipsAndAcceptance(t *testing.T) {
	b := NewBuilder(canonical.TargetClaude, ExportInput{Project: extensionProject()})
	b.Skip("agent.reviewer", canonical.EntityAgent, Resolution{SkippedReason: "excluded"}, provenance.Provenance{})
	if out := b.Result(); out.Mappings[0].Outcome != OutcomeSkipped || len(out.Diagnostics) != 0 {
		t.Fatalf("a skipped entity projects nothing, so it loses no field: %+v", out)
	}

	accept := Resolution{AcceptLossy: true, Applied: &AppliedOverride{AcceptLossy: true}}
	for _, initial := range []Outcome{OutcomeAdapted, OutcomeLossy} {
		b = NewBuilder(canonical.TargetClaude, ExportInput{Project: extensionProject()})
		b.Emit(".claude/agents/reviewer.md", "body\n", []string{"agent.reviewer"})
		b.RecordWithDiagnostics("agent.reviewer", canonical.EntityAgent, initial, accept, provenance.Provenance{},
			[]string{".claude/agents/reviewer.md"}, "Base.", []string{"dg_existing"})
		m := b.Result().Mappings[0]
		want := "Base. Provider-specific fields not written for claude: kiro.allowedTools (security), " +
			"kiro.resources (context). The lossy mapping is explicitly accepted in the target profile."
		if m.Outcome != OutcomeLossy || m.Explanation != want {
			t.Fatalf("%s: mapping = %s %q", initial, m.Outcome, m.Explanation)
		}
	}
}
