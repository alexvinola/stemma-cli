package compiler_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/quick"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/capabilities"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/parser"
	"github.com/alexvinola/stemma-cli/internal/profiles"
	"github.com/alexvinola/stemma-cli/internal/provenance"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// Issue #61: a Copilot-to-Claude migration keeps safe source names.
func TestCopilotSourceNamesSurviveMigrationToClaude(t *testing.T) {
	_, res := importWorkspace(t, canonical.TargetCopilot, map[string]string{
		".github/instructions/python.instructions.md": "---\napplyTo: \"**/*.py\"\n" +
			"description: Python conventions for every service in this repository\n---\n\nUse black.\n",
		".github/skills/review/SKILL.md": "---\nname: review\ndescription: Review a change\n---\n\nCheck tests.\n",
	})
	// Canonical IDs are unchanged: the long description still names the entity.
	var ids []string
	for _, e := range res.Project.Entities() {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)
	if want := []string{"context.python-conventions-for-every-service-in-this-repository", "skill.review"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("canonical IDs changed: %v", ids)
	}
	out, err := compiler.Compile(context.Background(), res.Project, compiler.CompileOptions{Target: canonical.TargetClaude})
	if err != nil {
		t.Fatal(err)
	}
	assertProjectionInvariants(t, res.Project, out)
	if got, want := filePaths(out), []string{".claude/rules/python.md", ".claude/skills/review/SKILL.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("destinations = %v, want %v", got, want)
	}
	for _, m := range out.Mappings {
		if m.Outcome != adapters.OutcomeExact {
			t.Fatalf("unexpected outcome: %+v", m)
		}
	}
	skill := parser.Parse(".claude/skills/review/SKILL.md", out.Files[1].Content)
	if name, _ := skill.FrontMatter.String("name"); name != "review" {
		t.Fatalf("skill name = %q, want the directory name review", name)
	}
	for _, d := range out.Diagnostics {
		if d.Code == diagnostics.SourceNameNotKept {
			t.Fatalf("unexpected %s: %+v", d.Code, d)
		}
	}
}

type namedEntity struct {
	id   string
	hint [3]string // provider, key, value
	ext  canonical.Extensions
}

func (e namedEntity) extensions() canonical.Extensions {
	ext := canonical.Extensions{}
	for provider, m := range e.ext {
		for k, v := range m {
			ext.Set(provider, k, v)
		}
	}
	if e.hint[0] != "" {
		ext.Set(e.hint[0], e.hint[1], e.hint[2])
	}
	return ext
}

// namingProject builds a project from entity IDs. Contexts and rules are
// path-scoped so that every target writes them to a file of their own.
func namingProject(entities []namedEntity) canonical.Project {
	p := canonical.NewProject("prj_names", "Names")
	for i, e := range entities {
		kind, slug, _ := canonical.ParseID(e.id)
		ext := e.extensions()
		scope := canonical.PathScoped([]string{fmt.Sprintf("s%d/**", i)}, nil)
		switch kind {
		case canonical.EntityContext:
			p.ContextDocuments = append(p.ContextDocuments, canonical.ContextDocument{ID: e.id, Title: slug,
				Content: "Context " + slug, Kind: canonical.KindOther, Audience: canonical.AudienceAgent,
				Activation: scope, Extensions: ext})
		case canonical.EntityRule:
			p.Rules = append(p.Rules, canonical.Rule{ID: e.id, Title: slug, Instruction: "Rule " + slug,
				Enabled: true, Priority: canonical.PriorityShould, Activation: scope, Extensions: ext})
		case canonical.EntityProcedure:
			p.Procedures = append(p.Procedures, canonical.Procedure{ID: e.id, Name: slug, Description: "Procedure",
				Content: "Procedure " + slug, Extensions: ext})
		case canonical.EntitySkill:
			p.Skills = append(p.Skills, canonical.Skill{ID: e.id, Name: slug, Description: "Skill",
				Content: "Skill " + slug, Extensions: ext})
		case canonical.EntityAgent:
			p.Agents = append(p.Agents, canonical.Agent{ID: e.id, Name: slug, Description: "Agent",
				Instructions: "Agent " + slug, Extensions: ext})
		}
	}
	return p
}

func filePaths(out compiler.CompileResult) []string {
	paths := make([]string, 0, len(out.Files))
	for _, f := range out.Files {
		paths = append(paths, f.Path)
	}
	return paths
}

func destinationsByEntity(out compiler.CompileResult) map[string]string {
	got := map[string]string{}
	for _, m := range out.Mappings {
		if len(m.Files) == 1 {
			got[m.EntityID] = m.Files[0]
		}
	}
	return got
}

func notKept(out compiler.CompileResult) []string {
	var ids []string
	for _, d := range out.Diagnostics {
		if d.Code == diagnostics.SourceNameNotKept {
			if d.Severity != diagnostics.SeverityInfo || d.Blocking {
				panic(fmt.Sprintf("unexpected severity: %+v", d))
			}
			ids = append(ids, d.EntityID)
		}
	}
	sort.Strings(ids)
	return ids
}

func reversed(p canonical.Project) canonical.Project {
	q := p
	rev := func(n int, swap func(i, j int)) {
		for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
			swap(i, j)
		}
	}
	q.ContextDocuments = append([]canonical.ContextDocument{}, p.ContextDocuments...)
	q.Rules = append([]canonical.Rule{}, p.Rules...)
	q.Procedures = append([]canonical.Procedure{}, p.Procedures...)
	q.Skills = append([]canonical.Skill{}, p.Skills...)
	q.Agents = append([]canonical.Agent{}, p.Agents...)
	rev(len(q.ContextDocuments), func(i, j int) {
		q.ContextDocuments[i], q.ContextDocuments[j] = q.ContextDocuments[j], q.ContextDocuments[i]
	})
	rev(len(q.Rules), func(i, j int) { q.Rules[i], q.Rules[j] = q.Rules[j], q.Rules[i] })
	rev(len(q.Procedures), func(i, j int) { q.Procedures[i], q.Procedures[j] = q.Procedures[j], q.Procedures[i] })
	rev(len(q.Skills), func(i, j int) { q.Skills[i], q.Skills[j] = q.Skills[j], q.Skills[i] })
	rev(len(q.Agents), func(i, j int) { q.Agents[i], q.Agents[j] = q.Agents[j], q.Agents[i] })
	return q
}

// compileBothOrders compiles a project and its reversed entity order and
// requires byte-identical results.
func compileBothOrders(t *testing.T, p canonical.Project, opts compiler.CompileOptions) compiler.CompileResult {
	t.Helper()
	out, err := compiler.Compile(context.Background(), p, opts)
	if err != nil {
		t.Fatalf("compile: %v %+v", err, out.Diagnostics)
	}
	assertProjectionInvariants(t, p, out)
	again, err := compiler.Compile(context.Background(), reversed(p), opts)
	if err != nil {
		t.Fatalf("compile reversed: %v", err)
	}
	if !sameOutput(out, again) {
		t.Fatalf("entity order changed the result:\n%+v\n%+v", out.Mappings, again.Mappings)
	}
	return out
}

// sameOutput compares destinations, owners, mappings and diagnostics. The
// text of a single-entity file must match too; an aggregate keeps its
// sections in the (sorted, when stored) canonical order, so only its owners
// are compared.
func sameOutput(a, b compiler.CompileResult) bool {
	if len(a.Files) != len(b.Files) || !reflect.DeepEqual(a.Mappings, b.Mappings) ||
		!reflect.DeepEqual(a.Diagnostics, b.Diagnostics) {
		return false
	}
	for i, f := range a.Files {
		g := b.Files[i]
		if f.Path != g.Path || !reflect.DeepEqual(f.Entities, g.Entities) ||
			len(f.Entities) == 1 && f.Text != g.Text {
			return false
		}
	}
	return true
}

func copilotFile(value string) [3]string {
	return [3]string{string(canonical.TargetCopilot), "stemma.instructionsFile", value}
}

func TestSourceNameCollisionsAndValidation(t *testing.T) {
	copilotSkill := func(v string) [3]string { return [3]string{string(canonical.TargetCopilot), "stemma.sourceDir", v} }
	cases := []struct {
		name     string
		entities []namedEntity
		want     map[string]string
		notKept  []string
	}{
		{
			name: "duplicate basenames from different source directories",
			entities: []namedEntity{
				{id: "context.a", hint: copilotFile("a/python.instructions.md")},
				{id: "context.b", hint: copilotFile("b/python.instructions.md")},
				{id: "context.c", hint: copilotFile("c/other.instructions.md")},
			},
			want: map[string]string{
				"context.a": ".claude/rules/context-a.md", "context.b": ".claude/rules/context-b.md",
				"context.c": ".claude/rules/other.md",
			},
			notKept: []string{"context.a", "context.b"},
		},
		{
			name: "procedure and skill share the skills namespace",
			entities: []namedEntity{
				{id: "procedure.release", hint: [3]string{string(canonical.TargetCopilot), "stemma.promptFile", "review.prompt.md"}},
				{id: "skill.checks", hint: copilotSkill("review")},
			},
			want: map[string]string{
				"procedure.release": ".claude/skills/procedure-release/SKILL.md",
				"skill.checks":      ".claude/skills/skill-checks/SKILL.md",
			},
			notKept: []string{"procedure.release", "skill.checks"},
		},
		{
			name: "case-insensitive file names",
			entities: []namedEntity{
				{id: "context.a", hint: copilotFile("x/Review.instructions.md")},
				{id: "context.b", hint: copilotFile("y/review.instructions.md")},
			},
			want:    map[string]string{"context.a": ".claude/rules/context-a.md", "context.b": ".claude/rules/context-b.md"},
			notKept: []string{"context.a", "context.b"},
		},
		{
			name: "target hint keeps precedence over a source name",
			entities: []namedEntity{
				{id: "rule.kept", hint: [3]string{string(canonical.TargetClaude), "stemma.ruleFile", "python.md"}},
				{id: "context.moved", hint: copilotFile("python.instructions.md")},
			},
			want:    map[string]string{"rule.kept": ".claude/rules/python.md", "context.moved": ".claude/rules/context-moved.md"},
			notKept: []string{"context.moved"},
		},
		{
			name: "source name equal to another entity's canonical-ID name",
			entities: []namedEntity{
				{id: "context.x", hint: copilotFile("context-foo.instructions.md")},
				{id: "context.foo"},
			},
			want:    map[string]string{"context.x": ".claude/rules/context-x.md", "context.foo": ".claude/rules/context-foo.md"},
			notKept: []string{"context.x"},
		},
		{
			// p and q collide first; their fallback names then take the name z wanted.
			name: "fallback names cascade until nothing collides",
			entities: []namedEntity{
				{id: "context.p", hint: copilotFile("dup.instructions.md")},
				{id: "context.q", hint: copilotFile("x/dup.instructions.md")},
				{id: "context.z", hint: copilotFile("context-p.instructions.md")},
			},
			want: map[string]string{
				"context.p": ".claude/rules/context-p.md", "context.q": ".claude/rules/context-q.md",
				"context.z": ".claude/rules/context-z.md",
			},
			notKept: []string{"context.p", "context.q", "context.z"},
		},
		{
			name: "source name where a nested hint needs a directory",
			entities: []namedEntity{
				{id: "rule.nested", hint: [3]string{string(canonical.TargetClaude), "stemma.ruleFile", "api.md/inner.md"}},
				{id: "context.file", hint: copilotFile("api.instructions.md")},
			},
			want:    map[string]string{"rule.nested": ".claude/rules/api.md/inner.md", "context.file": ".claude/rules/context-file.md"},
			notKept: []string{"context.file"},
		},
		{
			name: "same name from two providers is unambiguous, different names are not",
			entities: []namedEntity{
				{id: "context.same", hint: copilotFile("same.instructions.md"), ext: canonical.Extensions{
					string(canonical.TargetKiro): {"stemma.steeringFile": "same.md"}}},
				{id: "context.differ", hint: copilotFile("one.instructions.md"), ext: canonical.Extensions{
					string(canonical.TargetKiro): {"stemma.steeringFile": "two.md"}}},
			},
			want:    map[string]string{"context.same": ".claude/rules/same.md", "context.differ": ".claude/rules/context-differ.md"},
			notKept: []string{"context.differ"},
		},
		{
			name: "cross-kind names in different namespaces do not collide",
			entities: []namedEntity{
				{id: "agent.bot", hint: [3]string{string(canonical.TargetCopilot), "stemma.sourceFile", "review.md"}},
				{id: "context.doc", hint: copilotFile("review.instructions.md")},
				{id: "skill.sk", hint: copilotSkill("review")},
			},
			want: map[string]string{
				"agent.bot": ".claude/agents/review.md", "context.doc": ".claude/rules/review.md",
				"skill.sk": ".claude/skills/review/SKILL.md",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := namingProject(tc.entities)
			out := compileBothOrders(t, p, compiler.CompileOptions{Target: canonical.TargetClaude})
			if got := destinationsByEntity(out); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("destinations = %v, want %v", got, tc.want)
			}
			if got := notKept(out); !reflect.DeepEqual(got, tc.notKept) {
				t.Fatalf("%s for %v, want %v", diagnostics.SourceNameNotKept, got, tc.notKept)
			}
			assertSkillNamesMatchDirectories(t, out)
		})
	}
}

func assertSkillNamesMatchDirectories(t *testing.T, out compiler.CompileResult) {
	t.Helper()
	for _, f := range out.Files {
		if path.Base(f.Path) != "SKILL.md" {
			continue
		}
		doc := parser.Parse(f.Path, f.Content)
		name, _ := doc.FrontMatter.String("name")
		if name != path.Base(path.Dir(f.Path)) {
			t.Fatalf("%s declares name %q", f.Path, name)
		}
	}
	for _, m := range out.Mappings {
		if m.EntityType != canonical.EntitySkill || m.Outcome != adapters.OutcomeExact {
			continue
		}
		if strings.Contains(m.Explanation, "not reused") {
			t.Fatalf("a skill whose source name changed is reported exact: %+v", m)
		}
	}
}

// Untrusted recorded names are validated and fall back to the canonical ID.
func TestUnsafeOrInvalidSourceNamesFallBack(t *testing.T) {
	files := []any{
		"../x.instructions.md", "a/../x.instructions.md", "./x.instructions.md", "/x.instructions.md",
		"x:y.instructions.md", "C:x.instructions.md", "~x.instructions.md", `a\x.instructions.md`,
		"x\x00.instructions.md", "my rules.instructions.md", ".hidden.instructions.md",
		"-x.instructions.md", "x..instructions.md", "CON.instructions.md", "con.txt.instructions.md",
		"AGENTS.instructions.md", "claude.instructions.md", "x.md", ".instructions.md",
		strings.Repeat("a", 65) + ".instructions.md", "café.instructions.md", 42, nil, true,
	}
	skills := []any{
		"Review", "re--view", "-review", "review-", "re_view", "re.view", strings.Repeat("a", 65),
		"aux", "a/b", "..", ".", "", "rev iew", 7,
	}
	for i, v := range append(files, skills...) {
		t.Run(fmt.Sprintf("%d_%v", i, v), func(t *testing.T) {
			id, key := "context.doc", "stemma.instructionsFile"
			want := ".claude/rules/context-doc.md"
			if i >= len(files) {
				id, key, want = "skill.doc", "stemma.sourceDir", ".claude/skills/skill-doc/SKILL.md"
			}
			p := namingProject([]namedEntity{{id: id, ext: canonical.Extensions{
				string(canonical.TargetCopilot): {key: v}}}})
			out := compileBothOrders(t, p, compiler.CompileOptions{Target: canonical.TargetClaude})
			if got := destinationsByEntity(out)[id]; got != want {
				t.Fatalf("destination = %q, want %q", got, want)
			}
			if got := notKept(out); !reflect.DeepEqual(got, []string{id}) {
				t.Fatalf("%s = %v", diagnostics.SourceNameNotKept, got)
			}
			assertSkillNamesMatchDirectories(t, out)
		})
	}
	// Valid limits are accepted.
	p := namingProject([]namedEntity{
		{id: "context.long", hint: copilotFile(strings.Repeat("A", 64) + ".instructions.md")},
		{id: "skill.long", hint: [3]string{string(canonical.TargetCopilot), "stemma.sourceDir", strings.Repeat("a", 64)}},
	})
	out := compileBothOrders(t, p, compiler.CompileOptions{Target: canonical.TargetClaude})
	want := map[string]string{
		"context.long": ".claude/rules/" + strings.Repeat("A", 64) + ".md",
		"skill.long":   ".claude/skills/" + strings.Repeat("a", 64) + "/SKILL.md",
	}
	if got := destinationsByEntity(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("destinations = %v", got)
	}
}

func TestProfileDestinationsKeepPrecedenceOverSourceNames(t *testing.T) {
	p := namingProject([]namedEntity{
		{id: "context.pinned-file", hint: copilotFile("python.instructions.md")},
		{id: "context.pinned-dir", hint: copilotFile("go.instructions.md")},
		{id: "skill.pinned", hint: [3]string{string(canonical.TargetCopilot), "stemma.sourceDir", "review"}},
		// Its source name equals another entity's pinned file: the pin wins.
		{id: "context.loser", hint: copilotFile("custom.instructions.md")},
	})
	profile := profiles.Default(canonical.TargetClaude)
	profile.Overrides["context.pinned-file"] = profiles.Override{Filename: "custom.md"}
	profile.Overrides["context.pinned-dir"] = profiles.Override{Directory: ".claude/rules/lang"}
	profile.Overrides["skill.pinned"] = profiles.Override{Directory: ".claude/skills/pinned"}
	out := compileBothOrders(t, p, compiler.CompileOptions{Target: canonical.TargetClaude, Profile: profile})
	want := map[string]string{
		"context.pinned-file": ".claude/rules/custom.md",
		"context.pinned-dir":  ".claude/rules/lang/go.md",
		"skill.pinned":        ".claude/skills/pinned/SKILL.md",
		"context.loser":       ".claude/rules/context-loser.md",
	}
	if got := destinationsByEntity(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("destinations = %v, want %v", got, want)
	}
	if got := notKept(out); !reflect.DeepEqual(got, []string{"context.loser"}) {
		t.Fatalf("%s = %v", diagnostics.SourceNameNotKept, got)
	}
	assertSkillNamesMatchDirectories(t, out)
}

// The shared policy applies to every exporter, with each target's layout.
func TestSourceNamesAcrossTargets(t *testing.T) {
	claude, copilot, kiro := string(canonical.TargetClaude), string(canonical.TargetCopilot), string(canonical.TargetKiro)
	entities := []namedEntity{
		{id: "rule.python-style", hint: [3]string{claude, "stemma.ruleFile", "backend/python.md"}},
		{id: "skill.review-code", hint: [3]string{claude, "stemma.sourceDir", "review"}},
		{id: "agent.code-reviewer", hint: [3]string{claude, "stemma.sourceFile", "reviewer.md"}},
		{id: "procedure.cut-release", hint: [3]string{copilot, "stemma.promptFile", "nested/release.prompt.md"}},
		{id: "skill.from-kiro", hint: [3]string{kiro, "stemma.sourceDir", "audit"}},
	}
	onDemand := canonical.ContextDocument{ID: "context.incident-response", Title: "Incident", Content: "Respond.",
		Kind: canonical.KindOther, Audience: canonical.AudienceAgent,
		Activation: canonical.OnDemand("when paged", "incident-response")}
	onDemand.Extensions.Set(kiro, "stemma.steeringFile", "incident.md")
	want := map[canonical.TargetFormat]map[string]string{
		canonical.TargetCopilot: {
			"rule.python-style": ".github/instructions/python.instructions.md", "skill.review-code": ".github/skills/review/SKILL.md",
			"agent.code-reviewer": ".github/agents/reviewer.md", "procedure.cut-release": ".github/prompts/nested/release.prompt.md",
			"skill.from-kiro": ".github/skills/audit/SKILL.md", "context.incident-response": ".github/prompts/incident.prompt.md",
		},
		canonical.TargetClaude: {
			"rule.python-style": ".claude/rules/backend/python.md", "skill.review-code": ".claude/skills/review/SKILL.md",
			"agent.code-reviewer": ".claude/agents/reviewer.md", "procedure.cut-release": ".claude/skills/release/SKILL.md",
			"skill.from-kiro": ".claude/skills/audit/SKILL.md", "context.incident-response": ".claude/skills/incident/SKILL.md",
		},
		canonical.TargetCodex: {
			"skill.review-code": ".agents/skills/review/SKILL.md", "procedure.cut-release": ".agents/skills/release/SKILL.md",
			"skill.from-kiro": ".agents/skills/audit/SKILL.md", "context.incident-response": ".agents/skills/incident/SKILL.md",
		},
		canonical.TargetKiro: {
			"rule.python-style": ".kiro/steering/python.md", "skill.review-code": ".kiro/skills/review/SKILL.md",
			"agent.code-reviewer": ".kiro/agents/reviewer.json", "procedure.cut-release": ".kiro/skills/release/SKILL.md",
			"skill.from-kiro": ".kiro/skills/audit/SKILL.md", "context.incident-response": ".kiro/steering/incident.md",
		},
	}
	for _, target := range capabilities.AvailableTargets() {
		t.Run(string(target), func(t *testing.T) {
			p := namingProject(entities)
			p.ContextDocuments = append(p.ContextDocuments, onDemand)
			out := compileBothOrders(t, p, compiler.CompileOptions{Target: target})
			got := destinationsByEntity(out)
			for id, dest := range want[target] {
				if got[id] != dest {
					t.Errorf("%s -> %q, want %q", id, got[id], dest)
				}
			}
			if len(notKept(out)) != 0 {
				t.Errorf("unexpected fallbacks: %v", notKept(out))
			}
			assertSkillNamesMatchDirectories(t, out)
		})
	}
}

// Recorded names are untrusted and may collide in any combination. For every
// target and any mix of other providers' names, compilation must never block,
// write two entities to one portable destination, or depend on entity order.
func TestSourceNamesNeverCollideProperty(t *testing.T) {
	pool := []string{"review", "Review", "python", "PYTHON", "rule-a", "context-a", "skill-a",
		"procedure-a", "agent-a", "a", "api", "nested/api", "bad name", "../x", "con", "claude", strings.Repeat("z", 64)}
	kinds := []canonical.EntityType{canonical.EntityContext, canonical.EntityRule, canonical.EntityProcedure,
		canonical.EntitySkill, canonical.EntityAgent}
	for _, target := range capabilities.AvailableTargets() {
		t.Run(string(target), func(t *testing.T) {
			var sources []capabilities.Capabilities
			for _, c := range capabilities.All() {
				if c.Available && c.Target != target {
					sources = append(sources, c)
				}
			}
			property := func(seed int64) bool {
				rng := rand.New(rand.NewSource(seed))
				var entities []namedEntity
				for i := 0; i < 12; i++ {
					kind := kinds[rng.Intn(len(kinds))]
					e := namedEntity{id: fmt.Sprintf("%s.%s", kind, []string{"a", "b", "c", "d", "e", "f"}[i%6])}
					if i >= 6 {
						e.id += "-2"
					}
					for n := rng.Intn(3); n > 0; n-- {
						src := sources[rng.Intn(len(sources))]
						h := src.Naming.SourceNames[rng.Intn(len(src.Naming.SourceNames))]
						e.hint = [3]string{string(src.Target), h.Key, pool[rng.Intn(len(pool))] + h.Suffix}
					}
					entities = append(entities, e)
				}
				p := namingProject(entities)
				out, err := compiler.Compile(context.Background(), p, compiler.CompileOptions{Target: target})
				if err != nil || diagnostics.HasBlocking(out.Diagnostics) {
					var inv *compiler.InvariantError
					t.Logf("seed %d: %v %v %+v", seed, err, errors.As(err, &inv), out.Diagnostics)
					return false
				}
				assertProjectionInvariants(t, p, out)
				assertSkillNamesMatchDirectories(t, out)
				keys := map[string]string{}
				for _, f := range out.Files {
					k := workspace.PortablePathKey(f.Path)
					if other, dup := keys[k]; dup {
						t.Logf("seed %d: %s and %s share a destination", seed, f.Path, other)
						return false
					}
					keys[k] = f.Path
				}
				for _, f := range out.Files {
					for dir := path.Dir(f.Path); dir != "."; dir = path.Dir(dir) {
						if other, clash := keys[workspace.PortablePathKey(dir)]; clash {
							t.Logf("seed %d: %s occupies a parent of %s", seed, other, f.Path)
							return false
						}
					}
				}
				again, err := compiler.Compile(context.Background(), reversed(p), compiler.CompileOptions{Target: target})
				return err == nil && sameOutput(out, again)
			}
			if err := quick.Check(property, &quick.Config{MaxCount: 200, Rand: rand.New(rand.NewSource(61))}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Nested instructions, prompts and steering documents with the same base name
// come back to their own subdirectories on a same-provider round trip.
func TestNestedDuplicateBaseNamesRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		format canonical.TargetFormat
		files  map[string]string
	}{
		{canonical.TargetCopilot, map[string]string{
			".github/instructions/a/python.instructions.md": "---\napplyTo: \"a/**\"\n---\n\n# A\n\nBlack.\n",
			".github/instructions/b/python.instructions.md": "---\napplyTo: \"b/**\"\n---\n\n# B\n\nRuff.\n",
			".github/prompts/a/release.prompt.md":           "---\nname: release-a\n---\n\n# A\n\nTag a.\n",
			".github/prompts/b/release.prompt.md":           "---\nname: release-b\n---\n\n# B\n\nTag b.\n",
		}},
		{canonical.TargetKiro, map[string]string{
			".kiro/steering/a/api.md": "---\ninclusion: always\n---\n\n# A\n\nOne.\n",
			".kiro/steering/b/api.md": "---\ninclusion: always\n---\n\n# B\n\nTwo.\n",
		}},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			_, res := importWorkspace(t, tc.format, tc.files)
			out, err := compiler.Compile(context.Background(), res.Project, compiler.CompileOptions{
				Target: tc.format, Originals: originalsOf(tc.files),
			})
			if err != nil {
				t.Fatalf("compile: %v %+v", err, out.Diagnostics)
			}
			if len(out.Files) != len(tc.files) {
				t.Fatalf("files = %v", filePaths(out))
			}
			for _, f := range out.Files {
				if !f.ReusedSource || string(f.Content) != tc.files[f.Path] {
					t.Fatalf("%s was not reproduced byte-for-byte", f.Path)
				}
			}
		})
	}
}

func originalsOf(files map[string]string) map[string]adapters.SourceFile {
	out := map[string]adapters.SourceFile{}
	for p, data := range files {
		out[p] = adapters.SourceFile{Path: p, Data: []byte(data), Hash: provenance.HashString(data)}
	}
	return out
}
