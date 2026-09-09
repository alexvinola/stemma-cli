package compiler_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
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
	// A retired generated file may have been edited before the user decides
	// whether to remove it. Retaining the tombstone must not adopt those bytes.
	editedPath, _ := ws.Native(".claude/rules/context-api-layer-conventions.md")
	if err := os.WriteFile(editedPath, []byte("user edit to retired output\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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
	res, err = compiler.Apply(ctx, ws, plan, compiler.ApplyOptions{Manifest: m})
	if err != nil {
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
		beforeHash, beforeTracked := m.Tracked(string(canonical.TargetClaude), c.Path)
		afterHash, afterTracked := res.Manifest.Tracked(string(canonical.TargetClaude), c.Path)
		if !beforeTracked || !afterTracked || afterHash != beforeHash {
			t.Errorf("apply forgot ownership of %s: before=(%q, %t), after=(%q, %t)",
				c.Path, beforeHash, beforeTracked, afterHash, afterTracked)
		}
	}

	again, err := compiler.BuildPlan(ctx, ws, stripped, compiler.PlanOptions{
		Target: canonical.TargetClaude, Profile: profiles.Default(canonical.TargetClaude), Manifest: res.Manifest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !again.HasChanges() {
		t.Fatal("a retained delete proposal must keep the plan out of date")
	}
	if got := again.CountByKind()[compiler.ChangeDeleteProposed]; got != proposals {
		t.Fatalf("delete proposals after apply = %d, want %d", got, proposals)
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

func TestApplyRetainsPlanDiagnosticsOnEveryOutcome(t *testing.T) {
	warning := diagnostics.New(diagnostics.AgentNotNative, diagnostics.SeverityWarning, "agent is adapted").
		WithEntity("agent.reviewer").WithTarget("codex").WithSuggestion("Review the projection.")
	note := diagnostics.New(diagnostics.RegeneratedFile, diagnostics.SeverityInfo, "file regenerated").WithPath("a.md")
	for _, outcome := range []string{"success", "blocked", "stale", "cancelled", "read-error", "rollback"} {
		t.Run(outcome, func(t *testing.T) {
			ws, err := workspace.Open(t.TempDir(), workspace.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			plan := compiler.Plan{
				Target:      canonical.TargetCodex,
				Diagnostics: []diagnostics.Diagnostic{note, warning, warning},
				Changes:     []compiler.Change{{Path: "a.md", Kind: compiler.ChangeCreate, Content: "generated\n"}},
			}
			opts := compiler.ApplyOptions{Manifest: manifest.New()}
			var wantExtra diagnostics.Code
			switch outcome {
			case "blocked":
				plan.Diagnostics = append(plan.Diagnostics, diagnostics.New(diagnostics.MissingRequired, diagnostics.SeverityError, "required field missing"))
			case "stale":
				native, _ := ws.Native("a.md")
				if err := os.WriteFile(native, []byte("user content"), 0o644); err != nil {
					t.Fatal(err)
				}
				wantExtra = diagnostics.StalePlan
			case "cancelled":
				cancel()
			case "read-error":
				native, _ := ws.Native("a.md")
				if err := os.Mkdir(native, 0o755); err != nil {
					t.Fatal(err)
				}
			case "rollback":
				// Both writes can be queued, but a file cannot also be the
				// parent directory of the manifest during commit.
				opts.ManifestPath = "a.md/manifest.json"
				wantExtra = diagnostics.WriteRolledBack
			}
			res, err := compiler.Apply(ctx, ws, plan, opts)
			if (err == nil) != (outcome == "success") {
				t.Fatalf("unexpected error: %v", err)
			}
			var expected diagnostics.Bag
			expected.Extend(plan.Diagnostics)
			for _, d := range expected.Items() {
				count := 0
				for _, got := range res.Diagnostics {
					if reflect.DeepEqual(got, d) {
						count++
					}
				}
				if count != 1 {
					t.Errorf("diagnostic %s occurs %d times: %+v", d.Code, count, res.Diagnostics)
				}
			}
			if wantExtra != "" {
				found := false
				for _, d := range res.Diagnostics {
					found = found || d.Code == wantExtra
				}
				if !found {
					t.Errorf("missing transaction diagnostic %s: %+v", wantExtra, res.Diagnostics)
				}
			}
			sorted := append([]diagnostics.Diagnostic{}, res.Diagnostics...)
			diagnostics.Sort(sorted)
			if !reflect.DeepEqual(sorted, res.Diagnostics) {
				t.Fatal("diagnostics are not sorted")
			}
		})
	}
}
