package compiler

import (
	"context"
	"strings"
	"testing"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/profiles"
	"github.com/alexvinola/stemma-cli/internal/provenance"
)

// Exercise the verifier without source-byte reuse: matching a destination
// path alone must never establish ownership of different bytes.
func TestImportVerificationRequiresIdenticalBytes(t *testing.T) {
	ctx := context.Background()
	p := canonical.NewProject("project", "Project")
	p.ContextDocuments = []canonical.ContextDocument{{ID: "context.guide", Title: "Guide",
		Content: "Run tests.", Kind: canonical.KindOther, Audience: canonical.AudienceAgent,
		Activation: canonical.Always()}}
	out, err := Compile(ctx, p, CompileOptions{Target: canonical.TargetClaude, Profile: profiles.Default(canonical.TargetClaude)})
	if err != nil || diagnostics.HasBlocking(out.Diagnostics) || len(out.Files) != 1 {
		t.Fatalf("control compile: %+v, %v", out, err)
	}
	for _, changed := range []bool{false, true} {
		data := append([]byte{}, out.Files[0].Content...)
		if changed {
			data = append(data, []byte("\nUnrepresented source content.\n")...)
		}
		source := adapters.SourceFile{Path: out.Files[0].Path, Data: data, Hash: provenance.HashBytes(data)}
		record, diags, err := verifyImportRoundTrip(ctx, p, canonical.TargetClaude, []adapters.SourceFile{source})
		if err != nil {
			t.Fatal(err)
		}
		if changed {
			if len(record.GeneratedFiles) != 0 || len(diags) != 1 || diags[0].Code != diagnostics.ImportRoundTripUnverified ||
				!strings.Contains(diags[0].Detail, "differs from the bytes") {
				t.Fatalf("different bytes were not rejected: %+v, %+v", record, diags)
			}
		} else if len(record.GeneratedFiles) != 1 || record.GeneratedFiles[0].Hash != source.Hash || len(diags) != 0 {
			t.Fatalf("identical bytes were not verified: %+v, %+v", record, diags)
		}
	}
}
