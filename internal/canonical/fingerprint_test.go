package canonical

import (
	"testing"

	"github.com/alexvinola/stemma-cli/internal/provenance"
)

func fingerprintProject() Project {
	p := NewProject("prj_fingerprints", "Fingerprints")
	source := provenance.Provenance{
		SourceFormat: "claude", SourcePath: "CLAUDE.md", SourceHash: "sha256:source",
		ImporterVersion: "1", Disposition: provenance.DispositionParsed,
	}
	p.ContextDocuments = []ContextDocument{{
		ID: "context.guide", Title: "Guide", Kind: KindConventions, Content: "Use the guide.",
		Audience: AudienceAgent, Activation: Always(), Provenance: source,
		Extensions: Extensions{"claude": {"custom": "context"}},
	}}
	p.Rules = []Rule{{
		ID: "rule.validate", Title: "Validate", Instruction: "Validate input.",
		Priority: PriorityMust, Enabled: true, Activation: PathScoped([]string{"src/**"}, nil),
		Provenance: source, Extensions: Extensions{"claude": {"custom": "rule"}},
	}}
	p.Procedures = []Procedure{{
		ID: "procedure.release", Name: "release", Description: "Cut a release", Content: "Tag it.",
		Provenance: source, Extensions: Extensions{"claude": {"custom": "procedure"}},
	}}
	p.Skills = []Skill{{
		ID: "skill.audit", Name: "audit", Description: "Audit changes", Content: "Review the diff.",
		Provenance: source, Extensions: Extensions{"claude": {"custom": "skill"}},
	}}
	p.Agents = []Agent{{
		ID: "agent.reviewer", Name: "reviewer", Description: "Reviews changes", Instructions: "Review them.",
		Provenance: source, Extensions: Extensions{"claude": {"custom": "agent"}},
	}}
	p.Decisions = []Decision{{
		ID: "decision.storage", Title: "Storage", Status: DecisionAccepted, Decision: "Use files.",
		Provenance: source, Extensions: Extensions{"claude": {"custom": "decision"}},
	}}
	return p
}

func projectFingerprints(t *testing.T, p Project) map[string]string {
	t.Helper()
	wantCount := len(p.Entities())
	got := make(map[string]string, wantCount)
	for _, entity := range p.Entities() {
		fingerprint, ok := EntityFingerprint(p, entity.ID)
		if !ok {
			t.Fatalf("fingerprint %s", entity.ID)
		}
		got[entity.ID] = fingerprint
	}
	if len(got) != wantCount {
		t.Fatalf("fingerprinted %d entities, want %d", len(got), wantCount)
	}
	return got
}

func TestEntityFingerprintSurvivesCanonicalJSONRoundTrip(t *testing.T) {
	original := fingerprintProject()
	want := projectFingerprints(t, original)
	data, err := MarshalProject(original)
	if err != nil {
		t.Fatal(err)
	}
	roundTripped, err := UnmarshalProject(data)
	if err != nil {
		t.Fatal(err)
	}
	got := projectFingerprints(t, roundTripped)
	for _, entity := range original.Entities() {
		if got[entity.ID] != want[entity.ID] {
			t.Errorf("%s fingerprint changed across canonical JSON: got %s, want %s",
				entity.ID, got[entity.ID], want[entity.ID])
		}
	}
}

func TestEntityFingerprintIncludesExtensionsAndIgnoresProvenance(t *testing.T) {
	original := fingerprintProject()
	want := projectFingerprints(t, original)
	changed := provenance.Provenance{
		SourceFormat: "kiro", SourcePath: ".kiro/changed.md", SourceHash: "sha256:changed",
		ImporterVersion: "different", Disposition: provenance.DispositionAdapted,
	}
	for i := range original.ContextDocuments {
		original.ContextDocuments[i].Provenance = changed
	}
	for i := range original.Rules {
		original.Rules[i].Provenance = changed
	}
	for i := range original.Procedures {
		original.Procedures[i].Provenance = changed
	}
	for i := range original.Skills {
		original.Skills[i].Provenance = changed
	}
	for i := range original.Agents {
		original.Agents[i].Provenance = changed
	}
	for i := range original.Decisions {
		original.Decisions[i].Provenance = changed
	}
	got := projectFingerprints(t, original)
	for _, entity := range original.Entities() {
		if got[entity.ID] != want[entity.ID] {
			t.Errorf("%s fingerprint changed with provenance: got %s, want %s",
				entity.ID, got[entity.ID], want[entity.ID])
		}
	}

	cases := []struct {
		id     string
		mutate func(*Project)
	}{
		{"context.guide", func(p *Project) { p.ContextDocuments[0].Extensions.Set("claude", "custom", "changed") }},
		{"rule.validate", func(p *Project) { p.Rules[0].Extensions.Set("claude", "custom", "changed") }},
		{"procedure.release", func(p *Project) { p.Procedures[0].Extensions.Set("claude", "custom", "changed") }},
		{"skill.audit", func(p *Project) { p.Skills[0].Extensions.Set("claude", "custom", "changed") }},
		{"agent.reviewer", func(p *Project) { p.Agents[0].Extensions.Set("claude", "custom", "changed") }},
		{"decision.storage", func(p *Project) { p.Decisions[0].Extensions.Set("claude", "custom", "changed") }},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			project := fingerprintProject()
			before, _ := EntityFingerprint(project, tc.id)
			tc.mutate(&project)
			after, _ := EntityFingerprint(project, tc.id)
			if after == before {
				t.Errorf("fingerprint did not include %s extensions", tc.id)
			}
		})
	}
}
