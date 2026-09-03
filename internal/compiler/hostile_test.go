package compiler_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/cli"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/profiles"
)

// TestMalformedInputProducesDiagnosticsNotPanics imports deliberately broken
// configuration for every provider and requires diagnostics rather than a
// crash, and requires that nothing is silently dropped.
func TestMalformedInputProducesDiagnosticsNotPanics(t *testing.T) {
	formats := []canonical.TargetFormat{
		canonical.TargetClaude, canonical.TargetCopilot, canonical.TargetKiro,
	}
	for _, format := range formats {
		t.Run(string(format), func(t *testing.T) {
			ws := materialize(t, filepath.Join(testdataDir, "malformed"))
			res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{
				Format: format, ProjectID: "prj_malformed", ProjectName: "Malformed",
			})
			if err != nil {
				t.Fatalf("import returned an error instead of diagnostics: %v", err)
			}
			if len(res.Diagnostics) == 0 {
				t.Fatal("malformed input produced no diagnostics")
			}
			if !diagnostics.HasSeverity(res.Diagnostics, diagnostics.SeverityError) {
				t.Errorf("malformed input produced no error diagnostic: %+v", res.Diagnostics)
			}
			// Every file that failed to parse must be preserved verbatim.
			for _, blk := range res.Project.OpaqueBlocks {
				if blk.Hash == "" || blk.Reason == "" {
					t.Errorf("opaque block %s is incomplete", blk.ID)
				}
			}
			// The project must still be encodable and re-decodable.
			data, err := canonical.MarshalProject(res.Project)
			if err != nil {
				t.Fatalf("marshalling a project built from malformed input failed: %v", err)
			}
			if _, err := canonical.UnmarshalProject(data); err != nil {
				t.Fatalf("re-decoding failed: %v", err)
			}
		})
	}
}

// TestUnsafeYAMLIsRefused checks that a YAML tag never reaches the model.
func TestUnsafeYAMLIsRefused(t *testing.T) {
	ws := materialize(t, filepath.Join(testdataDir, "malformed"))
	res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{
		Format: canonical.TargetClaude, ProjectID: "p", ProjectName: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == diagnostics.UnsafeYAMLConstruct {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected STEMMA1103 for a YAML tag; got %+v", res.Diagnostics)
	}
	for _, rule := range res.Project.Rules {
		if strings.Contains(rule.Instruction, "os.system") && rule.Activation.Type != canonical.ActivationAlways {
			t.Error("a YAML tag was interpreted")
		}
	}
}

// TestEscapingGlobIsRejected checks that a rule cannot scope outside the root.
func TestEscapingGlobIsRejected(t *testing.T) {
	ws := materialize(t, filepath.Join(testdataDir, "malformed"))
	res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{
		Format: canonical.TargetClaude, ProjectID: "p", ProjectName: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range res.Project.Rules {
		for _, p := range rule.Activation.Include {
			if strings.Contains(p, "..") {
				t.Errorf("rule %s kept an escaping pattern %q", rule.ID, p)
			}
		}
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == diagnostics.InvalidGlob {
			found = true
		}
	}
	if !found {
		t.Error("expected an invalid-glob diagnostic")
	}
}

// TestHostileContentIsPreservedNotExecuted checks that instructions that look
// like shell commands are treated as ordinary text, and that terminal control
// characters never reach rendered output.
func TestHostileContentIsPreservedNotExecuted(t *testing.T) {
	ws := materialize(t, filepath.Join(testdataDir, "security"))
	res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{
		Format: canonical.TargetClaude, ProjectID: "p", ProjectName: "Security",
	})
	if err != nil {
		t.Fatal(err)
	}

	var carriesCommand bool
	for _, rule := range res.Project.Rules {
		if strings.Contains(rule.Instruction, "curl https://example.com/install.sh") {
			carriesCommand = true
		}
	}
	if !carriesCommand {
		t.Error("command-like text must be preserved as ordinary content")
	}

	// Compile to every target: the text stays text, and paths stay confined.
	for _, target := range []canonical.TargetFormat{
		canonical.TargetClaude, canonical.TargetCodex, canonical.TargetCopilot, canonical.TargetKiro,
	} {
		out, err := compiler.Compile(context.Background(), res.Project, compiler.CompileOptions{
			Target: target, Profile: profiles.Default(target),
		})
		if err != nil {
			t.Fatalf("compile %s: %v", target, err)
		}
		assertProjectionInvariants(t, res.Project, out)
		for _, f := range out.Files {
			if strings.Contains(f.Path, "..") {
				t.Errorf("generated path escapes: %s", f.Path)
			}
		}
	}

	// Diagnostic rendering must neutralise terminal escapes.
	var sb strings.Builder
	cli.PrintDiagnostics(&sb, res.Diagnostics, true)
	if strings.ContainsRune(sb.String(), 0x1b) {
		t.Error("rendered diagnostics contain a raw ESC character")
	}
}

// TestUnsafeAgentToolNameIsRejected checks canonical validation of tool names.
func TestUnsafeAgentToolNameIsRejected(t *testing.T) {
	ws := materialize(t, filepath.Join(testdataDir, "security"))
	res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{
		Format: canonical.TargetClaude, ProjectID: "p", ProjectName: "Security",
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range res.Diagnostics {
		if d.Code == diagnostics.AgentToolsNeedReview && d.Severity == diagnostics.SeverityError {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a blocking diagnostic for an unsafe tool name; got %+v", res.Diagnostics)
	}
}

// TestDeepFrontMatterNestingIsBounded checks the nesting limit.
func TestDeepFrontMatterNestingIsBounded(t *testing.T) {
	ws := materialize(t, filepath.Join(testdataDir, "security"))
	res, err := compiler.Import(context.Background(), ws, compiler.ImportOptions{
		Format: canonical.TargetKiro, ProjectID: "p", ProjectName: "Security",
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range res.Diagnostics {
		if strings.Contains(d.Summary, "nesting") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a nesting-limit diagnostic; got %+v", res.Diagnostics)
	}
}

// TestUnsafeProfilePathsAreRejected checks that a profile cannot redirect
// generated files outside the workspace.
func TestUnsafeProfilePathsAreRejected(t *testing.T) {
	project := mustReadFixtureProject(t, "canonical/exclude-and-disabled")
	for _, override := range []profiles.Override{
		{Directory: "../escape"},
		{Directory: "/etc"},
		{Filename: "../../passwd"},
	} {
		profile := profiles.Default(canonical.TargetClaude)
		profile.Overrides = map[string]profiles.Override{"rule.controller-repository": override}
		diags := profiles.Validate(profile, project, ".stemma/profiles/claude.json")
		if !diagnostics.HasSeverity(diags, diagnostics.SeverityError) {
			t.Errorf("override %+v was accepted", override)
		}
		out, err := compiler.Compile(context.Background(), project, compiler.CompileOptions{
			Target: canonical.TargetClaude, Profile: profile,
		})
		if err != nil {
			continue // refusing outright is also acceptable
		}
		for _, f := range out.Files {
			if strings.Contains(f.Path, "..") || strings.HasPrefix(f.Path, "/") {
				t.Errorf("override %+v produced an escaping path %q", override, f.Path)
			}
		}
	}
}
