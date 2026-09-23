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
		if f.PreservedOnDemandBy != "" && (f.Value == "" || f.Kind == ExtensionPresentation) {
			t.Errorf("%s.%s: only a reported value can be preserved by an invocation mode", f.Provider, f.Key)
		}
		if i > 0 {
			prev := fields[i-1]
			if prev.Provider > f.Provider || (prev.Provider == f.Provider && prev.Key > f.Key) ||
				(prev.Provider == f.Provider && prev.Key == f.Key && prev.Value >= f.Value) {
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
		value    any
		kind     ExtensionKind
	}{
		{kiro, "resources", []any{"file://src"}, ExtensionContext},
		{kiro, "allowedTools", nil, ExtensionSecurity},
		{kiro, "permissions", nil, ExtensionSecurity},
		{kiro, "toolsSettings", nil, ExtensionSecurity},
		{kiro, "mcpServers", nil, ExtensionSecurity},
		{kiro, "hooks", nil, ExtensionSecurity},
		{kiro, "includeMcpJson", nil, ExtensionSecurity},
		{kiro, "toolAliases", nil, ExtensionBehaviour},
		{kiro, "welcomeMessage", nil, ExtensionPresentation},
		// Kiro's inclusion mode depends on its value: always and fileMatch
		// are canonical activations, manual and auto both import as on-demand.
		{kiro, "inclusion", "always", ExtensionPresentation},
		{kiro, "inclusion", "fileMatch", ExtensionPresentation},
		{kiro, "inclusion", "manual", ExtensionBehaviour},
		{kiro, "inclusion", "auto", ExtensionBehaviour},
		{claude, "permissionMode", nil, ExtensionSecurity},
		{claude, "disallowedTools", nil, ExtensionSecurity},
		{claude, "skills", nil, ExtensionContext},
		{claude, "maxTurns", nil, ExtensionBehaviour},
		{claude, "color", nil, ExtensionPresentation},
		{copilot, "excludeAgent", nil, ExtensionBehaviour},
		{copilot, "tools", nil, ExtensionSecurity},
		{copilot, "model", nil, ExtensionBehaviour},
		{canonical.TargetCodex, "license", nil, ExtensionPresentation},
	}
	for _, tc := range cases {
		f, ok := ClassifyExtension(tc.provider, tc.key, tc.value)
		if !ok || f.Kind != tc.kind {
			t.Errorf("%s.%s=%v: %q (classified %v), want %q", tc.provider, tc.key, tc.value, f.Kind, ok, tc.kind)
		}
	}
	// Keys are provider-specific: Kiro's allowedTools says nothing about Claude.
	// So is a value the documentation does not list for a value-dependent key.
	for _, tc := range []struct {
		provider canonical.TargetFormat
		key      string
		value    any
	}{
		{claude, "allowedTools", nil}, {kiro, "excludeAgent", nil}, {copilot, "mode", "agent"},
		{kiro, "stemma.sourceFile", "a.json"}, {kiro, "inclusion", "sometimes"}, {kiro, "inclusion", true},
	} {
		if f, ok := ClassifyExtension(tc.provider, tc.key, tc.value); ok {
			t.Errorf("%s.%s=%v must be unclassified, got %+v", tc.provider, tc.key, tc.value, f)
		}
	}
	if UnclassifiedExtensionKind != ExtensionBehaviour {
		t.Error("unclassified extension keys must default to behaviour, so their loss is never silent")
	}
}

func TestExtensionPreservedOnlyByMatchingOnDemandInvocation(t *testing.T) {
	manual, _ := ClassifyExtension(canonical.TargetKiro, "inclusion", "manual")
	auto, _ := ClassifyExtension(canonical.TargetKiro, "inclusion", "auto")
	resources, _ := ClassifyExtension(canonical.TargetKiro, "resources", nil)
	onDemand, always := canonical.ActivationOnDemand, canonical.ActivationAlways
	cases := []struct {
		field      ExtensionField
		target     canonical.TargetFormat
		activation canonical.ActivationType
		want       bool
	}{
		// Copilot delivers on-demand context as prompt files, which only a
		// person runs; Claude and Codex as skills the agent may load itself.
		{manual, canonical.TargetCopilot, onDemand, true},
		{manual, canonical.TargetClaude, onDemand, false},
		{manual, canonical.TargetCodex, onDemand, false},
		{manual, canonical.TargetKiro, onDemand, true},
		{auto, canonical.TargetCopilot, onDemand, false},
		{auto, canonical.TargetClaude, onDemand, true},
		{auto, canonical.TargetCodex, onDemand, true},
		{auto, canonical.TargetKiro, onDemand, true},
		// A profile that turns the entity always-on drops either mode.
		{manual, canonical.TargetCopilot, always, false},
		{auto, canonical.TargetClaude, always, false},
		{resources, canonical.TargetClaude, onDemand, false},
	}
	for _, tc := range cases {
		if got := ExtensionPreserved(tc.field, MustFor(tc.target), tc.activation); got != tc.want {
			t.Errorf("%s=%s on %s (%s): preserved = %v, want %v",
				tc.field.Key, tc.field.Value, tc.target, tc.activation, got, tc.want)
		}
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
	if f.PreservedOnDemandBy != "" {
		basis += "; kept by targets whose on-demand delivery is `" + string(f.PreservedOnDemandBy) + "`"
	}
	key := "`" + f.Key + "`"
	if f.Value != "" {
		key = "`" + f.Key + ": " + f.Value + "`"
	}
	return fmt.Sprintf("| `%s` | %s | %s | %s | %s |", f.Provider, key, f.Kind, f.Meaning, basis)
}
