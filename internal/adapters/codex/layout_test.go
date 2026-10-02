package codex

import (
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
)

// TestChainSizeFollowsCodexBudget checks the byte arithmetic of the default
// project_doc_max_bytes budget (32 KiB): Codex skips empty files, truncates
// the file that crosses the budget and loads nothing after it. The warning is
// reported only on the file where a chain first exceeds the budget.
func TestChainSizeFollowsCodexBudget(t *testing.T) {
	const limit = DefaultProjectDocMaxBytes
	fill := func(n int) string { return strings.Repeat("x", n) }
	cases := []struct {
		name    string
		files   map[string]string
		shadow  []string
		warnAt  []string
		dirList []string
	}{
		{name: "exactly at the limit", files: map[string]string{"AGENTS.md": fill(limit)}},
		{name: "one byte over", files: map[string]string{"AGENTS.md": fill(limit + 1)},
			warnAt: []string{"AGENTS.md"}},
		{name: "root over the limit is reported once",
			files:  map[string]string{"AGENTS.md": fill(limit + 1), "a/AGENTS.md": fill(10), "a/b/AGENTS.md": fill(10)},
			warnAt: []string{"AGENTS.md"}},
		{name: "a nested file crosses the combined limit",
			files:  map[string]string{"AGENTS.md": fill(limit / 2), "a/AGENTS.md": fill(limit / 2), "a/b/AGENTS.md": fill(1), "c/AGENTS.md": fill(1)},
			warnAt: []string{"a/b/AGENTS.md"}},
		{name: "a file after an exactly full budget is not loaded",
			files:  map[string]string{"AGENTS.md": fill(limit), "a/AGENTS.md": fill(1)},
			warnAt: []string{"a/AGENTS.md"}},
		{name: "an empty file is skipped, not counted",
			files: map[string]string{"AGENTS.md": fill(limit), "a/AGENTS.md": " \n\t\n"}},
		{name: "the override is the file Codex reads",
			files:  map[string]string{"AGENTS.md": fill(10), "AGENTS.override.md": fill(limit + 1)},
			warnAt: []string{"AGENTS.override.md"}},
		{name: "a shadowed file is not read",
			files:  map[string]string{"AGENTS.md": fill(limit + 1), "AGENTS.override.md": fill(10)},
			shadow: []string{"AGENTS.md"}},
		{name: "a shadowed file without its override is not counted",
			files:  map[string]string{"AGENTS.md": fill(limit + 1)},
			shadow: []string{"AGENTS.md"}},
		{name: "siblings are separate chains",
			files: map[string]string{"a/AGENTS.md": fill(limit), "b/AGENTS.md": fill(limit)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p canonical.Project
			for _, s := range tc.shadow {
				p.Extensions.Set(string(canonical.TargetCodex), shadowedKeyPrefix+s, joinDir(instructionsDir(s), OverrideFile))
			}
			b := adapters.NewBuilder(canonical.TargetCodex, adapters.ExportInput{Project: p})
			dirs := []string{""}
			for path, content := range tc.files {
				b.Emit(path, content, nil)
				dirs = append(dirs, instructionsDir(path))
			}
			newLayout(p).checkChainSize(b, dirs)
			var got []string
			for _, d := range b.Result().Diagnostics {
				if d.Code != diagnostics.InstructionChainTooLarge {
					continue
				}
				if d.Severity != diagnostics.SeverityWarning || d.Blocking {
					t.Errorf("unexpected severity: %+v", d)
				}
				got = append(got, d.Path)
			}
			if strings.Join(got, ",") != strings.Join(tc.warnAt, ",") {
				t.Errorf("warnings at %v, want %v", got, tc.warnAt)
			}
		})
	}
}

func TestLayoutIgnoresUnsafeShadowMarkers(t *testing.T) {
	var p canonical.Project
	for _, key := range []string{"../AGENTS.md", "/AGENTS.md", "a/../AGENTS.md", "docs/README.md", "a\\AGENTS.md"} {
		p.Extensions.Set(string(canonical.TargetCodex), shadowedKeyPrefix+key, "x")
	}
	l := newLayout(p)
	if len(l.shadowed) != 0 || len(l.overrideDirs) != 0 {
		t.Fatalf("unsafe markers were accepted: %+v", l)
	}
	if got := l.file(""); got != RootFile {
		t.Errorf("root file = %q", got)
	}
}
