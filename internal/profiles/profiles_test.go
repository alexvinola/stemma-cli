package profiles

import (
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
)

func sampleProject() canonical.Project {
	p := canonical.NewProject("prj", "Test")
	p.Rules = []canonical.Rule{{
		ID: "rule.x", Title: "X", Instruction: "do x", Priority: canonical.PriorityMust,
		Enabled: true, Activation: canonical.Always(),
	}}
	return p
}

func TestMarshalRoundTrip(t *testing.T) {
	p := Default(canonical.TargetCodex)
	include := false
	activation := canonical.PathScoped([]string{"src/api/**"}, nil)
	p.Overrides["rule.x"] = Override{Include: &include, Activation: &activation, Directory: "src/api"}
	p.AcceptedDiagnostics = []string{"dg_b", "dg_a"}

	data, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(string(data), "dg_a") > strings.Index(string(data), "dg_b") {
		t.Error("accepted diagnostics must be sorted")
	}
	back, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Marshal(back)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(again) {
		t.Fatal("profile serialization is not stable")
	}
}

func TestUnmarshalRejectsBadDocuments(t *testing.T) {
	for _, in := range []string{
		`{"schemaVersion":99,"target":"claude"}`,
		`{"schemaVersion":1,"target":"claude","unknown":1}`,
		`{`,
	} {
		if _, err := Unmarshal([]byte(in)); err == nil {
			t.Errorf("Unmarshal(%q) should fail", in)
		}
	}
}

func TestValidate(t *testing.T) {
	project := sampleProject()
	p := Default(canonical.TargetClaude)
	bad := canonical.Activation{Type: canonical.ActivationPathScoped, Include: []string{"../escape"}}
	p.Overrides["rule.x"] = Override{Activation: &bad}
	p.Overrides["rule.missing"] = Override{Directory: "docs"}
	p.Overrides["rule.y"] = Override{Directory: "../outside"}
	p.AcceptedDiagnostics = []string{"not-a-fingerprint"}

	diags := Validate(p, project, "profile.json")
	var codes []diagnostics.Code
	for _, d := range diags {
		codes = append(codes, d.Code)
	}
	want := map[diagnostics.Code]bool{
		diagnostics.InvalidGlob:      false,
		diagnostics.ProfileUnknownID: false,
		diagnostics.ProfileInvalid:   false,
	}
	for _, c := range codes {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for code, found := range want {
		if !found {
			t.Errorf("expected diagnostic %s; got %v", code, codes)
		}
	}
}

func TestValidateAcceptsAGoodProfile(t *testing.T) {
	p := Default(canonical.TargetClaude)
	include := true
	p.Overrides["rule.x"] = Override{Include: &include, Directory: "docs", Filename: "x.md"}
	p.AcceptedDiagnostics = []string{"dg_0123456789abcdef"}
	if diags := Validate(p, sampleProject(), "profile.json"); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
}

func TestHashChangesWithContent(t *testing.T) {
	a, err := Hash(Default(canonical.TargetClaude))
	if err != nil {
		t.Fatal(err)
	}
	p := Default(canonical.TargetClaude)
	p.AcceptedDiagnostics = []string{"dg_x"}
	b, err := Hash(p)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("profile hashes must change with content")
	}
}
