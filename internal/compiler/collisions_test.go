package compiler_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"path"
	"reflect"
	"slices"
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
)

func collisionProject(title string) canonical.Project {
	p := canonical.NewProject("prj_collisions", "Destination collisions")
	for i, slug := range []string{"same", "same-2", strings.Repeat("a", 63) + "b", strings.Repeat("a", 63) + "c"} {
		for j, a := range []canonical.Activation{canonical.Always(), canonical.PathScoped([]string{fmt.Sprintf("src/scope%d/**", i)}, nil), canonical.OnDemand("when requested", title)} {
			suffix := slug
			if j > 0 && len(slug) < 60 {
				suffix += fmt.Sprintf("-activation%d", j)
			} else if j > 0 {
				continue
			}
			p.ContextDocuments = append(p.ContextDocuments, canonical.ContextDocument{ID: "context." + suffix, Title: title, Content: "Context " + suffix, Kind: canonical.KindOther, Audience: canonical.AudienceAgent, Activation: a})
			p.Rules = append(p.Rules, canonical.Rule{ID: "rule." + suffix, Title: title, Instruction: "Rule " + suffix, Enabled: true, Priority: canonical.PriorityShould, Activation: a})
		}
		p.Procedures = append(p.Procedures, canonical.Procedure{ID: "procedure." + slug, Name: title, Description: "Procedure", Content: "Procedure " + slug})
		p.Skills = append(p.Skills, canonical.Skill{ID: "skill." + slug, Name: title, Description: "Skill", Content: "Skill " + slug})
		p.Agents = append(p.Agents, canonical.Agent{ID: "agent." + slug, Name: title, Description: "Agent", Instructions: "Agent " + slug})
		p.Decisions = append(p.Decisions, canonical.Decision{ID: "decision." + slug, Title: title, Status: canonical.DecisionAccepted, Context: "Context", Decision: "Decision", Consequences: "Consequences", AgentConstraints: []string{"Constraint " + slug}})
	}
	return p
}

// Every entity must own its destination unless the adapter deliberately built
// a repository-wide or directory-scoped instructions aggregate in one Emit.
func TestNoUnintendedSharedDestinationsProperty(t *testing.T) {
	for _, target := range capabilities.AvailableTargets() {
		t.Run(string(target), func(t *testing.T) {
			property := func(seed uint64) bool {
				title := []string{"Shared title", "Shared-title", "!!!", "日本語", strings.Repeat("same", 30)}[seed%5]
				p := collisionProject(title)
				out, err := compiler.Compile(context.Background(), p, compiler.CompileOptions{Target: target})
				if err != nil || diagnostics.HasBlocking(out.Diagnostics) {
					t.Logf("compile: %v %+v", err, out.Diagnostics)
					return false
				}
				assertProjectionInvariants(t, p, out)
				owners := map[string][]adapters.ProjectionMapping{}
				for _, m := range out.Mappings {
					for _, dest := range m.Files {
						owners[dest] = append(owners[dest], m)
					}
				}
				for _, f := range out.Files {
					if len(owners[f.Path]) > 1 {
						aggregate := target == canonical.TargetCodex && path.Base(f.Path) == "AGENTS.md" || target == canonical.TargetClaude && f.Path == "CLAUDE.md" || target == canonical.TargetCopilot && f.Path == ".github/copilot-instructions.md"
						if !aggregate {
							t.Logf("unintended shared destination %s: %+v", f.Path, owners[f.Path])
							return false
						}
						for _, m := range owners[f.Path] {
							if m.EntityType == canonical.EntitySkill || m.EntityType == canonical.EntityProcedure {
								return false
							}
						}
					}
					for dir := path.Dir(f.Path); dir != "."; dir = path.Dir(dir) {
						if len(owners[dir]) > 0 {
							return false
						}
					}
					if path.Base(f.Path) == "SKILL.md" {
						doc := parser.Parse(f.Path, f.Content)
						if doc.FrontMatter == nil {
							return false
						}
						name, _ := doc.FrontMatter.String("name")
						if name != path.Base(path.Dir(f.Path)) || len(name) > 64 {
							t.Logf("skill metadata differs from directory: %s %s", name, f.Path)
							return false
						}
					}
				}
				// Shuffle all entity collections. Destination assignments and mapping
				// outcomes must not depend on who happens to be visited first.
				rng := rand.New(rand.NewSource(int64(seed)))
				rng.Shuffle(len(p.ContextDocuments), func(i, j int) {
					p.ContextDocuments[i], p.ContextDocuments[j] = p.ContextDocuments[j], p.ContextDocuments[i]
				})
				rng.Shuffle(len(p.Rules), func(i, j int) { p.Rules[i], p.Rules[j] = p.Rules[j], p.Rules[i] })
				slices.Reverse(p.Procedures)
				slices.Reverse(p.Skills)
				slices.Reverse(p.Agents)
				slices.Reverse(p.Decisions)
				again, err := compiler.Compile(context.Background(), p, compiler.CompileOptions{Target: target})
				if err != nil || !reflect.DeepEqual(out.Mappings, again.Mappings) {
					t.Logf("order changed mappings: %v", err)
					return false
				}
				for i, f := range out.Files {
					if f.Path != again.Files[i].Path || !reflect.DeepEqual(f.Entities, again.Files[i].Entities) {
						return false
					}
					if len(f.Entities) == 1 && f.Text != again.Files[i].Text {
						return false
					}
				}
				return true
			}
			if err := quick.Check(property, &quick.Config{MaxCount: 30, Rand: rand.New(rand.NewSource(2))}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDuplicateTitlesKeepIndependentScope(t *testing.T) {
	for _, target := range []canonical.TargetFormat{canonical.TargetClaude, canonical.TargetCopilot, canonical.TargetKiro} {
		p := canonical.NewProject("prj", "Scope regression")
		for i, title := range []string{"API conventions", "API-conventions"} {
			p.Rules = append(p.Rules, canonical.Rule{ID: fmt.Sprintf("rule.api-%d", i), Title: title, Instruction: fmt.Sprintf("Body %d", i), Priority: canonical.PriorityShould, Enabled: true, Activation: canonical.PathScoped([]string{fmt.Sprintf("src/%d/**", i)}, nil)})
		}
		out, err := compiler.Compile(context.Background(), p, compiler.CompileOptions{Target: target})
		if err != nil || len(out.Files) != 2 {
			t.Fatalf("%s: %v %+v", target, err, out)
		}
		key := map[canonical.TargetFormat]string{canonical.TargetClaude: "paths", canonical.TargetCopilot: "applyTo", canonical.TargetKiro: "fileMatchPattern"}[target]
		for i, f := range out.Files {
			doc := parser.Parse(f.Path, f.Content)
			if doc.FrontMatter == nil || strings.Count(f.Text, "---\n") != 2 {
				t.Fatalf("malformed output: %s", f.Text)
			}
			scope, ok := doc.FrontMatter.StringList(key)
			if !ok {
				if s, ok := doc.FrontMatter.String(key); ok {
					scope = []string{s}
				}
			}
			if !reflect.DeepEqual(scope, []string{fmt.Sprintf("src/%d/**", i)}) {
				t.Fatalf("scope lost in %s: %v", f.Path, scope)
			}
			if out.Mappings[i].Outcome != adapters.OutcomeExact {
				t.Fatalf("unexpected mapping: %+v", out.Mappings[i])
			}
		}
	}
}

func TestConflictingHintsAndPinsBlockAllOwners(t *testing.T) {
	for _, target := range capabilities.AvailableTargets() {
		for _, shape := range []string{"skill-hint", "profile-filename", "profile-directory", "root", "opaque", "file-directory", "file-hint", "agent-hint", "procedure-hint", "hint-fallback"} {
			t.Run(string(target)+"/"+shape, func(t *testing.T) {
				p := canonical.NewProject("prj", "Conflicts")
				p.Skills = []canonical.Skill{{ID: "skill.a", Name: "First", Description: "First", Content: "First body"}, {ID: "skill.b", Name: "Second", Description: "Second", Content: "Second body"}}
				profile := profiles.Default(target)
				ids := []string{"skill.a", "skill.b"}
				switch shape {
				case "skill-hint":
					for i := range p.Skills {
						p.Skills[i].Extensions.Set(string(target), "stemma.sourceDir", "shared")
					}
				case "profile-filename":
					for _, id := range ids {
						profile.Overrides[id] = profiles.Override{Directory: "custom", Filename: "shared.md"}
					}
				case "profile-directory":
					profile.Overrides[ids[0]] = profiles.Override{Directory: "custom"}
					profile.Overrides[ids[1]] = profiles.Override{Directory: "./custom/"}
				case "file-directory":
					profile.Overrides[ids[0]] = profiles.Override{Directory: "custom", Filename: "shared.md"}
					profile.Overrides[ids[1]] = profiles.Override{Directory: "custom/shared.md"}
				case "root":
					if target == canonical.TargetKiro {
						t.Skip("Kiro has no aggregate root")
					}
					p.Skills = p.Skills[:1]
					p.ContextDocuments = []canonical.ContextDocument{{ID: "context.root", Title: "Root", Content: "Root content", Audience: canonical.AudienceAgent, Kind: canonical.KindOther, Activation: canonical.Always()}}
					root := map[canonical.TargetFormat]string{canonical.TargetClaude: "CLAUDE.md", canonical.TargetCopilot: ".github/copilot-instructions.md", canonical.TargetCodex: "AGENTS.md"}[target]
					profile.Overrides["skill.a"] = profiles.Override{Directory: path.Dir(root), Filename: path.Base(root)}
					ids = []string{"skill.a", "context.root"}
				case "opaque":
					if target != canonical.TargetCodex {
						t.Skip("Codex emits standalone opaque overrides")
					}
					p.Skills = p.Skills[:1]
					p.OpaqueBlocks = []canonical.OpaqueBlock{{ID: "opaque.override", Provider: string(target), SourcePath: "AGENTS.override.md", Content: "Keep original", ReemitForRoundTrip: true}}
					profile.Overrides["skill.a"] = profiles.Override{Directory: ".", Filename: "AGENTS.override.md"}
					ids = []string{"skill.a", "opaque.override"}
				case "agent-hint":
					if target == canonical.TargetCodex {
						t.Skip("No native agents")
					}
					p.Skills = nil
					for _, id := range []string{"agent.a", "agent.b"} {
						a := canonical.Agent{ID: id, Name: id, Description: id, Instructions: id}
						a.Extensions.Set(string(target), "stemma.sourceFile", "same.md")
						p.Agents = append(p.Agents, a)
					}
					ids = []string{"agent.a", "agent.b"}
				case "procedure-hint":
					if target != canonical.TargetCopilot {
						t.Skip("Only Copilot has prompt files")
					}
					p.Skills = nil
					for _, id := range []string{"procedure.a", "procedure.b"} {
						v := canonical.Procedure{ID: id, Name: id, Description: id, Content: id}
						v.Extensions.Set(string(target), "stemma.promptFile", "same.prompt.md")
						p.Procedures = append(p.Procedures, v)
					}
					ids = []string{"procedure.a", "procedure.b"}
				case "file-hint", "hint-fallback":
					if target == canonical.TargetCodex {
						t.Skip("Codex scoped files are intentional aggregates")
					}
					p.Skills = nil
					key := map[canonical.TargetFormat]string{canonical.TargetClaude: "stemma.ruleFile", canonical.TargetCopilot: "stemma.instructionsFile", canonical.TargetKiro: "stemma.steeringFile"}[target]
					filename := "context-a.md"
					if target == canonical.TargetCopilot {
						filename = "context-a.instructions.md"
					}
					for i, id := range []string{"context.a", "context.b"} {
						d := canonical.ContextDocument{ID: id, Title: id, Content: id, Audience: canonical.AudienceAgent, Kind: canonical.KindOther, Activation: canonical.PathScoped([]string{fmt.Sprintf("src/%d/**", i)}, nil)}
						if shape == "file-hint" || i == 1 {
							d.Extensions.Set(string(target), key, filename)
						}
						p.ContextDocuments = append(p.ContextDocuments, d)
					}
					ids = []string{"context.a", "context.b"}
				}
				out, err := compiler.Compile(context.Background(), p, compiler.CompileOptions{Target: target, Profile: profile})
				if !errors.Is(err, compiler.ErrInvariant) || len(out.Files) != 0 {
					t.Fatalf("collision allowed: err=%v output=%+v", err, out)
				}
				for _, m := range out.Mappings {
					if !slices.Contains(ids, m.EntityID) || m.Outcome != adapters.OutcomeBlocked || len(m.Diagnostics) == 0 {
						t.Fatalf("unblocked mapping: %+v", m)
					}
				}
				if len(out.Mappings) != len(ids) {
					t.Fatal("missing mappings")
				}
				for _, d := range out.Diagnostics {
					if d.Code == diagnostics.InternalInvariant {
						profile.AcceptedDiagnostics = append(profile.AcceptedDiagnostics, d.Fingerprint)
					}
				}
				again, err := compiler.Compile(context.Background(), p, compiler.CompileOptions{Target: target, Profile: profile})
				if !errors.Is(err, compiler.ErrInvariant) || !diagnostics.HasBlocking(again.Diagnostics) {
					t.Fatal("invariant accepted away")
				}
			})
		}
	}
}
