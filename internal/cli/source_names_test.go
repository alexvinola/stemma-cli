package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/cli"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/manifest"
	"github.com/alexvinola/stemma-cli/internal/provenance"
)

// Issue #61 inputs: a scoped instruction whose description differs from its
// file name, and a project skill whose directory is its invocation name.
func sourceNamesHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.write(".github/instructions/python.instructions.md",
		"---\napplyTo: \"**/*.py\"\ndescription: Python conventions for every service\n---\n\nUse black.\n")
	h.write(".github/skills/review/SKILL.md", "---\nname: review\ndescription: Review a change\n---\n\nCheck tests.\n")
	if res := h.run("import", "--from", "github-copilot", "--targets", "claude"); res.code != cli.ExitOK {
		t.Fatalf("import: %d\n%s\n%s", res.code, res.stdout, res.stderr)
	}
	return h
}

const (
	newRulePath  = ".claude/rules/python.md"
	newSkillPath = ".claude/skills/review/SKILL.md"
	// The destinations and bytes written by the previous naming policy,
	// which always used the complete canonical ID across providers.
	oldRulePath  = ".claude/rules/context-python-conventions-for-every-service.md"
	oldSkillPath = ".claude/skills/skill-review/SKILL.md"
	oldRule      = "---\npaths:\n  - \"**/*.py\"\ndescription: Python conventions for every service\n---\n\n" +
		"# Python conventions for every service\n\nUse black.\n"
	oldSkill = "---\nname: skill-review\ndescription: Review a change\n---\n\n# review\n\nCheck tests.\n"
)

type planReport struct {
	ExitCode    int                      `json:"exitCode"`
	Diagnostics []diagnostics.Diagnostic `json:"diagnostics"`
	Data        compiler.Plan            `json:"data"`
}

func (h *harness) planJSON(t *testing.T) planReport {
	t.Helper()
	res := h.run("plan", "--target", "claude", "--json")
	var report planReport
	if err := json.Unmarshal([]byte(res.stdout), &report); err != nil {
		t.Fatalf("plan JSON: %v\n%s\n%s", err, res.stdout, res.stderr)
	}
	report.ExitCode = res.code
	return report
}

func changeKinds(p compiler.Plan) map[string]compiler.ChangeKind {
	out := map[string]compiler.ChangeKind{}
	for _, c := range p.Changes {
		out[string(c.Path)] = c.Kind
	}
	return out
}

func TestCopilotToClaudeKeepsSourceNamesThroughApply(t *testing.T) {
	h := sourceNamesHarness(t)
	report := h.planJSON(t)
	if want := map[string]compiler.ChangeKind{
		newRulePath: compiler.ChangeCreate, newSkillPath: compiler.ChangeCreate,
	}; report.ExitCode != cli.ExitOK || !reflect.DeepEqual(changeKinds(report.Data), want) {
		t.Fatalf("plan = %d %+v", report.ExitCode, changeKinds(report.Data))
	}
	if res := h.run("apply", "--target", "claude", "--yes"); res.code != cli.ExitOK {
		t.Fatalf("apply: %d\n%s\n%s", res.code, res.stdout, res.stderr)
	}
	if got := h.read(newSkillPath); got != "---\nname: review\ndescription: Review a change\n---\n\n# review\n\nCheck tests.\n" {
		t.Fatalf("skill = %q", got)
	}
	again := h.planJSON(t)
	if again.Data.HasChanges() || again.ExitCode != cli.ExitOK {
		t.Fatalf("re-plan after apply proposes changes: %+v", again.Data.Changes)
	}
	if res := h.run("check", "--target", "claude"); res.code != cli.ExitOK {
		t.Fatalf("check: %d\n%s", res.code, res.stdout)
	}
}

// A project applied with the previous naming policy moves to the preserved
// source names: the new files are created, the old ones are proposed for
// deletion and never removed, and ownership is kept until the user deletes
// them.
func TestUpgradeFromCanonicalIDNamesProposesDeletion(t *testing.T) {
	h := sourceNamesHarness(t)
	h.write(oldRulePath, oldRule)
	h.write(oldSkillPath, oldSkill)
	m, err := manifest.Unmarshal([]byte(h.read(".stemma/manifest.json")))
	if err != nil {
		t.Fatal(err)
	}
	m.Targets["claude"] = manifest.TargetRecord{GeneratedFiles: []manifest.GeneratedRecord{
		{Path: oldRulePath, Hash: provenance.HashString(oldRule),
			Entities: []string{"context.python-conventions-for-every-service"}},
		{Path: oldSkillPath, Hash: provenance.HashString(oldSkill), Entities: []string{"skill.review"}},
	}}
	data, err := manifest.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	h.write(".stemma/manifest.json", string(data))

	want := map[string]compiler.ChangeKind{
		newRulePath: compiler.ChangeCreate, newSkillPath: compiler.ChangeCreate,
		oldRulePath: compiler.ChangeDeleteProposed, oldSkillPath: compiler.ChangeDeleteProposed,
	}
	report := h.planJSON(t)
	if report.ExitCode != cli.ExitOK || !reflect.DeepEqual(changeKinds(report.Data), want) {
		t.Fatalf("plan = %d %+v", report.ExitCode, changeKinds(report.Data))
	}
	var proposed []string
	for _, d := range report.Diagnostics {
		if d.Code == diagnostics.DeleteProposed {
			proposed = append(proposed, d.Path)
		}
		if d.Code == diagnostics.UntrackedDestConfl {
			t.Fatalf("new destination treated as untracked: %+v", d)
		}
	}
	sort.Strings(proposed)
	if !reflect.DeepEqual(proposed, []string{oldRulePath, oldSkillPath}) {
		t.Fatalf("%s = %v", diagnostics.DeleteProposed, proposed)
	}

	if res := h.run("apply", "--target", "claude", "--yes"); res.code != cli.ExitOK {
		t.Fatalf("apply: %d\n%s\n%s", res.code, res.stdout, res.stderr)
	}
	if h.read(oldRulePath) != oldRule || h.read(oldSkillPath) != oldSkill {
		t.Fatal("apply changed or deleted output of the previous naming policy")
	}
	if !h.exists(newRulePath) || !h.exists(newSkillPath) {
		t.Fatal("apply did not create the preserved source names")
	}
	tracked := func() []string {
		m, err := manifest.Unmarshal([]byte(h.read(".stemma/manifest.json")))
		if err != nil {
			t.Fatal(err)
		}
		return m.TrackedPaths("claude")
	}
	if got := tracked(); len(got) != 4 {
		t.Fatalf("apply forgot a pending deletion: %v", got)
	}
	again := h.planJSON(t)
	if want := map[string]compiler.ChangeKind{
		newRulePath: compiler.ChangeUnchanged, newSkillPath: compiler.ChangeUnchanged,
		oldRulePath: compiler.ChangeDeleteProposed, oldSkillPath: compiler.ChangeDeleteProposed,
	}; !reflect.DeepEqual(changeKinds(again.Data), want) {
		t.Fatalf("re-plan = %+v", changeKinds(again.Data))
	}

	for _, p := range []string{oldRulePath, oldSkillPath} {
		if err := os.Remove(filepath.Join(h.root, filepath.FromSlash(p))); err != nil {
			t.Fatal(err)
		}
	}
	if res := h.run("apply", "--target", "claude", "--yes"); res.code != cli.ExitOK {
		t.Fatalf("cleanup apply: %d\n%s", res.code, res.stderr)
	}
	if got := tracked(); !reflect.DeepEqual(got, []string{newRulePath, newSkillPath}) {
		t.Fatalf("tracked after cleanup = %v", got)
	}
	if final := h.planJSON(t); final.Data.HasChanges() {
		t.Fatalf("final plan proposes changes: %+v", final.Data.Changes)
	}
}

// A preserved name that already exists as a file Stemma does not own is a
// conflict, exactly like any other untracked destination.
func TestSourceNameDestinationOwnedByUserIsAConflict(t *testing.T) {
	h := sourceNamesHarness(t)
	h.write(newRulePath, "User rule.\n")
	before := h.snapshot()
	report := h.planJSON(t)
	if report.ExitCode != cli.ExitDiagnostics || changeKinds(report.Data)[newRulePath] != compiler.ChangeConflict {
		t.Fatalf("plan = %d %+v", report.ExitCode, changeKinds(report.Data))
	}
	if res := h.run("apply", "--target", "claude", "--yes"); res.code != cli.ExitDiagnostics {
		t.Fatalf("apply exit = %d", res.code)
	}
	assertSameTree(t, before, h.snapshot(), "conflicting apply")
}
