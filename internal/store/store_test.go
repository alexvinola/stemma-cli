package store

import (
	"context"
	"testing"

	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/manifest"
	"github.com/alexvinola/stemma/internal/profiles"
	"github.com/alexvinola/stemma/internal/workspace"
)

func newWorkspace(t *testing.T) *workspace.Workspace {
	t.Helper()
	ws, err := workspace.Open(t.TempDir(), workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestProjectRoundTrip(t *testing.T) {
	ws := newWorkspace(t)
	ctx := context.Background()
	if _, err := LoadProject(ctx, ws); err == nil {
		t.Fatal("loading a missing project must fail")
	}
	p := canonical.NewProject("prj_1", "Test")
	if err := SaveProject(ws, p); err != nil {
		t.Fatal(err)
	}
	back, err := LoadProject(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if back.ID != "prj_1" || back.Name != "Test" {
		t.Fatalf("loaded project = %+v", back)
	}
	if ok, err := HasProject(ws); err != nil || !ok {
		t.Fatalf("HasProject = %v %v", ok, err)
	}
}

func TestProfileDefaultsWhenMissing(t *testing.T) {
	ws := newWorkspace(t)
	prof, path, err := LoadProfile(context.Background(), ws, canonical.TargetClaude, "")
	if err != nil {
		t.Fatal(err)
	}
	if path != ".stemma/profiles/claude.json" {
		t.Errorf("path = %q", path)
	}
	if prof.Target != canonical.TargetClaude || len(prof.Overrides) != 0 {
		t.Fatalf("default profile = %+v", prof)
	}
	if err := SaveProfile(ws, prof); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadProfile(context.Background(), ws, canonical.TargetClaude, ""); err != nil {
		t.Fatal(err)
	}
}

func TestProfileTargetMismatchIsRejected(t *testing.T) {
	ws := newWorkspace(t)
	if err := SaveProfile(ws, profiles.Default(canonical.TargetCodex)); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadProfile(context.Background(), ws, canonical.TargetClaude, ProfilePath(canonical.TargetCodex))
	if err == nil {
		t.Fatal("loading a profile for the wrong target must fail")
	}
}

func TestManifestRoundTrip(t *testing.T) {
	ws := newWorkspace(t)
	ctx := context.Background()
	m, err := LoadManifest(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Targets) != 0 {
		t.Fatal("a missing manifest must load as an empty one")
	}
	m.Targets["claude"] = manifest.TargetRecord{ProjectHash: "sha256:1"}
	if err := SaveManifest(ws, m); err != nil {
		t.Fatal(err)
	}
	back, err := LoadManifest(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if back.Targets["claude"].ProjectHash != "sha256:1" {
		t.Fatalf("manifest = %+v", back)
	}
}
