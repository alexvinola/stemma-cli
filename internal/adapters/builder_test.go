package adapters

import (
	"reflect"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/provenance"
)

func TestBuilderRejectsDestinationCollisions(t *testing.T) {
	for _, paths := range [][3]string{
		{"same.md", "same.md", "./same.md"},
		{"rules/a.md", "rules/a.md/child.md", "rules/a.md/other.md"},
		{"Scope.md", "scope.md", "SCOPE.MD"},
		{"rules/Scope", "rules/scope/child.md", "RULES/SCOPE/other.md"},
	} {
		for mask := 0; mask < 8; mask++ {
			var first ExportResult
			for _, order := range [][3]int{{0, 1, 2}, {2, 1, 0}, {1, 0, 2}} {
				b := NewBuilder(canonical.TargetClaude, ExportInput{})
				for _, i := range order {
					id := []string{"rule.a", "rule.b", "rule.c"}[i]
					// Mix mappings before/after emission, as adapters do for aggregates.
					record := func() {
						b.Exact(id, canonical.EntityRule, Resolution{}, provenance.Provenance{}, []string{paths[i]}, "Exact before collision.")
					}
					if i == 0 {
						record()
					}
					if mask&(1<<i) != 0 {
						b.EmitReused(paths[i], []byte("original"), []string{id})
					} else {
						b.Emit(paths[i], "---\npaths: src/**\n---\nbody", []string{id})
					}
					if i != 0 {
						record()
					}
				}
				b.Emit("safe.md", "aggregate\n", []string{"context.a", "context.b"})
				out := b.Result()
				if len(out.Files) != 1 || out.Files[0].Path != "safe.md" || len(out.Files[0].Entities) != 2 {
					t.Fatalf("conflicting files survived: %+v", out.Files)
				}
				for _, m := range out.Mappings {
					if m.Outcome != OutcomeBlocked || len(m.Diagnostics) == 0 {
						t.Fatalf("unblocked mapping: %+v", m)
					}
				}
				for _, d := range out.Diagnostics {
					if d.Code != diagnostics.InternalInvariant || !d.Blocking {
						t.Fatalf("diagnostic: %+v", d)
					}
				}
				if len(first.Diagnostics) == 0 {
					first = out
				} else if !reflect.DeepEqual(first, out) {
					t.Fatalf("emission order changed collision result:\n%+v\n%+v", first, out)
				}
			}
		}
	}
}

func TestBuilderAllowsSiblingPathsAndSingleAggregates(t *testing.T) {
	b := NewBuilder(canonical.TargetCodex, ExportInput{})
	for _, p := range []string{"AGENTS.md", "src/AGENTS.md", "src/a.md", "src/a.md-extra/AGENTS.md", "RULES/a.md", "rules/b.md"} {
		b.Emit(p, "body", []string{"context.a", "rule.b"})
	}
	out := b.Result()
	if len(out.Files) != 6 || len(out.Diagnostics) != 0 {
		t.Fatalf("legitimate output blocked: %+v", out)
	}
}

func TestFileSlugPreservesCompleteIdentity(t *testing.T) {
	ids := []string{"context.foo", "rule.foo", "rule." + strings.Repeat("a", 63) + "b", "rule." + strings.Repeat("a", 63) + "c"}
	files, skills := map[string]bool{}, map[string]bool{}
	for _, id := range ids {
		f, s := FileSlug(id), SkillSlug(id)
		if files[f] || skills[s] {
			t.Fatalf("distinct ID %s collided: %s %s", id, f, s)
		}
		if len(s) > 64 || !canonical.ValidIDSlug(s) {
			t.Fatalf("invalid skill directory %q", s)
		}
		files[f], skills[s] = true, true
	}
	if FileSlug("rule.foo") != "rule-foo" {
		t.Fatal("fallback contract changed")
	}
}
