package adapters

import (
	"reflect"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/profiles"
)

func TestResolveNormalizesStoredAndOverriddenPatterns(t *testing.T) {
	raw := canonical.Activation{
		Type:    canonical.ActivationPathScoped,
		Include: []string{"src/**/*.{ts,tsx}"},
		Exclude: []string{"src/**/*.test.{ts,tsx}"},
	}
	want := canonical.Activation{
		Type:    canonical.ActivationPathScoped,
		Include: []string{"src/**/*.ts", "src/**/*.tsx"},
		Exclude: []string{"src/**/*.test.ts", "src/**/*.test.tsx"},
	}
	for _, mode := range []string{"stored", "filename-override", "activation-override"} {
		t.Run(mode, func(t *testing.T) {
			profile := profiles.Default(canonical.TargetCopilot)
			activation := raw
			if mode == "filename-override" {
				profile.Overrides["rule.api"] = profiles.Override{Filename: "api.instructions.md"}
			}
			if mode == "activation-override" {
				activation = canonical.Always()
				profile.Overrides["rule.api"] = profiles.Override{Activation: &raw}
			}
			res := Resolve("rule.api", true, activation, "Use types.", profile)
			if !reflect.DeepEqual(res.Activation, want) {
				t.Fatalf("activation = %+v, want %+v", res.Activation, want)
			}
			if raw.Include[0] != "src/**/*.{ts,tsx}" || raw.Exclude[0] != "src/**/*.test.{ts,tsx}" {
				t.Fatal("resolution mutated the input")
			}
		})
	}
}
