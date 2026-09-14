package compiler

import (
	"bytes"
	"context"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/manifest"
	"github.com/alexvinola/stemma-cli/internal/optimizer"
	"github.com/alexvinola/stemma-cli/internal/profiles"
	"github.com/alexvinola/stemma-cli/internal/version"
)

// verifyImportRoundTrip uses the exact bytes read by Import, not a second read
// of a potentially changed workspace. Normal original-byte reuse is part of
// this round-trip contract; this is not a proof about an unrelated renderer or
// a future edit. Files that cannot be reproduced remain unowned.
func verifyImportRoundTrip(ctx context.Context, project canonical.Project, target canonical.TargetFormat, sources []adapters.SourceFile) (manifest.TargetRecord, []diagnostics.Diagnostic, error) {
	profile := profiles.Default(target)
	profileHash, err := profiles.Hash(profile)
	if err != nil {
		return manifest.TargetRecord{}, nil, err
	}
	projectHash, err := canonical.Hash(project)
	if err != nil {
		return manifest.TargetRecord{}, nil, err
	}
	record := manifest.TargetRecord{
		ProjectHash: projectHash, ProfileHash: profileHash,
		CompatibilityBaseline: version.CompatibilityBaseline, StemmaVersion: version.Version,
		GeneratedFiles: []manifest.GeneratedRecord{}, AcceptedDiagnostics: []string{},
	}
	originals := make(map[string]adapters.SourceFile, len(sources))
	for _, source := range sources {
		originals[source.Path] = source
	}
	out, compileErr := Compile(ctx, project, CompileOptions{
		Target: target, Profile: profile, Originals: originals, Optimizer: optimizer.DefaultOptions(),
	})
	if err := ctx.Err(); err != nil {
		return manifest.TargetRecord{}, nil, err
	}
	generated := make(map[string]GeneratedFile, len(out.Files))
	for _, file := range out.Files {
		generated[file.Path] = file
	}
	var bag diagnostics.Bag
	blocked := diagnostics.HasBlocking(out.Diagnostics)
	for _, source := range sources {
		file, found := generated[source.Path]
		if compileErr == nil && !blocked && found && bytes.Equal(source.Data, file.Content) {
			record.GeneratedFiles = append(record.GeneratedFiles, manifest.GeneratedRecord{
				Path: source.Path, Hash: source.Hash, Entities: append([]string{}, file.Entities...),
			})
			continue
		}
		reason := "The same-provider compilation did not produce this source path."
		switch {
		case compileErr != nil:
			reason = "The same-provider compilation could not be verified: " + compileErr.Error()
		case blocked:
			reason = "Blocking projection diagnostics prevent verifying the same-provider compilation."
		case found:
			reason = "The same-provider output differs from the bytes read at import."
		}
		bag.Add(diagnostics.New(diagnostics.ImportRoundTripUnverified, diagnostics.SeverityWarning,
			"imported source could not be verified for safe updates").
			WithPath(source.Path).WithTarget(string(target)).WithDetail("%s No ownership was recorded; the source file was not changed.", reason).
			WithSuggestion("Compare this source with the canonical entity files and the same-provider plan. Preserve any missing content; use a separate output path until the difference is resolved."))
	}
	return record, bag.Items(), nil
}
