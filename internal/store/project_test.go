package store

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/provenance"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

func richProject() canonical.Project {
	enabled := false
	p := canonical.NewProject("prj_test", "Test project")
	p.Description = "A project exercising every entity type."
	p.Targets = []canonical.TargetFormat{canonical.TargetClaude, canonical.TargetCodex}
	p.TokenBudgets = canonical.TokenBudgets{AlwaysOn: 4000}

	doc := canonical.ContextDocument{
		ID: "context.architecture", Title: "Architecture", Kind: canonical.KindArchitecture,
		Content:  "Handlers call services.\n\nServices call repositories.",
		Audience: canonical.AudienceAgent, Activation: canonical.Always(),
		Provenance: provenance.Provenance{
			SourceFormat: "claude", SourcePath: "CLAUDE.md", SourceHash: "sha256:aaa",
			ImporterVersion: "1", Disposition: provenance.DispositionParsed,
		},
	}
	doc.Extensions.Set("claude", "custom", "value")
	scoped := canonical.ContextDocument{
		ID: "context.api", Title: "API", Kind: canonical.KindOther,
		Content: "Validate input.", Audience: canonical.AudienceHuman,
		Activation: canonical.PathScoped([]string{"src/api/**", "src/lib/**"}, []string{"**/*_test.go"}),
		Enabled:    &enabled,
	}
	onDemand := canonical.ContextDocument{
		ID: "context.runbook", Title: "Runbook", Kind: canonical.KindOperations,
		Content: "Page the on-call engineer.", Audience: canonical.AudienceBoth,
		Activation: canonical.OnDemand("a production incident", "incident-runbook"),
	}
	p.ContextDocuments = []canonical.ContextDocument{doc, scoped, onDemand}

	p.Rules = []canonical.Rule{{
		ID: "rule.validate", Title: "Validate at the boundary",
		Instruction: "Validate every request body at the boundary.",
		Priority:    canonical.PriorityMust, Enabled: true,
		Activation:   canonical.PathScoped([]string{"src/api/**"}, nil),
		Rationale:    "Keeps validation in one place.\n\nAnd makes it testable.",
		GoodExamples: []string{"handler -> service", "service -> repository"},
		BadExamples:  []string{"handler -> db.Query"},
	}}
	p.Procedures = []canonical.Procedure{{
		ID: "procedure.release", Name: "release", Description: "Cut a release",
		Trigger: "a version is ready", Content: "1. Tag\n2. Push",
	}}
	p.Skills = []canonical.Skill{{
		ID: "skill.audit", Name: "audit", Description: "Audit the ledger",
		Content: "Run `make audit`.", AllowedTools: []string{"bash", "read"},
	}}
	p.Agents = []canonical.Agent{{
		ID: "agent.reviewer", Name: "reviewer", Description: "Reviews PRs",
		Instructions: "Review diffs.", Tools: []string{"read", "grep"}, ModelPreference: "opus",
	}}
	p.Decisions = []canonical.Decision{{
		ID: "decision.ledger", Title: "Append-only ledger", Status: canonical.DecisionAccepted,
		Context: "We need auditability.", Decision: "Entries are append-only.",
		Consequences:     "Deletion is never allowed.",
		AgentConstraints: []string{"Never UPDATE the entries table."},
	}}
	p.OpaqueBlocks = []canonical.OpaqueBlock{{
		ID: "opaque.override", Provider: "codex", SourcePath: "AGENTS.override.md",
		Content: "# Overrides\n", Reason: "not modelled", Hash: provenance.HashString("# Overrides\n"),
		ReemitForRoundTrip: true,
	}}
	p.Sort()
	return p
}

func TestProjectSurvivesASaveLoadRoundTrip(t *testing.T) {
	ctx := context.Background()
	ws, err := workspace.Open(t.TempDir(), workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	original := richProject()
	if _, err := SaveProject(ctx, ws, original, false); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	loaded, diags, err := LoadProjectWithDiagnostics(ctx, ws)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	for _, d := range diags {
		if d.Severity == "error" {
			t.Fatalf("loading produced an error: %+v", d)
		}
	}

	want, err := canonical.MarshalProject(original)
	if err != nil {
		t.Fatal(err)
	}
	got, err := canonical.MarshalProject(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Fatalf("a save/load round trip changed the project\n--- saved ---\n%s\n--- loaded ---\n%s", want, got)
	}
}

func TestEncodingIsStable(t *testing.T) {
	ctx := context.Background()
	ws, err := workspace.Open(t.TempDir(), workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SaveProject(ctx, ws, richProject(), false); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadProject(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	first, err := EncodeProject(loaded)
	if err != nil {
		t.Fatal(err)
	}
	// Re-encoding what was loaded must reproduce exactly the same files.
	original, err := EncodeProject(richProject())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != len(original.Files) {
		t.Fatalf("file count changed: %d vs %d", len(first.Files), len(original.Files))
	}
	for path, want := range original.Files {
		got, ok := first.Files[path]
		if !ok {
			t.Errorf("%s disappeared after a round trip", path)
			continue
		}
		if string(want) != string(got) {
			t.Errorf("%s changed after a round trip\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
		}
	}
}

func TestEntityFilesAreReadable(t *testing.T) {
	encoded, err := EncodeProject(richProject())
	if err != nil {
		t.Fatal(err)
	}
	rule := string(encoded.Files[".stemma/rules/validate.md"])
	for _, want := range []string{
		"title: Validate at the boundary",
		"priority: must",
		"type: path-scoped",
		"Validate every request body at the boundary.",
		"## Rationale",
		"## Good examples",
		"- handler -> service",
	} {
		if !strings.Contains(rule, want) {
			t.Errorf("rule file is missing %q:\n%s", want, rule)
		}
	}
	// The agent-facing instruction must come before the human-only sections.
	if strings.Index(rule, "Validate every request body") > strings.Index(rule, "## Rationale") {
		t.Error("the instruction must precede the rationale")
	}
	decision := string(encoded.Files[".stemma/decisions/ledger.md"])
	for _, want := range []string{"status: accepted", "## Context", "## Agent constraints"} {
		if !strings.Contains(decision, want) {
			t.Errorf("decision file is missing %q:\n%s", want, decision)
		}
	}
}

func TestPruneRemovesEntitiesNoLongerInTheProject(t *testing.T) {
	ctx := context.Background()
	ws, err := workspace.Open(t.TempDir(), workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SaveProject(ctx, ws, richProject(), false); err != nil {
		t.Fatal(err)
	}
	trimmed := richProject()
	trimmed.Rules = nil
	stale, err := SaveProject(ctx, ws, trimmed, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0] != ".stemma/rules/validate.md" {
		t.Fatalf("pruned = %v", stale)
	}
	if ok, _ := ws.Exists(".stemma/rules/validate.md"); ok {
		t.Error("the stale entity file was not removed")
	}
	// Without prune, nothing is removed.
	if _, err := SaveProject(ctx, ws, richProject(), false); err != nil {
		t.Fatal(err)
	}
	trimmed2 := richProject()
	trimmed2.Skills = nil
	stale, err = SaveProject(ctx, ws, trimmed2, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Fatalf("prune=false removed %v", stale)
	}
	if ok, _ := ws.Exists(".stemma/skills/audit.md"); !ok {
		t.Error("a file was removed without prune")
	}
}

func TestUnknownHeadingsInAnEntityAreKept(t *testing.T) {
	ctx := context.Background()
	ws, err := workspace.Open(t.TempDir(), workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SaveProject(ctx, ws, richProject(), false); err != nil {
		t.Fatal(err)
	}
	// A person adds their own heading to a rule file.
	native, _ := ws.Native(".stemma/rules/validate.md")
	data, err := os.ReadFile(native)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data),
		"Validate every request body at the boundary.",
		"Validate every request body at the boundary.\n\n## Notes for the team\n\nAsk Ana first.", 1)
	if err := os.WriteFile(native, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadProject(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range loaded.Rules {
		if strings.Contains(r.Instruction, "## Notes for the team") &&
			strings.Contains(r.Instruction, "Ask Ana first.") {
			found = true
		}
	}
	if !found {
		t.Fatal("an unrecognised heading was dropped instead of being kept as content")
	}
}

func TestV1ProjectGivesAHelpfulError(t *testing.T) {
	ctx := context.Background()
	ws, err := workspace.Open(t.TempDir(), workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFile(ws, ProjectFile,
		[]byte(`{"schemaVersion":1,"id":"x","name":"x","targets":[],"contextDocuments":[],"rules":[],`+
			`"procedures":[],"skills":[],"agents":[],"decisions":[],"opaqueBlocks":[]}`)); err != nil {
		t.Fatal(err)
	}
	_, err = LoadProject(ctx, ws)
	if err == nil {
		t.Fatal("a version 1 project must be refused")
	}
	if !strings.Contains(err.Error(), "stemma import") {
		t.Errorf("the error should say how to migrate: %v", err)
	}
}

func sortedPaths(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
