package compiler_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/manifest"
	"github.com/alexvinola/stemma-cli/internal/profiles"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// importWorkspace writes provider files into a fresh workspace and imports it.
func importWorkspace(
	t *testing.T, format canonical.TargetFormat, files map[string]string,
) (*workspace.Workspace, compiler.ImportResult) {
	t.Helper()
	ws, err := workspace.Open(t.TempDir(), workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	tx := ws.Begin()
	for _, p := range paths {
		if err := tx.Add(workspace.WriteOp{Path: p, Content: []byte(files[p]), Mode: 0o644}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{
		Format: format, ProjectID: "prj_ext", ProjectName: "Extensions",
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if diagnostics.HasBlocking(res.Diagnostics) {
		t.Fatalf("import blocked: %+v", res.Diagnostics)
	}
	return ws, res
}

func mappingFor(t *testing.T, mappings []adapters.ProjectionMapping, id string) adapters.ProjectionMapping {
	t.Helper()
	for _, m := range mappings {
		if m.EntityID == id {
			return m
		}
	}
	t.Fatalf("no mapping for %s in %+v", id, mappings)
	return adapters.ProjectionMapping{}
}

// extensionDiags returns "code field severity blocking" for every STEMMA38xx
// diagnostic, after checking that the entity's mapping references it.
func extensionDiags(t *testing.T, out compiler.CompileResult, m adapters.ProjectionMapping) []string {
	t.Helper()
	referenced := map[string]bool{}
	for _, fp := range m.Diagnostics {
		referenced[fp] = true
	}
	var got []string
	for _, d := range out.Diagnostics {
		if d.Code != diagnostics.ExtensionNotProjected && d.Code != diagnostics.SecurityExtensionNotProjected {
			continue
		}
		if d.EntityID != m.EntityID || d.Target != string(out.Target) {
			t.Errorf("diagnostic is not anchored to the entity and target: %+v", d)
		}
		if !referenced[d.Fingerprint] {
			t.Errorf("mapping for %s does not reference %s (%s)", m.EntityID, d.Fingerprint, d.Field)
		}
		got = append(got, strings.Join([]string{string(d.Code), d.Field, string(d.Severity),
			map[bool]string{true: "blocking", false: "non-blocking"}[d.Blocking]}, " "))
	}
	return got
}

// TestKiroAgentExtensionFieldsProjectIndependently covers each classified kind
// on its own. The agent declares no tools, so STEMMA3301 cannot mask a
// missing extension diagnostic, which is how the original issue went unseen.
func TestKiroAgentExtensionFieldsProjectIndependently(t *testing.T) {
	const warn, sec = "STEMMA3801_EXTENSION_NOT_PROJECTED", "STEMMA3802_SECURITY_EXTENSION_NOT_PROJECTED"
	cases := []struct {
		name, field, value string
		want               []string // for a foreign target; nil means nothing reported
	}{
		{"none", "", "", nil},
		{"presentation only", "welcomeMessage", `"Hi"`, nil},
		{"context only", "resources", `["file://src/api"]`,
			[]string{warn + " extensions.kiro.resources warning non-blocking"}},
		{"behaviour only", "toolAliases", `{"read":"fs_read"}`,
			[]string{warn + " extensions.kiro.toolAliases warning non-blocking"}},
		{"unclassified only", "somethingNew", `true`,
			[]string{warn + " extensions.kiro.somethingNew warning non-blocking"}},
		{"allowedTools only", "allowedTools", `["read"]`,
			[]string{sec + " extensions.kiro.allowedTools error blocking"}},
		{"permissions only", "permissions", `{"shell":{"deny":["rm"]}}`,
			[]string{sec + " extensions.kiro.permissions error blocking"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := `{"name":"reviewer","description":"Reviews APIs","prompt":"Review diffs."`
			if tc.field != "" {
				agent += `,"` + tc.field + `":` + tc.value
			}
			agent += "}\n"
			_, res := importWorkspace(t, canonical.TargetKiro, map[string]string{".kiro/agents/reviewer.json": agent})
			for _, target := range []canonical.TargetFormat{
				canonical.TargetClaude, canonical.TargetCopilot, canonical.TargetCodex, canonical.TargetKiro,
			} {
				out, err := compiler.Compile(context.Background(), res.Project, compiler.CompileOptions{
					Target: target, Profile: profiles.Default(target),
				})
				if err != nil {
					t.Fatalf("%s: %v", target, err)
				}
				m := mappingFor(t, out.Mappings, "agent.reviewer")
				got := extensionDiags(t, out, m)
				want := tc.want
				if target == canonical.TargetKiro {
					want = nil // the same provider writes its own fields back
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: extension diagnostics = %v, want %v", target, got, want)
				}
				wantOutcome := map[canonical.TargetFormat]adapters.Outcome{
					canonical.TargetClaude: adapters.OutcomeAdapted, canonical.TargetCopilot: adapters.OutcomeAdapted,
					canonical.TargetCodex: adapters.OutcomeLossy, canonical.TargetKiro: adapters.OutcomeExact,
				}[target]
				if len(want) > 0 {
					wantOutcome = adapters.OutcomeLossy
					if !strings.Contains(m.Explanation, "kiro."+tc.field) {
						t.Errorf("%s: explanation does not name the field: %q", target, m.Explanation)
					}
				}
				if m.Outcome != wantOutcome {
					t.Errorf("%s: outcome = %s, want %s (%s)", target, m.Outcome, wantOutcome, m.Explanation)
				}
				if target == canonical.TargetKiro && tc.field != "" {
					if len(out.Files) != 1 || !strings.Contains(out.Files[0].Text, `"`+tc.field+`"`) {
						t.Errorf("kiro output must keep %s: %+v", tc.field, out.Files)
					}
				}
			}
		})
	}
}

func TestSecurityExtensionLossRequiresAcceptedFingerprint(t *testing.T) {
	ctx := context.Background()
	ws, res := importWorkspace(t, canonical.TargetKiro, map[string]string{
		".kiro/agents/reviewer.json": `{"name":"reviewer","prompt":"Review diffs.",` +
			`"allowedTools":["read"],"resources":["file://src"]}` + "\n",
	})
	target := canonical.TargetClaude
	plan := func(profile profiles.Profile) compiler.Plan {
		t.Helper()
		p, err := compiler.BuildPlan(ctx, ws, res.Project, compiler.PlanOptions{
			Target: target, Profile: profile, Manifest: manifest.New(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	// 1. The security field blocks apply; nothing is written.
	blocked := plan(profiles.Default(target))
	blocking := blocked.Blocking()
	if len(blocking) != 1 || blocking[0].Code != diagnostics.SecurityExtensionNotProjected ||
		blocking[0].Field != "extensions.kiro.allowedTools" {
		t.Fatalf("blocking diagnostics = %+v", blocking)
	}
	fingerprint := blocking[0].Fingerprint
	if _, err := compiler.Apply(ctx, ws, blocked, compiler.ApplyOptions{Manifest: manifest.New()}); !errors.Is(err, compiler.ErrBlocked) {
		t.Fatalf("apply must be blocked, got %v", err)
	}
	if _, exists, err := ws.HashFile(".claude/agents/agent-reviewer.md"); err != nil || exists {
		t.Fatalf("a blocked apply wrote the agent (exists=%v, err=%v)", exists, err)
	}

	// 2. acceptLossy on the entity is not an acceptance of the security loss.
	overridden := profiles.Default(target)
	overridden.Overrides["agent.reviewer"] = profiles.Override{AcceptLossy: true}
	if got := plan(overridden).Blocking(); len(got) != 1 || got[0].Fingerprint != fingerprint {
		t.Fatalf("acceptLossy must not unblock a security field: %+v", got)
	}

	// 3. Accepting the fingerprint downgrades exactly that diagnostic.
	accepted := profiles.Default(target)
	accepted.AcceptedDiagnostics = []string{fingerprint}
	p := plan(accepted)
	if len(p.Blocking()) != 0 {
		t.Fatalf("accepted security loss still blocks: %+v", p.Blocking())
	}
	var sawSecurity, sawContext bool
	for _, d := range p.Diagnostics {
		switch d.Field {
		case "extensions.kiro.allowedTools":
			sawSecurity = d.Severity == diagnostics.SeverityInfo && d.Fingerprint == fingerprint
		case "extensions.kiro.resources":
			sawContext = d.Severity == diagnostics.SeverityWarning
		}
	}
	if !sawSecurity || !sawContext {
		t.Fatalf("acceptance must downgrade only the accepted field: %+v", p.Diagnostics)
	}
	if m := mappingFor(t, p.Mappings, "agent.reviewer"); m.Outcome != adapters.OutcomeLossy {
		t.Fatalf("an accepted loss is still a loss: %+v", m)
	}
	applied, err := compiler.Apply(ctx, ws, p, compiler.ApplyOptions{Manifest: manifest.New()})
	if err != nil {
		t.Fatalf("apply after acceptance: %v", err)
	}
	if got := applied.Manifest.Targets[string(target)].AcceptedDiagnostics; !reflect.DeepEqual(got, []string{fingerprint}) {
		t.Fatalf("manifest accepted diagnostics = %v", got)
	}
}

func TestClaudeAgentSecurityFieldsCrossProviders(t *testing.T) {
	_, res := importWorkspace(t, canonical.TargetClaude, map[string]string{
		".claude/agents/reviewer.md": "---\nname: reviewer\ndescription: Reviews\npermissionMode: plan\n" +
			"color: blue\nmaxTurns: 5\n---\n\nReview diffs.\n",
	})
	out, err := compiler.Compile(context.Background(), res.Project, compiler.CompileOptions{
		Target: canonical.TargetCopilot, Profile: profiles.Default(canonical.TargetCopilot),
	})
	if err != nil {
		t.Fatal(err)
	}
	m := mappingFor(t, out.Mappings, "agent.reviewer")
	want := []string{
		"STEMMA3802_SECURITY_EXTENSION_NOT_PROJECTED extensions.claude.permissionMode error blocking",
		"STEMMA3801_EXTENSION_NOT_PROJECTED extensions.claude.maxTurns warning non-blocking",
	}
	if got := extensionDiags(t, out, m); !reflect.DeepEqual(got, want) || m.Outcome != adapters.OutcomeLossy {
		t.Fatalf("diagnostics = %v, outcome = %s", got, m.Outcome)
	}

	// The same provider regenerates the file with every preserved key.
	same, err := compiler.Compile(context.Background(), res.Project, compiler.CompileOptions{
		Target: canonical.TargetClaude, Profile: profiles.Default(canonical.TargetClaude),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := extensionDiags(t, same, mappingFor(t, same.Mappings, "agent.reviewer")); len(got) != 0 {
		t.Fatalf("same-provider regeneration reported loss: %v", got)
	}
	for _, key := range []string{"permissionMode: plan", "color: blue", "maxTurns: 5"} {
		if !strings.Contains(same.Files[0].Text, key) {
			t.Fatalf("regenerated agent lost %q:\n%s", key, same.Files[0].Text)
		}
	}
}

// Regenerated same-provider files must carry their preserved front matter:
// scoped Copilot instructions and Claude rules used to drop it silently.
func TestScopedFilesKeepSameProviderExtensionsWhenRegenerated(t *testing.T) {
	cases := []struct {
		format      canonical.TargetFormat
		path, body  string
		id, dest    string
		want, field string
	}{
		{canonical.TargetCopilot, ".github/instructions/api.instructions.md",
			"---\napplyTo: \"src/**\"\nexcludeAgent: code-review\n---\n\n# API\n\nValidate input.\n",
			"context.api", ".github/instructions/api.instructions.md", "excludeAgent: code-review",
			"extensions.github-copilot.excludeAgent"},
		{canonical.TargetClaude, ".claude/rules/api.md",
			"---\npaths:\n  - \"src/**\"\nsomethingNew: kept\n---\n\n# API\n\nValidate input.\n",
			"rule.api", ".claude/rules/api.md", "somethingNew: kept",
			"extensions.claude.somethingNew"},
	}
	for _, tc := range cases {
		_, res := importWorkspace(t, tc.format, map[string]string{tc.path: tc.body})
		// A content override forces regeneration instead of verbatim reuse.
		profile := profiles.Default(tc.format)
		profile.Overrides[tc.id] = profiles.Override{ContentOverride: "Validate every input."}
		out, err := compiler.Compile(context.Background(), res.Project, compiler.CompileOptions{
			Target: tc.format, Profile: profile,
		})
		if err != nil {
			t.Fatal(err)
		}
		var text string
		for _, f := range out.Files {
			if f.Path == tc.dest {
				text = f.Text
			}
		}
		if !strings.Contains(text, tc.want) || !strings.Contains(text, "Validate every input.") {
			t.Errorf("%s: regenerated file lost %q:\n%s", tc.format, tc.want, text)
		}
		if got := extensionDiags(t, out, mappingFor(t, out.Mappings, tc.id)); len(got) != 0 {
			t.Errorf("%s: same-provider regeneration reported loss: %v", tc.format, got)
		}

		// Another provider cannot write it, and says so for that field.
		other := canonical.TargetKiro
		foreign, err := compiler.Compile(context.Background(), res.Project, compiler.CompileOptions{
			Target: other, Profile: profiles.Default(other),
		})
		if err != nil {
			t.Fatal(err)
		}
		got := extensionDiags(t, foreign, mappingFor(t, foreign.Mappings, tc.id))
		if len(got) != 1 || !strings.Contains(got[0], tc.field+" warning") {
			t.Errorf("%s -> kiro: extension diagnostics = %v", tc.format, got)
		}
	}
}
