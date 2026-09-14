package compiler_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/canonical"
)

func TestGoldenInventoryRejectsUncoveredFixtures(t *testing.T) {
	copilot := canonical.TargetCopilot
	base := goldenCase{"claude/example", []canonical.TargetFormat{copilot}, false}
	for _, tc := range []struct {
		name    string
		cases   []goldenCase
		extra   string
		missing bool
		want    string
	}{
		{name: "registered", cases: []goldenCase{base}},
		{name: "unregistered case", cases: []goldenCase{base}, extra: "claude/new/input/example.md", want: "unregistered golden case"},
		{name: "deleted case", cases: []goldenCase{base}, missing: true, want: "golden case claude/example"},
		{name: "unselected target", cases: []goldenCase{base}, extra: "claude/example/expected-kiro-mappings.json", want: "undeclared snapshot/profile"},
		{name: "unselected storage", cases: []goldenCase{base}, extra: "claude/example/expected-project/project.json", want: "undeclared snapshot/profile"},
		{name: "unused profile", cases: []goldenCase{base}, extra: "claude/example/profile-kiro.json", want: "undeclared snapshot/profile"},
		{name: "empty targets", cases: []goldenCase{{name: base.name}}, want: "has no targets"},
		{name: "duplicate case", cases: []goldenCase{base, base}, want: "duplicate golden case"},
		{name: "duplicate target", cases: []goldenCase{{base.name, []canonical.TargetFormat{copilot, copilot}, false}}, want: "repeats target"},
		{name: "unsupported target", cases: []goldenCase{{base.name, []canonical.TargetFormat{"unknown"}, false}}, want: "unsupported target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, provider := range []string{"canonical", "claude", "codex", "copilot", "kiro"} {
				if err := os.MkdirAll(filepath.Join(root, provider), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if !tc.missing {
				if err := os.MkdirAll(filepath.Join(root, base.name), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.extra != "" {
				path := filepath.Join(root, tc.extra)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("[]\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			err := validateGoldenInventory(root, tc.cases)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}
