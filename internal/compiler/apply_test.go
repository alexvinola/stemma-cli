package compiler_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/manifest"
	"github.com/alexvinola/stemma-cli/internal/profiles"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

func importFixture(t *testing.T, name string, format canonical.TargetFormat) (*workspace.Workspace, canonical.Project) {
	t.Helper()
	ws := materialize(t, filepath.Join(testdataDir, name, "input"))
	res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{
		Format: format, ProjectID: "prj_fixture", ProjectName: "Fixture",
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return ws, res.Project
}

func TestApplyThenPlanIsANoOp(t *testing.T) {
	ctx := context.Background()
	ws, project := importFixture(t, "copilot/basic", canonical.TargetCopilot)
	m := manifest.New()

	for _, target := range []canonical.TargetFormat{
		canonical.TargetClaude, canonical.TargetCodex, canonical.TargetKiro,
	} {
		plan, err := compiler.BuildPlan(ctx, ws, project, compiler.PlanOptions{
			Target: target, Profile: profiles.Default(target), Manifest: m,
		})
		if err != nil {
			t.Fatalf("plan %s: %v", target, err)
		}
		res, err := compiler.Apply(ctx, ws, plan, compiler.ApplyOptions{Manifest: m})
		if err != nil {
			t.Fatalf("apply %s: %v", target, err)
		}
		m = res.Manifest

		again, err := compiler.BuildPlan(ctx, ws, project, compiler.PlanOptions{
			Target: target, Profile: profiles.Default(target), Manifest: m,
		})
		if err != nil {
			t.Fatalf("re-plan %s: %v", target, err)
		}
		if again.HasChanges() {
			for _, c := range again.Changes {
				if c.Kind != compiler.ChangeUnchanged {
					t.Errorf("%s: %s is %s after apply", target, c.Path, c.Kind)
				}
			}
		}
	}
}

func TestApplyIsStaleWhenAFileChanges(t *testing.T) {
	ctx := context.Background()
	ws, project := importFixture(t, "copilot/basic", canonical.TargetCopilot)
	plan, err := compiler.BuildPlan(ctx, ws, project, compiler.PlanOptions{
		Target: canonical.TargetClaude, Profile: profiles.Default(canonical.TargetClaude),
		Manifest: manifest.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	native, _ := ws.Native("CLAUDE.md")
	if err := os.WriteFile(native, []byte("someone else wrote this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = compiler.Apply(ctx, ws, plan, compiler.ApplyOptions{Manifest: manifest.New()})
	if !errors.Is(err, compiler.ErrStalePlan) {
		t.Fatalf("err = %v, want ErrStalePlan", err)
	}
	got, _ := os.ReadFile(native)
	if string(got) != "someone else wrote this\n" {
		t.Fatal("a stale apply modified the file")
	}
}

func TestApplyRollsBackOnPartialFailure(t *testing.T) {
	ctx := context.Background()
	ws, project := importFixture(t, "copilot/basic", canonical.TargetCopilot)
	plan, err := compiler.BuildPlan(ctx, ws, project, compiler.PlanOptions{
		Target: canonical.TargetClaude, Profile: profiles.Default(canonical.TargetClaude),
		Manifest: manifest.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Make one destination impossible to write by putting a directory there.
	var blocked string
	for _, c := range plan.Changes {
		if strings.HasPrefix(c.Path, ".claude/skills/") {
			blocked = c.Path
			break
		}
	}
	if blocked == "" {
		t.Skip("no suitable destination in this plan")
	}
	native, _ := ws.Native(blocked)
	if err := os.MkdirAll(native, 0o755); err != nil {
		t.Fatal(err)
	}
	// The plan recorded the destination as absent, and a directory has no hash,
	// so this must fail cleanly rather than write half a configuration.
	_, err = compiler.Apply(ctx, ws, plan, compiler.ApplyOptions{Manifest: manifest.New()})
	if err == nil {
		t.Fatal("apply must fail when a destination cannot be written")
	}
	// Files that sort before the blocked one must have been rolled back.
	for _, c := range plan.Changes {
		if c.Path >= blocked || c.Kind != compiler.ChangeCreate {
			continue
		}
		p, _ := ws.Native(c.Path)
		if _, statErr := os.Stat(p); statErr == nil {
			t.Errorf("%s survived a failed transaction", c.Path)
		}
	}
}

func TestApplyNeverDeletes(t *testing.T) {
	ctx := context.Background()
	ws, project := importFixture(t, "copilot/basic", canonical.TargetCopilot)
	m := manifest.New()
	plan, err := compiler.BuildPlan(ctx, ws, project, compiler.PlanOptions{
		Target: canonical.TargetClaude, Profile: profiles.Default(canonical.TargetClaude), Manifest: m,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := compiler.Apply(ctx, ws, plan, compiler.ApplyOptions{Manifest: m})
	if err != nil {
		t.Fatal(err)
	}
	m = res.Manifest

	// Drop every context document: the generated files are no longer produced.
	stripped := project
	stripped.ContextDocuments = nil
	stripped.Rules = nil
	plan, err = compiler.BuildPlan(ctx, ws, stripped, compiler.PlanOptions{
		Target: canonical.TargetClaude, Profile: profiles.Default(canonical.TargetClaude), Manifest: m,
	})
	if err != nil {
		t.Fatal(err)
	}
	var proposals int
	for _, c := range plan.Changes {
		if c.Kind == compiler.ChangeDeleteProposed {
			proposals++
		}
	}
	if proposals == 0 {
		t.Fatal("expected delete proposals")
	}
	if _, err := compiler.Apply(ctx, ws, plan, compiler.ApplyOptions{Manifest: m}); err != nil {
		t.Fatal(err)
	}
	for _, c := range plan.Changes {
		if c.Kind != compiler.ChangeDeleteProposed {
			continue
		}
		native, _ := ws.Native(c.Path)
		if _, err := os.Stat(native); err != nil {
			t.Errorf("apply deleted %s; deletions must never be executed", c.Path)
		}
	}
}

func TestApplyTimestampDoesNotAffectGeneratedFiles(t *testing.T) {
	ctx := context.Background()
	run := func(now time.Time) string {
		ws, project := importFixture(t, "copilot/basic", canonical.TargetCopilot)
		plan, err := compiler.BuildPlan(ctx, ws, project, compiler.PlanOptions{
			Target: canonical.TargetClaude, Profile: profiles.Default(canonical.TargetClaude),
			Manifest: manifest.New(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := compiler.Apply(ctx, ws, plan, compiler.ApplyOptions{
			Manifest: manifest.New(), Now: now,
		}); err != nil {
			t.Fatal(err)
		}
		native, _ := ws.Native("CLAUDE.md")
		data, err := os.ReadFile(native)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	a := run(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	b := run(time.Date(2030, 6, 6, 12, 0, 0, 0, time.UTC))
	if a != b {
		t.Fatal("a recorded timestamp changed generated output")
	}
}

func TestPlanIsSerializableAndReplayable(t *testing.T) {
	ctx := context.Background()
	ws, project := importFixture(t, "claude/basic", canonical.TargetClaude)
	plan, err := compiler.BuildPlan(ctx, ws, project, compiler.PlanOptions{
		Target: canonical.TargetCodex, Profile: profiles.Default(canonical.TargetCodex),
		Manifest: manifest.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := compiler.MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	back, err := compiler.UnmarshalPlan(data)
	if err != nil {
		t.Fatal(err)
	}
	again, err := compiler.MarshalPlan(back)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(again) {
		t.Fatal("plan serialization is not stable")
	}
	if _, err := compiler.Apply(ctx, ws, back, compiler.ApplyOptions{Manifest: manifest.New()}); err != nil {
		t.Fatalf("replaying a saved plan failed: %v", err)
	}
}

func TestUnmarshalPlanRejectsBadDocuments(t *testing.T) {
	for _, in := range []string{
		`{"schemaVersion":99}`,
		`{"schemaVersion":1,"target":"made-up"}`,
		`{"schemaVersion":1,"nope":1}`,
	} {
		if _, err := compiler.UnmarshalPlan([]byte(in)); err == nil {
			t.Errorf("UnmarshalPlan(%q) should fail", in)
		}
	}
}
