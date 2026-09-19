package compiler_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/manifest"
	"github.com/alexvinola/stemma-cli/internal/optimizer"
	"github.com/alexvinola/stemma-cli/internal/profiles"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

var deduplicationTargets = []canonical.TargetFormat{
	canonical.TargetClaude,
	canonical.TargetCodex,
	canonical.TargetCopilot,
	canonical.TargetKiro,
}

func TestDeduplicationPreservesEffectiveGuidanceAcrossTargets(t *testing.T) {
	falseValue := false

	tests := []struct {
		name             string
		project          func() canonical.Project
		profile          func(canonical.TargetFormat) profiles.Profile
		projected        []string
		skipped          []string
		wantText         []string
		wantDiagnostic   diagnostics.Code
		wantOverridePath string
	}{
		{
			name: "disabled rule sorts before enabled rule",
			project: func() canonical.Project {
				return duplicateRules("rule.a-disabled", false, "rule.b-enabled", true, canonical.Always())
			},
			projected: []string{"rule.b-enabled"}, skipped: []string{"rule.a-disabled"},
			wantText: []string{"Retain the enabled instruction."},
		},
		{
			name: "disabled rule sorts after enabled rule",
			project: func() canonical.Project {
				return duplicateRules("rule.a-enabled", true, "rule.b-disabled", false, canonical.Always())
			},
			projected: []string{"rule.a-enabled"}, skipped: []string{"rule.b-disabled"},
			wantText: []string{"Retain the enabled instruction."},
		},
		{
			name: "disabled document sorts before default-enabled document",
			project: func() canonical.Project {
				return duplicateDocuments("context.a-disabled", &falseValue, canonical.AudienceAgent,
					"context.b-enabled", nil, canonical.AudienceAgent, canonical.Always())
			},
			projected: []string{"context.b-enabled"}, skipped: []string{"context.a-disabled"},
			wantText: []string{"Retain the enabled context."},
		},
		{
			name: "disabled document sorts after default-enabled document",
			project: func() canonical.Project {
				return duplicateDocuments("context.a-enabled", nil, canonical.AudienceAgent,
					"context.b-disabled", &falseValue, canonical.AudienceAgent, canonical.Always())
			},
			projected: []string{"context.a-enabled"}, skipped: []string{"context.b-disabled"},
			wantText: []string{"Retain the enabled context."},
		},
		{
			name: "human document does not replace agent document",
			project: func() canonical.Project {
				return duplicateDocuments("context.a-human", nil, canonical.AudienceHuman,
					"context.b-agent", nil, canonical.AudienceAgent, canonical.Always())
			},
			projected: []string{"context.b-agent"}, skipped: []string{"context.a-human"},
			wantText: []string{"Retain the enabled context."},
		},
		{
			name: "profile exclusion does not replace rule",
			project: func() canonical.Project {
				return duplicateRules("rule.a-excluded", true, "rule.b-included", true, canonical.Always())
			},
			profile: func(target canonical.TargetFormat) profiles.Profile {
				profile := profiles.Default(target)
				include := false
				profile.Overrides["rule.a-excluded"] = profiles.Override{Include: &include}
				return profile
			},
			projected: []string{"rule.b-included"}, skipped: []string{"rule.a-excluded"},
			wantText: []string{"Retain the enabled instruction."},
		},
		{
			name: "profile exclusion does not replace document",
			project: func() canonical.Project {
				return duplicateDocuments("context.a-excluded", nil, canonical.AudienceAgent,
					"context.b-included", nil, canonical.AudienceAgent, canonical.Always())
			},
			profile: func(target canonical.TargetFormat) profiles.Profile {
				profile := profiles.Default(target)
				include := false
				profile.Overrides["context.a-excluded"] = profiles.Override{Include: &include}
				return profile
			},
			projected: []string{"context.b-included"}, skipped: []string{"context.a-excluded"},
			wantText: []string{"Retain the enabled context."},
		},
		{
			name: "content override preserves both wordings",
			project: func() canonical.Project {
				return duplicateRules("rule.a-overridden", true, "rule.b-original", true, canonical.Always())
			},
			profile: func(target canonical.TargetFormat) profiles.Profile {
				profile := profiles.Default(target)
				profile.Overrides["rule.a-overridden"] = profiles.Override{ContentOverride: "Target-specific instruction."}
				return profile
			},
			projected:      []string{"rule.a-overridden", "rule.b-original"},
			wantText:       []string{"Target-specific instruction.", "Retain the enabled instruction."},
			wantDiagnostic: diagnostics.TargetOverridesContent,
		},
		{
			name: "activation override preserves both deliveries",
			project: func() canonical.Project {
				return duplicateRules("rule.a-rescoped", true, "rule.b-always", true, canonical.Always())
			},
			profile: func(target canonical.TargetFormat) profiles.Profile {
				profile := profiles.Default(target)
				activation := canonical.PathScoped([]string{"src/**"}, nil)
				profile.Overrides["rule.a-rescoped"] = profiles.Override{Activation: &activation}
				return profile
			},
			projected: []string{"rule.a-rescoped", "rule.b-always"},
			wantText:  []string{"Retain the enabled instruction."},
		},
		{
			name: "destination override preserves both deliveries",
			project: func() canonical.Project {
				return duplicateRules("rule.a-moved", true, "rule.b-default", true,
					canonical.PathScoped([]string{"src/**"}, nil))
			},
			profile: func(target canonical.TargetFormat) profiles.Profile {
				profile := profiles.Default(target)
				profile.Overrides["rule.a-moved"] = profiles.Override{Directory: "custom", Filename: "moved.md"}
				return profile
			},
			projected: []string{"rule.a-moved", "rule.b-default"},
			wantText:  []string{"Retain the enabled instruction."}, wantOverridePath: "custom/moved.md",
		},
		{
			name: "extensions preserve both entities",
			project: func() canonical.Project {
				project := duplicateRules("rule.a-extension", true, "rule.b-extension", true, canonical.Always())
				project.Rules[0].Extensions = canonical.Extensions{"future": {"mode": "one"}}
				project.Rules[1].Extensions = canonical.Extensions{"future": {"mode": "two"}}
				return project
			},
			projected: []string{"rule.a-extension", "rule.b-extension"},
			wantText:  []string{"Retain the enabled instruction."},
		},
		{
			name: "document extensions preserve both entities",
			project: func() canonical.Project {
				project := duplicateDocuments("context.a-extension", nil, canonical.AudienceAgent,
					"context.b-extension", nil, canonical.AudienceAgent, canonical.Always())
				project.ContextDocuments[0].Extensions = canonical.Extensions{"future": {"mode": "one"}}
				project.ContextDocuments[1].Extensions = canonical.Extensions{"future": {"mode": "two"}}
				return project
			},
			projected: []string{"context.a-extension", "context.b-extension"},
			wantText:  []string{"Retain the enabled context."},
		},
		{
			name: "empty override remains projection-sensitive",
			project: func() canonical.Project {
				return duplicateRules("rule.a-override", true, "rule.b-default", true, canonical.Always())
			},
			profile: func(target canonical.TargetFormat) profiles.Profile {
				profile := profiles.Default(target)
				profile.Overrides["rule.a-override"] = profiles.Override{}
				return profile
			},
			projected: []string{"rule.a-override", "rule.b-default"},
			wantText:  []string{"Retain the enabled instruction."},
		},
		{
			name: "invocation names preserve both commands",
			project: func() canonical.Project {
				project := duplicateRules("rule.a-first", true, "rule.b-second", true,
					canonical.OnDemand("review", "first-review"))
				project.Rules[1].Activation = canonical.OnDemand("review", "second-review")
				return project
			},
			projected: []string{"rule.a-first", "rule.b-second"},
			wantText:  []string{"Retain the enabled instruction."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, target := range deduplicationTargets {
				t.Run(string(target), func(t *testing.T) {
					project := tt.project()
					profile := profiles.Default(target)
					if tt.profile != nil {
						profile = tt.profile(target)
					}
					profileBefore, err := profiles.Marshal(profile)
					if err != nil {
						t.Fatal(err)
					}
					optimization := optimizer.DefaultOptions()
					optimization.PreserveEntityIDs = map[string]struct{}{"rule.unrelated": {}}
					out, err := compiler.Compile(context.Background(), project, compiler.CompileOptions{
						Target: target, Profile: profile, Optimizer: optimization,
					})
					if err != nil {
						t.Fatal(err)
					}
					profileAfter, err := profiles.Marshal(profile)
					if err != nil {
						t.Fatal(err)
					}
					if string(profileAfter) != string(profileBefore) {
						t.Fatal("compiler mutated the profile")
					}
					if len(optimization.PreserveEntityIDs) != 1 {
						t.Fatalf("compiler mutated optimizer options: %v", optimization.PreserveEntityIDs)
					}
					if _, ok := optimization.PreserveEntityIDs["rule.unrelated"]; !ok {
						t.Fatalf("compiler mutated optimizer options: %v", optimization.PreserveEntityIDs)
					}
					mappings := mappingsByID(t, project, out)
					for _, id := range tt.projected {
						mapping := mappings[id]
						if mapping.Outcome == adapters.OutcomeSkipped || len(mapping.Files) == 0 {
							t.Errorf("%s was not projected: %+v", id, mapping)
						}
						if strings.Contains(mapping.Explanation, "Removed by the deduplication pass") {
							t.Errorf("%s has a deduplication mapping: %+v", id, mapping)
						}
					}
					for _, id := range tt.skipped {
						mapping := mappings[id]
						if mapping.Outcome != adapters.OutcomeSkipped || len(mapping.Files) != 0 {
							t.Errorf("%s mapping = %+v, want an explicit skip", id, mapping)
						}
						if strings.Contains(mapping.Explanation, "Removed by the deduplication pass") {
							t.Errorf("%s was deduplicated instead of reporting its own skip reason", id)
						}
					}
					allFiles := generatedText(out)
					for _, want := range tt.wantText {
						if !strings.Contains(allFiles, want) {
							t.Errorf("generated files do not contain %q:\n%s", want, allFiles)
						}
					}
					if tt.wantDiagnostic != "" && !hasDiagnostic(out, tt.wantDiagnostic) {
						t.Errorf("diagnostics do not include %s: %+v", tt.wantDiagnostic, out.Diagnostics)
					}
					if tt.wantOverridePath != "" {
						mapping := mappings[tt.projected[0]]
						wantPath := tt.wantOverridePath
						if target == canonical.TargetCodex {
							// Codex honors the directory but requires its native AGENTS.md name.
							wantPath = "custom/AGENTS.md"
						}
						if len(mapping.Files) != 1 || mapping.Files[0] != wantPath {
							t.Errorf("override files = %v, want [%s]", mapping.Files, wantPath)
						}
					}
				})
			}
		})
	}
}

func TestDeduplicationPreservesExtensionDestinations(t *testing.T) {
	project := duplicateRules("rule.a", true, "rule.b", true, canonical.Always())
	project.Rules[0].Extensions = canonical.Extensions{"claude": {"stemma.ruleFile": "first.md"}}
	project.Rules[1].Extensions = canonical.Extensions{"claude": {"stemma.ruleFile": "second.md"}}
	out, err := compiler.Compile(context.Background(), project, compiler.CompileOptions{
		Target: canonical.TargetClaude, Optimizer: optimizer.DefaultOptions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	mappings := mappingsByID(t, project, out)
	for _, pair := range []struct{ id, path string }{
		{"rule.a", ".claude/rules/first.md"},
		{"rule.b", ".claude/rules/second.md"},
	} {
		m := mappings[pair.id]
		if m.Outcome != adapters.OutcomeExact || len(m.Files) != 1 || m.Files[0] != pair.path {
			t.Errorf("%s lost its extension-directed destination: %+v", pair.id, m)
		}
		found := false
		for _, file := range out.Files {
			if file.Path == pair.path && strings.Contains(file.Text, "Retain the enabled instruction.") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s did not receive its instruction", pair.path)
		}
	}
}

func TestDeduplicationCombinesCallerAndProfilePreservation(t *testing.T) {
	project := duplicateRules("rule.a", true, "rule.b", true, canonical.Always())
	third := project.Rules[0]
	third.ID = "rule.c"
	project.Rules = append(project.Rules, third)
	profile := profiles.Default(canonical.TargetClaude)
	profile.Overrides["rule.a"] = profiles.Override{}
	opts := optimizer.DefaultOptions()
	opts.PreserveEntityIDs = map[string]struct{}{"rule.b": {}}
	out, err := compiler.Compile(context.Background(), project, compiler.CompileOptions{
		Target: canonical.TargetClaude, Profile: profile, Optimizer: opts,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mappingsByID(t, project, out) {
		if m.Outcome == adapters.OutcomeSkipped || len(m.Files) == 0 {
			t.Errorf("caller or profile preservation was lost: %+v", m)
		}
	}
	if len(opts.PreserveEntityIDs) != 1 {
		t.Fatalf("caller preservation set was mutated: %v", opts.PreserveEntityIDs)
	}
	if _, ok := opts.PreserveEntityIDs["rule.b"]; !ok {
		t.Fatal("caller preservation set lost rule.b")
	}
}

func TestSafeDuplicatesStillDeduplicateAcrossTargets(t *testing.T) {
	tests := []struct {
		name        string
		instruction string
		other       string
	}{
		{name: "exact", instruction: "Check the result.", other: "Check the result."},
		{name: "normalized", instruction: "Check   the result.\n\n", other: "Check the result."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, target := range deduplicationTargets {
				t.Run(string(target), func(t *testing.T) {
					project := duplicateRules("rule.a", true, "rule.b", true, canonical.Always())
					project.Rules[0].Instruction = tt.instruction
					project.Rules[1].Instruction = tt.other
					out, err := compiler.Compile(context.Background(), project, compiler.CompileOptions{
						Target: target, Profile: profiles.Default(target), Optimizer: optimizer.DefaultOptions(),
					})
					if err != nil {
						t.Fatal(err)
					}
					mappings := mappingsByID(t, project, out)
					deduplicated := 0
					projected := 0
					for _, mapping := range mappings {
						if strings.Contains(mapping.Explanation, "Removed by the deduplication pass") {
							deduplicated++
						} else if mapping.Outcome != adapters.OutcomeSkipped {
							projected++
						}
					}
					if deduplicated != 1 || projected != 1 {
						t.Errorf("deduplicated=%d projected=%d mappings=%+v", deduplicated, projected, out.Mappings)
					}
				})
			}
		})
	}
}

func TestDeduplicationIsDeterministicAndDoesNotMutateCanonicalInput(t *testing.T) {
	project := duplicateRules("rule.a-disabled", false, "rule.b-enabled", true, canonical.Always())
	project.ContextDocuments = duplicateDocuments("context.a-human", nil, canonical.AudienceHuman,
		"context.b-agent", nil, canonical.AudienceAgent, canonical.Always()).ContextDocuments
	before, err := canonical.MarshalProject(project)
	if err != nil {
		t.Fatal(err)
	}

	for _, target := range deduplicationTargets {
		first, err := compiler.Compile(context.Background(), project, compiler.CompileOptions{
			Target: target, Profile: profiles.Default(target), Optimizer: optimizer.DefaultOptions(),
		})
		if err != nil {
			t.Fatal(err)
		}
		after, err := canonical.MarshalProject(project)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("%s mutated canonical input", target)
		}

		reordered := project
		reordered.Rules = append([]canonical.Rule{}, project.Rules...)
		reordered.ContextDocuments = append([]canonical.ContextDocument{}, project.ContextDocuments...)
		reordered.Rules[0], reordered.Rules[1] = reordered.Rules[1], reordered.Rules[0]
		reordered.ContextDocuments[0], reordered.ContextDocuments[1] =
			reordered.ContextDocuments[1], reordered.ContextDocuments[0]
		second, err := compiler.Compile(context.Background(), reordered, compiler.CompileOptions{
			Target: target, Profile: profiles.Default(target), Optimizer: optimizer.DefaultOptions(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first.Files, second.Files) ||
			!reflect.DeepEqual(first.Mappings, second.Mappings) ||
			!reflect.DeepEqual(first.Diagnostics, second.Diagnostics) {
			t.Errorf("%s compilation depends on canonical collection order", target)
		}
	}
}

func TestDeduplicationApplyThenReplanIsStable(t *testing.T) {
	project := duplicateRules("rule.a-disabled", false, "rule.b-enabled", true, canonical.Always())
	ws, err := workspace.Open(t.TempDir(), workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	currentManifest := manifest.New()
	for _, target := range deduplicationTargets {
		profile := profiles.Default(target)
		plan, err := compiler.BuildPlan(context.Background(), ws, project, compiler.PlanOptions{
			Target: target, Profile: profile, Manifest: currentManifest,
		})
		if err != nil {
			t.Fatal(err)
		}
		applied, err := compiler.Apply(context.Background(), ws, plan, compiler.ApplyOptions{Manifest: currentManifest})
		if err != nil {
			t.Fatal(err)
		}
		currentManifest = applied.Manifest
		again, err := compiler.BuildPlan(context.Background(), ws, project, compiler.PlanOptions{
			Target: target, Profile: profile, Manifest: currentManifest,
		})
		if err != nil {
			t.Fatal(err)
		}
		if again.HasChanges() {
			t.Errorf("%s has changes after apply: %+v", target, again.Changes)
		}
	}
}

func duplicateRules(firstID string, firstEnabled bool, secondID string, secondEnabled bool,
	activation canonical.Activation,
) canonical.Project {
	project := canonical.NewProject("prj_dedup", "Deduplication")
	base := canonical.Rule{
		Title: "Guidance", Instruction: "Retain the enabled instruction.",
		Priority: canonical.PriorityMust, Activation: activation,
	}
	first, second := base, base
	first.ID, first.Enabled = firstID, firstEnabled
	second.ID, second.Enabled = secondID, secondEnabled
	project.Rules = []canonical.Rule{first, second}
	return project
}

func duplicateDocuments(firstID string, firstEnabled *bool, firstAudience canonical.Audience,
	secondID string, secondEnabled *bool, secondAudience canonical.Audience,
	activation canonical.Activation,
) canonical.Project {
	project := canonical.NewProject("prj_dedup", "Deduplication")
	base := canonical.ContextDocument{
		Title: "Guidance", Kind: canonical.KindConventions,
		Content: "Retain the enabled context.", Activation: activation,
	}
	first, second := base, base
	first.ID, first.Enabled, first.Audience = firstID, firstEnabled, firstAudience
	second.ID, second.Enabled, second.Audience = secondID, secondEnabled, secondAudience
	project.ContextDocuments = []canonical.ContextDocument{first, second}
	return project
}

func mappingsByID(t *testing.T, project canonical.Project, out compiler.CompileResult) map[string]compiler.ProjectionMapping {
	t.Helper()
	want := map[string]struct{}{}
	for _, entity := range project.Entities() {
		want[entity.ID] = struct{}{}
	}
	got := map[string]compiler.ProjectionMapping{}
	for _, mapping := range out.Mappings {
		if !adapters.KnownOutcome(mapping.Outcome) {
			t.Errorf("unknown outcome for %s: %q", mapping.EntityID, mapping.Outcome)
		}
		if err := mapping.Activation.Validate(); err != nil {
			t.Errorf("invalid activation for %s: %v", mapping.EntityID, err)
		}
		if (mapping.Outcome == adapters.OutcomeLossy || mapping.Outcome == adapters.OutcomeBlocked) && len(mapping.Diagnostics) == 0 {
			t.Errorf("%s has %s outcome without a diagnostic", mapping.EntityID, mapping.Outcome)
		}
		if _, duplicate := got[mapping.EntityID]; duplicate {
			t.Errorf("%s has more than one mapping", mapping.EntityID)
		}
		got[mapping.EntityID] = mapping
	}
	for id := range want {
		if _, ok := got[id]; !ok {
			t.Errorf("%s has no mapping", id)
		}
	}
	if len(got) != len(want) {
		t.Errorf("mappings=%d entities=%d", len(got), len(want))
	}
	return got
}

func generatedText(out compiler.CompileResult) string {
	var text strings.Builder
	for _, file := range out.Files {
		text.WriteString(file.Text)
		text.WriteByte('\n')
	}
	return text.String()
}

func hasDiagnostic(out compiler.CompileResult, code diagnostics.Code) bool {
	for _, diagnostic := range out.Diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}
