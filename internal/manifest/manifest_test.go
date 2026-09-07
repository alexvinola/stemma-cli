package manifest

import (
	"reflect"
	"strings"
	"testing"
)

func TestRecordImportReplacesOnlyTheImportedSourcesEvidence(t *testing.T) {
	m := New()
	m.LastTarget = "codex"
	m.Targets["codex"] = TargetRecord{GeneratedFiles: []GeneratedRecord{{Path: "AGENTS.md", Hash: "other"}}}
	m.Targets["claude"] = TargetRecord{GeneratedFiles: []GeneratedRecord{
		{Path: "CLAUDE.md", Hash: "old"},
		{Path: ".claude/rules/unverified.md", Hash: "old"},
		{Path: ".claude/rules/previous.md", Hash: "keep"},
	}}
	sources := []SourceRecord{
		{Path: "CLAUDE.md", Hash: "new", Format: "claude"},
		{Path: ".claude/rules/unverified.md", Hash: "changed", Format: "claude"},
	}
	verified := TargetRecord{ProjectHash: "project", ProfileHash: "profile",
		GeneratedFiles: []GeneratedRecord{{Path: "CLAUDE.md", Hash: "new"}}}
	m.RecordImport("claude", sources, verified)
	for _, tc := range []struct {
		target, path, hash string
		tracked            bool
	}{
		{"claude", "CLAUDE.md", "new", true},
		{"claude", ".claude/rules/unverified.md", "", false},
		{"claude", ".claude/rules/previous.md", "keep", true},
		{"codex", "AGENTS.md", "other", true},
	} {
		if hash, ok := m.Tracked(tc.target, tc.path); ok != tc.tracked || hash != tc.hash {
			t.Errorf("Tracked(%s, %s) = %q, %v", tc.target, tc.path, hash, ok)
		}
	}
	if !m.Imported("claude", sources[1].Path) || m.Imported("codex", "CLAUDE.md") {
		t.Fatal("imported source identity must be independent of ownership and per target")
	}
	if m.LastTarget != "codex" || m.Targets["claude"].AppliedAt != "" || m.ProjectHash != "project" {
		t.Fatalf("import must record the new project without fabricating an apply: %+v", m)
	}
	if !reflect.DeepEqual(m.ImportedSources, sources) {
		t.Fatal("sources were not recorded")
	}
	first, err := Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	m.RecordImport("claude", sources, verified)
	second, err := Marshal(m)
	if err != nil || string(first) != string(second) {
		t.Fatal("repeating import changed ownership or manifest bytes")
	}
}

func TestRecordImportRevokesTheLastUnverifiedFile(t *testing.T) {
	m := New()
	m.Targets["claude"] = TargetRecord{GeneratedFiles: []GeneratedRecord{{Path: "CLAUDE.md", Hash: "old"}}}
	m.RecordImport("claude", []SourceRecord{{Path: "CLAUDE.md", Hash: "new", Format: "claude"}}, TargetRecord{})
	if _, ok := m.Targets["claude"]; ok {
		t.Fatal("failed verification retained ownership")
	}
	if !m.Imported("claude", "CLAUDE.md") {
		t.Fatal("unverified source identity was lost")
	}
}

func TestMarshalIsDeterministic(t *testing.T) {
	m := New()
	m.ImportedSources = []SourceRecord{
		{Path: "z.md", Hash: "sha256:2", Format: "claude"},
		{Path: "a.md", Hash: "sha256:1", Format: "claude"},
	}
	m.Targets["claude"] = TargetRecord{
		GeneratedFiles: []GeneratedRecord{
			{Path: "z.md", Hash: "sha256:9", Entities: []string{"rule.b", "rule.a"}},
			{Path: "a.md", Hash: "sha256:8"},
		},
		AcceptedDiagnostics: []string{"dg_b", "dg_a"},
	}
	first, err := Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(string(first), `"a.md"`) > strings.Index(string(first), `"z.md"`) {
		t.Error("records must be sorted by path")
	}
	if strings.Index(string(first), "rule.a") > strings.Index(string(first), "rule.b") {
		t.Error("entities must be sorted")
	}
	second, err := Marshal(m)
	if err != nil || string(first) != string(second) {
		t.Fatal("marshalling is not deterministic")
	}
	back, err := Unmarshal(first)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Marshal(back)
	if err != nil || string(again) != string(first) {
		t.Fatal("round trip is not stable")
	}
}

func TestTrackedLookup(t *testing.T) {
	m := New()
	m.Targets["claude"] = TargetRecord{GeneratedFiles: []GeneratedRecord{{Path: "CLAUDE.md", Hash: "sha256:1"}}}
	if hash, ok := m.Tracked("claude", "CLAUDE.md"); !ok || hash != "sha256:1" {
		t.Errorf("Tracked = %q %v", hash, ok)
	}
	if _, ok := m.Tracked("claude", "other.md"); ok {
		t.Error("unknown paths must not be reported as tracked")
	}
	if _, ok := m.Tracked("codex", "CLAUDE.md"); ok {
		t.Error("tracking is per target")
	}
	if paths := m.TrackedPaths("claude"); len(paths) != 1 || paths[0] != "CLAUDE.md" {
		t.Errorf("TrackedPaths = %v", paths)
	}
}

func TestUnmarshalRejectsBadDocuments(t *testing.T) {
	for _, in := range []string{`{"schemaVersion":99}`, `{"schemaVersion":1,"nope":1}`, `{`} {
		if _, err := Unmarshal([]byte(in)); err == nil {
			t.Errorf("Unmarshal(%q) should fail", in)
		}
	}
}

func TestAppliedAtIsMetadataOnly(t *testing.T) {
	// A timestamp may be recorded, but it must never take part in the record of
	// what was generated: the generated-file hashes are what planning compares.
	a := New()
	a.Targets["claude"] = TargetRecord{
		ProjectHash:    "sha256:1",
		GeneratedFiles: []GeneratedRecord{{Path: "CLAUDE.md", Hash: "sha256:9"}},
	}
	b := New()
	b.Targets["claude"] = TargetRecord{
		ProjectHash:    "sha256:1",
		GeneratedFiles: []GeneratedRecord{{Path: "CLAUDE.md", Hash: "sha256:9"}},
		AppliedAt:      "2026-01-01T00:00:00Z",
	}
	if got, _ := b.Tracked("claude", "CLAUDE.md"); got != "sha256:9" {
		t.Fatalf("tracked hash = %q", got)
	}
	ha, _ := a.Tracked("claude", "CLAUDE.md")
	hb, _ := b.Tracked("claude", "CLAUDE.md")
	if ha != hb {
		t.Fatal("a recorded timestamp changed what the manifest says about generated files")
	}
}
