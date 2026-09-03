package capabilities

import (
	"testing"

	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/version"
)

func TestEveryDeclaredTargetHasARow(t *testing.T) {
	for _, target := range canonical.AllTargets() {
		c, ok := For(target)
		if !ok {
			t.Fatalf("no capability row for declared target %s", target)
		}
		if c.Baseline != version.CompatibilityBaseline {
			t.Errorf("%s: baseline = %q", target, c.Baseline)
		}
		if !c.Available {
			continue
		}
		if len(c.RecognizedPaths) == 0 {
			t.Errorf("%s: an available target must declare recognized paths", target)
		}
		if len(c.Sources) == 0 {
			t.Errorf("%s: an available target must cite its documentation source", target)
		}
		for _, s := range c.Sources {
			if s.URL == "" || s.LastVerified == "" || s.Title == "" {
				t.Errorf("%s: incomplete documentation source %+v", target, s)
			}
		}
	}
}

func TestCursorIsDeclaredButUnavailable(t *testing.T) {
	c, ok := For(canonical.TargetCursor)
	if !ok {
		t.Fatal("cursor must be a declared target identifier")
	}
	if c.Available {
		t.Fatal("cursor must not be presented as available until it is implemented")
	}
	if c.Notes == "" {
		t.Error("an unavailable target must explain itself")
	}
	if Available(canonical.TargetCursor) {
		t.Error("Available(cursor) must be false")
	}
}

func TestAvailableTargetsAreSorted(t *testing.T) {
	targets := AvailableTargets()
	for i := 1; i < len(targets); i++ {
		if targets[i-1] >= targets[i] {
			t.Fatalf("AvailableTargets is not sorted: %v", targets)
		}
	}
	if len(targets) != 4 {
		t.Fatalf("expected four implemented targets, got %v", targets)
	}
}

func TestExcludeGlobsAreUnsupportedEverywhere(t *testing.T) {
	// This encodes the current baseline: no supported provider documents a
	// negative pattern syntax. If a provider adds one, this test should be
	// updated together with the capability row and the exporter.
	for _, c := range All() {
		if c.Available && c.ExcludeGlobs {
			t.Errorf("%s claims exclude glob support; verify the documentation and update the exporter", c.Target)
		}
	}
}

func TestUnknownTargetHasNoRow(t *testing.T) {
	if _, ok := For(canonical.TargetFormat("made-up")); ok {
		t.Fatal("an unknown target must not have a capability row")
	}
	if c := MustFor(canonical.TargetFormat("made-up")); c.Available {
		t.Fatal("an unknown target must never be reported as available")
	}
}
