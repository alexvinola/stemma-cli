package discovery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/workspace"
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
		{"src/api/CLAUDE.md", canonical.TargetClaude, RoleNestedInstructions},
		{"a/b/c/CLAUDE.md", canonical.TargetClaude, RoleNestedInstructions},
		// Provider directories win over the recursive instruction patterns.
		{".claude/rules/CLAUDE.md", canonical.TargetClaude, RoleRule},
		{".claude/rules/nested/CLAUDE.md", canonical.TargetClaude, RoleRule},
		{".claude/agents/CLAUDE.md", canonical.TargetClaude, RoleAgent},
		{".kiro/steering/CLAUDE.md", canonical.TargetKiro, RoleSteering},
		{".kiro/steering/AGENTS.md", canonical.TargetKiro, RoleSteering},
		{".github/agents/AGENTS.md", canonical.TargetCopilot, RoleAgent},
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
		"CLAUDE.local.md", "src/CLAUDE.local.md", "src/claude.md",
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

func TestRegistryExtensions(t *testing.T) {
	// candidateExtension may only pre-filter safely while every registered
	// pattern ends in one of its extensions.
	for _, r := range registry {
		if !candidateExtension(r.pattern) {
			t.Errorf("pattern %q escapes the candidate extension pre-filter", r.pattern)
		}
	}
	for _, r := range sharedReaders {
		if !candidateExtension(r.pattern) {
			t.Errorf("shared pattern %q escapes the candidate extension pre-filter", r.pattern)
		}
	}
}

func TestAlsoReadBy(t *testing.T) {
	cases := []struct {
		path string
		want []canonical.TargetFormat
	}{
		{"AGENTS.md", []canonical.TargetFormat{canonical.TargetKiro}},
		{"src/api/AGENTS.md", []canonical.TargetFormat{canonical.TargetKiro}},
		{"AGENTS.override.md", nil},
		{"CLAUDE.md", nil},
		{".kiro/steering/AGENTS.md", nil}, // Kiro owns it: never a reader of itself
		{"main.go", nil},
	}
	for _, c := range cases {
		got := AlsoReadBy(c.path)
		if len(got) != len(c.want) {
			t.Errorf("AlsoReadBy(%q) = %v, want %v", c.path, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("AlsoReadBy(%q) = %v, want %v", c.path, got, c.want)
			}
		}
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Source files that sort before configuration must not spend the candidate
// budget: before issue #11 every regular file counted against MaxFiles, so the
// configuration below was never seen.
func TestScanFindsConfigurationAfterManyIrrelevantFiles(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"zz/api/CLAUDE.md": "x", "zz/web/AGENTS.md": "x"}
	for i := 0; i < 3000; i++ {
		files[fmt.Sprintf("aa/pkg%02d/file%04d.go", i%30, i)] = "package x"
	}
	writeTree(t, root, files)
	limits := workspace.DefaultLimits()
	limits.MaxFiles = 10 // far fewer than the irrelevant files, enough for the candidates
	ws, err := workspace.Open(root, limits)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Scan(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Complete || len(res.LimitsReached) != 0 {
		t.Fatalf("scan incomplete: %v", res.LimitsReached)
	}
	if got := res.Files(canonical.TargetClaude); len(got) != 1 || got[0].Path != "zz/api/CLAUDE.md" {
		t.Errorf("claude files = %+v", got)
	}
	if got := res.Files(canonical.TargetCodex); len(got) != 1 || got[0].Path != "zz/web/AGENTS.md" {
		t.Errorf("codex files = %+v", got)
	}
	if res.FilesVisited != 3002 {
		t.Errorf("FilesVisited = %d, want 3002", res.FilesVisited)
	}
	for _, d := range res.Diagnostics {
		if d.Code == diagnostics.FileLimitReached {
			t.Errorf("unexpected limit diagnostic: %+v", d)
		}
	}
}

func TestScanReportsIncompleteWalk(t *testing.T) {
	cases := []struct {
		name   string
		limits func(*workspace.Limits)
		limit  string
	}{
		{"entries", func(l *workspace.Limits) { l.MaxEntries = 50 }, workspace.LimitMaxEntries},
		{"candidates", func(l *workspace.Limits) { l.MaxFiles = 1 }, workspace.LimitMaxFiles},
		{"depth", func(l *workspace.Limits) { l.MaxDepth = 1 }, workspace.LimitMaxDepth},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{"AGENTS.md": "x", "zz/web/AGENTS.md": "x"}
			for i := 0; i < 100; i++ {
				files[fmt.Sprintf("aa/file%03d.go", i)] = "package x"
			}
			writeTree(t, root, files)
			limits := workspace.DefaultLimits()
			c.limits(&limits)
			ws, err := workspace.Open(root, limits)
			if err != nil {
				t.Fatal(err)
			}
			res, err := Scan(context.Background(), ws)
			if err != nil {
				t.Fatal(err)
			}
			if res.Complete || len(res.LimitsReached) != 1 || res.LimitsReached[0] != c.limit {
				t.Fatalf("complete = %v, limits = %v, want [%s]", res.Complete, res.LimitsReached, c.limit)
			}
			found := false
			for _, d := range res.Diagnostics {
				if d.Code == diagnostics.FileLimitReached && d.Severity == diagnostics.SeverityWarning {
					found = true
				}
			}
			if !found {
				t.Errorf("no STEMMA1002 warning: %+v", res.Diagnostics)
			}
		})
	}
}

// Kiro reads AGENTS.md, but Stemma models AGENTS.md with the Codex adapter
// only. A Kiro repository whose only file is AGENTS.md is therefore detected
// as Codex, and the overlap is reported rather than hidden.
func TestKiroRepositoryWithOnlyAgentsMD(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"AGENTS.md": "# Project\n\nUse tabs.\n"})
	ws, err := workspace.Open(root, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	res, err := Scan(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Formats(); len(got) != 1 || got[0] != canonical.TargetCodex {
		t.Fatalf("formats = %v, want [codex]", got)
	}
	if res.Files(canonical.TargetKiro) != nil {
		t.Errorf("kiro must not be detected from AGENTS.md alone: %+v", res.Files(canonical.TargetKiro))
	}
	m := res.Files(canonical.TargetCodex)
	if len(m) != 1 || len(m[0].AlsoReadBy) != 1 || m[0].AlsoReadBy[0] != canonical.TargetKiro {
		t.Fatalf("codex matches = %+v, want AGENTS.md also read by kiro", m)
	}
}

func TestSkillName(t *testing.T) {
	if got := SkillName(".claude/skills/release/SKILL.md"); got != "release" {
		t.Errorf("SkillName = %q", got)
	}
}

func FuzzClassify(f *testing.F) {
	f.Add("CLAUDE.md")
	f.Add("src/api/CLAUDE.md")
	f.Add("AGENTS.md")
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
		for _, reader := range AlsoReadBy(path) {
			if reader == format || !canonical.KnownTarget(reader) {
				t.Fatalf("AlsoReadBy(%q) = %v with owner %q", path, AlsoReadBy(path), format)
			}
		}
	})
}
