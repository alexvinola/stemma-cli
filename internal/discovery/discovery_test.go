package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/workspace"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		path   string
		format canonical.TargetFormat
		role   Role
	}{
		{".github/copilot-instructions.md", canonical.TargetCopilot, RoleRootInstructions},
		{".github/instructions/api.instructions.md", canonical.TargetCopilot, RoleScopedInstructions},
		{".github/instructions/nested/api.instructions.md", canonical.TargetCopilot, RoleScopedInstructions},
		{".github/prompts/release.prompt.md", canonical.TargetCopilot, RolePrompt},
		{".github/skills/x/SKILL.md", canonical.TargetCopilot, RoleSkill},
		{".github/agents/x.md", canonical.TargetCopilot, RoleAgent},
		{"CLAUDE.md", canonical.TargetClaude, RoleRootInstructions},
		{".claude/CLAUDE.md", canonical.TargetClaude, RoleRootInstructions},
		{".claude/rules/a.md", canonical.TargetClaude, RoleRule},
		{".claude/rules/nested/a.md", canonical.TargetClaude, RoleRule},
		{".claude/skills/x/SKILL.md", canonical.TargetClaude, RoleSkill},
		{".claude/agents/x.md", canonical.TargetClaude, RoleAgent},
		{"AGENTS.md", canonical.TargetCodex, RoleRootInstructions},
		{"src/api/AGENTS.md", canonical.TargetCodex, RoleNestedInstructions},
		{"AGENTS.override.md", canonical.TargetCodex, RoleOverride},
		{".agents/skills/x/SKILL.md", canonical.TargetCodex, RoleSkill},
		{".kiro/steering/product.md", canonical.TargetKiro, RoleSteering},
		{".kiro/skills/x/SKILL.md", canonical.TargetKiro, RoleSkill},
		{".kiro/agents/x.json", canonical.TargetKiro, RoleAgent},
	}
	for _, c := range cases {
		format, role, ok := Classify(c.path)
		if !ok {
			t.Errorf("Classify(%q) did not match", c.path)
			continue
		}
		if format != c.format || role != c.role {
			t.Errorf("Classify(%q) = %s/%s, want %s/%s", c.path, format, role, c.format, c.role)
		}
	}
}

func TestClassifyIgnoresSourceCode(t *testing.T) {
	for _, p := range []string{
		"main.go", "src/api/handler.go", "package.json", "README.md",
		"docs/architecture.md", ".github/workflows/ci.yml", "src/CLAUDE.md.bak",
	} {
		if _, _, ok := Classify(p); ok {
			t.Errorf("Classify(%q) matched; Stemma must never read source files", p)
		}
	}
}

func TestScanDetectsAndSorts(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("CLAUDE.md", "x")
	write(".claude/rules/z.md", "x")
	write(".claude/rules/a.md", "x")
	write("AGENTS.md", "x")
	write("main.go", "package main")

	ws, err := workspace.Open(root, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	res, err := Scan(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Detections) != 2 {
		t.Fatalf("detections = %+v", res.Detections)
	}
	// Deterministic order: claude before codex.
	if res.Detections[0].Format != canonical.TargetClaude || res.Detections[1].Format != canonical.TargetCodex {
		t.Errorf("detection order = %s, %s", res.Detections[0].Format, res.Detections[1].Format)
	}
	claude := res.Files(canonical.TargetClaude)
	for i := 1; i < len(claude); i++ {
		if claude[i-1].Path > claude[i].Path {
			t.Errorf("files are not sorted: %+v", claude)
		}
	}
	if res.Detections[0].Confidence != ConfidenceHigh {
		t.Errorf("confidence = %s", res.Detections[0].Confidence)
	}
	for _, d := range res.Detections {
		for _, f := range d.Files {
			if f.Path == "main.go" {
				t.Error("scan reported a source file")
			}
		}
	}
}

func TestScanOnEmptyRepository(t *testing.T) {
	ws, err := workspace.Open(t.TempDir(), workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	res, err := Scan(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Detections) != 0 {
		t.Fatalf("detections = %+v", res.Detections)
	}
	if len(res.Diagnostics) == 0 {
		t.Error("expected an informational diagnostic")
	}
}

func TestSkillName(t *testing.T) {
	if got := SkillName(".claude/skills/release/SKILL.md"); got != "release" {
		t.Errorf("SkillName = %q", got)
	}
}

func FuzzClassify(f *testing.F) {
	f.Add("CLAUDE.md")
	f.Add(".github/instructions/a.instructions.md")
	f.Add("../escape")
	f.Add("")
	f.Fuzz(func(t *testing.T, path string) {
		format, role, ok := Classify(path)
		if !ok {
			return
		}
		if format == "" || role == "" {
			t.Fatalf("Classify(%q) matched with empty format/role", path)
		}
		if !canonical.KnownTarget(format) {
			t.Fatalf("Classify(%q) returned unknown format %q", path, format)
		}
		// A matched path must be safe to open.
		if _, err := workspace.NormalizeRel(path); err != nil {
			t.Fatalf("Classify(%q) matched an unsafe path: %v", path, err)
		}
	})
}
