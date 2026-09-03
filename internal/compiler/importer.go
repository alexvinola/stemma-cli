package compiler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alexvinola/stemma/internal/adapters"
	"github.com/alexvinola/stemma/internal/adapters/registry"
	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/diagnostics"
	"github.com/alexvinola/stemma/internal/discovery"
	"github.com/alexvinola/stemma/internal/manifest"
	"github.com/alexvinola/stemma/internal/workspace"
)

// ErrNoSources reports that no supported configuration was found.
var ErrNoSources = errors.New("no supported agent configuration found")

// ErrAmbiguousSource reports that several providers were found and none was
// selected explicitly.
var ErrAmbiguousSource = errors.New("several agent configurations found")

// ImportOptions configures an import.
type ImportOptions struct {
	// Format selects the source provider; empty means auto-detect.
	Format canonical.TargetFormat
	// ProjectID and ProjectName seed the canonical project. When ProjectID is
	// empty a deterministic identifier is derived from the workspace name.
	ProjectID   string
	ProjectName string
}

// ImportResult is the outcome of an import.
type ImportResult struct {
	Project     canonical.Project
	Format      canonical.TargetFormat
	Sources     []manifest.SourceRecord
	Diagnostics []diagnostics.Diagnostic
	Scan        discovery.Result
}

// Import reads a provider's configuration and builds a canonical project.
func Import(ctx context.Context, ws *workspace.Workspace, opts ImportOptions) (ImportResult, error) {
	scan, err := discovery.Scan(ctx, ws)
	if err != nil {
		return ImportResult{}, err
	}
	var bag diagnostics.Bag
	bag.Extend(scan.Diagnostics)

	format := opts.Format
	if format == "" {
		formats := scan.Formats()
		switch len(formats) {
		case 0:
			return ImportResult{Scan: scan, Diagnostics: bag.Items()},
				fmt.Errorf("%w: looked for %s", ErrNoSources, strings.Join(discovery.Registry(), ", "))
		case 1:
			format = formats[0]
		default:
			names := make([]string, 0, len(formats))
			for _, f := range formats {
				names = append(names, string(f))
			}
			bag.Add(diagnostics.New(diagnostics.MultipleSources, diagnostics.SeverityError,
				"several agent configurations are present; Stemma will not merge them silently").
				WithDetail("Detected: %s.", strings.Join(names, ", ")).
				WithSuggestion("Re-run with --from <format> to choose one."))
			return ImportResult{Scan: scan, Diagnostics: bag.Items()},
				fmt.Errorf("%w: %s", ErrAmbiguousSource, strings.Join(names, ", "))
		}
	}

	importer, ok := registry.Importer(format)
	if !ok {
		bag.Add(diagnostics.New(diagnostics.TargetUnavailable, diagnostics.SeverityError,
			fmt.Sprintf("no importer is implemented for %q", format)).
			WithTarget(string(format)))
		return ImportResult{Scan: scan, Diagnostics: bag.Items()},
			fmt.Errorf("%w: %q has no importer", ErrTargetUnavailable, format)
	}

	matches := scan.Files(format)
	if len(matches) == 0 {
		return ImportResult{Scan: scan, Diagnostics: bag.Items()},
			fmt.Errorf("%w for %q", ErrNoSources, format)
	}

	files := make([]adapters.SourceFile, 0, len(matches))
	sources := make([]manifest.SourceRecord, 0, len(matches))
	for _, m := range matches {
		if err := ctx.Err(); err != nil {
			return ImportResult{}, err
		}
		f, err := ws.ReadFile(ctx, m.Path)
		if err != nil {
			bag.Add(diagnostics.New(diagnostics.FileUnreadable, diagnostics.SeverityError,
				"configuration file could not be read").
				WithPath(m.Path).WithDetail("%v", err))
			continue
		}
		files = append(files, adapters.SourceFile{
			Path: f.Path, Data: f.Data, Hash: f.Hash, Role: m.Role, Mode: f.Mode,
		})
		sources = append(sources, manifest.SourceRecord{Path: f.Path, Hash: f.Hash, Format: string(format)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Slice(sources, func(i, j int) bool { return sources[i].Path < sources[j].Path })

	out, err := importer.Import(ctx, adapters.ImportInput{Files: files, IDs: canonical.NewAllocator()})
	if err != nil {
		return ImportResult{}, fmt.Errorf("import %s: %w", format, err)
	}
	bag.Extend(out.Diagnostics)

	project := out.Project
	project.SchemaVersion = canonicalSchemaVersion()
	project.Name = opts.ProjectName
	if project.Name == "" {
		project.Name = filepath.Base(ws.Root())
	}
	project.ID = opts.ProjectID
	if project.ID == "" {
		project.ID = DeriveProjectID(project.Name)
	}
	project.Targets = []canonical.TargetFormat{format}
	project.Sort()
	canonical.StampContentHashes(&project)

	bag.Extend(canonical.Validate(project))

	return ImportResult{
		Project:     project,
		Format:      format,
		Sources:     sources,
		Diagnostics: bag.Items(),
		Scan:        scan,
	}, nil
}

// DeriveProjectID builds a stable project identifier from a name.
func DeriveProjectID(name string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(name)))
	return "prj_" + hex.EncodeToString(sum[:])[:16]
}

func canonicalSchemaVersion() int {
	p := canonical.NewProject("x", "x")
	return p.SchemaVersion
}
