package capabilities

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/canonical"
)

func TestExtensionClassificationTableIsWellFormed(t *testing.T) {
	fields := ExtensionFields()
	if len(fields) == 0 {
		t.Fatal("the extension classification table is empty")
	}
	for i, f := range fields {
		if !Available(f.Provider) {
			t.Errorf("%s.%s: provider is not an implemented target", f.Provider, f.Key)
		}
		if f.Key == "" || f.Meaning == "" || !KnownExtensionKind(f.Kind) {
			t.Errorf("incomplete classification %+v", f)
		}
		documented := f.Source.URL != "" && f.Source.Title != "" && f.Source.LastVerified != ""
		if !documented && f.Mirrors == "" {
			t.Errorf("%s.%s must cite documentation or name the canonical field it mirrors", f.Provider, f.Key)
		}
		if f.Source != (Source{}) && !documented {
			t.Errorf("%s.%s: incomplete documentation source %+v", f.Provider, f.Key, f.Source)
		}
		if f.Mirrors != "" && f.Kind != ExtensionPresentation {
			// A mirror duplicates a canonical field that every target
			// projects itself, so losing the copy loses nothing else.
			t.Errorf("%s.%s mirrors %s but is classified %s", f.Provider, f.Key, f.Mirrors, f.Kind)
		}
		if i > 0 {
			prev := fields[i-1]
			if prev.Provider > f.Provider || (prev.Provider == f.Provider && prev.Key >= f.Key) {
				t.Errorf("ExtensionFields is not sorted at %s.%s", f.Provider, f.Key)
			}
		}
	}
}

func TestExtensionClassification(t *testing.T) {
	kiro, claude, copilot := canonical.TargetKiro, canonical.TargetClaude, canonical.TargetCopilot
	cases := []struct {
		provider canonical.TargetFormat
		key      string
		kind     ExtensionKind
	}{
		{kiro, "resources", ExtensionContext},
		{kiro, "allowedTools", ExtensionSecurity},
		{kiro, "permissions", ExtensionSecurity},
		{kiro, "toolsSettings", ExtensionSecurity},
		{kiro, "mcpServers", ExtensionSecurity},
		{kiro, "hooks", ExtensionSecurity},
		{kiro, "includeMcpJson", ExtensionSecurity},
		{kiro, "toolAliases", ExtensionBehaviour},
		{kiro, "welcomeMessage", ExtensionPresentation},
		{kiro, "inclusion", ExtensionPresentation},
		{claude, "permissionMode", ExtensionSecurity},
		{claude, "disallowedTools", ExtensionSecurity},
		{claude, "skills", ExtensionContext},
		{claude, "maxTurns", ExtensionBehaviour},
		{claude, "color", ExtensionPresentation},
		{copilot, "excludeAgent", ExtensionBehaviour},
		{copilot, "tools", ExtensionSecurity},
		{copilot, "model", ExtensionBehaviour},
		{canonical.TargetCodex, "license", ExtensionPresentation},
	}
	for _, tc := range cases {
		f, ok := ClassifyExtension(tc.provider, tc.key)
		if !ok || f.Kind != tc.kind {
			t.Errorf("%s.%s = %q (classified %v), want %q", tc.provider, tc.key, f.Kind, ok, tc.kind)
		}
	}
	// Keys are provider-specific: Kiro's allowedTools says nothing about Claude.
	for _, tc := range []struct {
		provider canonical.TargetFormat
		key      string
	}{{claude, "allowedTools"}, {kiro, "excludeAgent"}, {copilot, "mode"}, {kiro, "stemma.sourceFile"}} {
		if f, ok := ClassifyExtension(tc.provider, tc.key); ok {
			t.Errorf("%s.%s must be unclassified, got %+v", tc.provider, tc.key, f)
		}
	}
	if UnclassifiedExtensionKind != ExtensionBehaviour {
		t.Error("unclassified extension keys must default to behaviour, so their loss is never silent")
	}
}

// TestExtensionClassificationIsDocumented keeps the published table in
// docs/provider-compatibility.md identical to the one the compiler uses.
func TestExtensionClassificationIsDocumented(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "provider-compatibility.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, found := strings.Cut(string(data), "\n## Provider extension classification\n")
	if !found {
		t.Fatal("docs/provider-compatibility.md has no provider extension classification section")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	documented := map[string]bool{}
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "| `") {
			documented[line] = true
		}
	}
	var missing []string
	for _, f := range ExtensionFields() {
		row := extensionDocRow(f)
		if !documented[row] {
			missing = append(missing, row)
		}
		delete(documented, row)
	}
	if len(missing) > 0 {
		t.Errorf("docs/provider-compatibility.md is missing these classification rows:\n%s",
			strings.Join(missing, "\n"))
	}
	for row := range documented {
		t.Errorf("docs/provider-compatibility.md documents a row the table does not have: %s", row)
	}
}

func extensionDocRow(f ExtensionField) string {
	basis := "mirrors canonical `" + f.Mirrors + "`"
	if f.Source.URL != "" {
		basis = "[" + f.Source.Title + "](" + f.Source.URL + ")"
		if f.Mirrors != "" {
			basis += "; mirrors canonical `" + f.Mirrors + "`"
		}
	}
	return fmt.Sprintf("| `%s` | `%s` | %s | %s | %s |", f.Provider, f.Key, f.Kind, f.Meaning, basis)
}
