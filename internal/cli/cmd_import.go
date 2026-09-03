package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/compiler"
	"github.com/alexvinola/stemma/internal/diagnostics"
	"github.com/alexvinola/stemma/internal/manifest"
	"github.com/alexvinola/stemma/internal/store"
	"github.com/alexvinola/stemma/internal/workspace"
)

type importData struct {
	Format   string         `json:"format"`
	Output   string         `json:"output"`
	Sources  []string       `json:"sources"`
	Counts   map[string]int `json:"entityCounts"`
	Preserve int            `json:"opaqueBlocks"`
}

func runImport(ctx context.Context, env Env, args []string) int {
	fs := newFlagSet(env, "import")
	jsonOut := fs.Bool("json", false, "emit JSON")
	dir := fs.String("workspace", "", "repository root (default: current directory)")
	from := fs.String("from", "auto", "source format: auto, github-copilot, claude, codex, kiro")
	output := fs.String("output", store.ProjectFile, "where to write the canonical project")
	overwrite := fs.Bool("overwrite", false, "replace an existing canonical project")
	name := fs.String("name", "", "project name (default: the workspace directory name)")
	positional, code, ok := parsePositional(fs, args)
	if !ok {
		return code
	}
	root := *dir
	if root == "" && len(positional) > 0 {
		root = positional[0]
	}

	ws, err := openWorkspace(env, root)
	if err != nil {
		return fail(env, "import", *jsonOut, ExitUsage, err, nil)
	}
	outPath, err := workspace.NormalizeRel(*output)
	if err != nil {
		return fail(env, "import", *jsonOut, ExitUsage,
			fmt.Errorf("invalid --output: %w", err), nil)
	}

	var format canonical.TargetFormat
	if *from != "auto" && *from != "" {
		format = canonical.TargetFormat(*from)
		if !canonical.KnownTarget(format) {
			return fail(env, "import", *jsonOut, ExitUsage,
				fmt.Errorf("unknown source format %q", *from), nil)
		}
	}

	// Preserve identity when replacing an existing project.
	var existingID string
	exists, err := ws.Exists(outPath)
	if err != nil {
		return fail(env, "import", *jsonOut, ExitDiagnostics, err, nil)
	}
	hasContent := false
	if exists {
		if prev, perr := store.LoadProject(ctx, ws); perr == nil {
			existingID = prev.ID
			hasContent = len(prev.Entities()) > 0 || len(prev.OpaqueBlocks) > 0
		} else {
			hasContent = true // an unreadable project is never silently replaced
		}
	}
	if exists && hasContent {
		if !*overwrite {
			if *jsonOut || !env.StdinIsTTY {
				return fail(env, "import", *jsonOut, ExitDiagnostics,
					fmt.Errorf("%s already exists; pass --overwrite to replace it", outPath), nil)
			}
			ok, cerr := confirm(env, fmt.Sprintf("Replace the existing canonical project at %s?", outPath))
			if cerr != nil || !ok {
				fmt.Fprintf(env.Stdout, "Import cancelled; nothing was written.\n")
				return ExitDiagnostics
			}
		}
	}

	result, err := compiler.Import(ctx, ws, compiler.ImportOptions{
		Format:      format,
		ProjectID:   existingID,
		ProjectName: *name,
	})
	if err != nil {
		code := ExitDiagnostics
		switch {
		case errors.Is(err, compiler.ErrAmbiguousSource):
			code = ExitDiagnostics
		case errors.Is(err, compiler.ErrTargetUnavailable):
			code = ExitUnsupportedTarget
		}
		if *jsonOut {
			doc := NewEnvelope("import", code, result.Diagnostics, nil)
			doc.Error = err.Error()
			if werr := WriteJSON(env, doc); werr != nil {
				return ExitInternal
			}
			return code
		}
		PrintDiagnostics(env.Stderr, result.Diagnostics, true)
		fmt.Fprintf(env.Stderr, "stemma: %s\n", SanitizeLine(err.Error()))
		return code
	}

	if diagnostics.HasBlocking(result.Diagnostics) {
		code := ExitDiagnostics
		if *jsonOut {
			doc := NewEnvelope("import", code, result.Diagnostics, nil)
			doc.Error = "blocking diagnostics prevented the import from being written"
			if werr := WriteJSON(env, doc); werr != nil {
				return ExitInternal
			}
			return code
		}
		PrintDiagnostics(env.Stderr, result.Diagnostics, true)
		fmt.Fprintf(env.Stderr,
			"stemma: import aborted; the source files produced blocking diagnostics and nothing was written.\n")
		return code
	}

	data, err := canonical.MarshalProject(result.Project)
	if err != nil {
		return fail(env, "import", *jsonOut, ExitInternal, err, nil)
	}
	tx := ws.Begin()
	if err := tx.Add(workspace.WriteOp{Path: outPath, Content: data, Mode: 0o644}); err != nil {
		return fail(env, "import", *jsonOut, ExitWriteFailed, err, nil)
	}
	if outPath == store.ProjectFile {
		m, merr := store.LoadManifest(ctx, ws)
		if merr != nil {
			return fail(env, "import", *jsonOut, ExitDiagnostics, merr, nil)
		}
		m.ImportedSources = result.Sources
		m.ImportedFormat = string(result.Format)
		if hash, herr := canonical.Hash(result.Project); herr == nil {
			m.ProjectHash = hash
		}
		mdata, merr := manifest.Marshal(m)
		if merr != nil {
			return fail(env, "import", *jsonOut, ExitInternal, merr, nil)
		}
		if err := tx.Add(workspace.WriteOp{Path: store.ManifestFile, Content: mdata, Mode: 0o644}); err != nil {
			return fail(env, "import", *jsonOut, ExitWriteFailed, err, nil)
		}
	}
	if err := tx.Commit(); err != nil {
		return fail(env, "import", *jsonOut, ExitWriteFailed, err, nil)
	}

	payload := importData{
		Format:   string(result.Format),
		Output:   outPath,
		Counts:   entityCounts(result.Project),
		Preserve: len(result.Project.OpaqueBlocks),
	}
	for _, s := range result.Sources {
		payload.Sources = append(payload.Sources, s.Path)
	}
	if *jsonOut {
		if err := WriteJSON(env, NewEnvelope("import", ExitOK, result.Diagnostics, payload)); err != nil {
			return ExitInternal
		}
		return ExitOK
	}
	fmt.Fprintf(env.Stdout, "Imported %s configuration from %s\n",
		result.Format, Plural(len(result.Sources), "file", "files"))
	for _, k := range SortedKeys(payload.Counts) {
		if payload.Counts[k] > 0 {
			fmt.Fprintf(env.Stdout, "  %-18s %d\n", k, payload.Counts[k])
		}
	}
	if payload.Preserve > 0 {
		fmt.Fprintf(env.Stdout, "  %-18s %d (content preserved without interpretation)\n",
			"opaque blocks", payload.Preserve)
	}
	fmt.Fprintf(env.Stdout, "\nWrote %s\n", outPath)
	PrintDiagnostics(env.Stdout, result.Diagnostics, false)
	return ExitOK
}

func entityCounts(p canonical.Project) map[string]int {
	return map[string]int{
		"context documents": len(p.ContextDocuments),
		"rules":             len(p.Rules),
		"procedures":        len(p.Procedures),
		"skills":            len(p.Skills),
		"agents":            len(p.Agents),
		"decisions":         len(p.Decisions),
	}
}
