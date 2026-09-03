package cli

import (
	"context"
	"fmt"

	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/diagnostics"
	"github.com/alexvinola/stemma/internal/profiles"
	"github.com/alexvinola/stemma/internal/store"
	"github.com/alexvinola/stemma/internal/workspace"
)

type validateData struct {
	Project  string         `json:"projectId"`
	Entities int            `json:"entityCount"`
	Counts   map[string]int `json:"entityCounts"`
	Profiles []string       `json:"profilesChecked"`
	Errors   int            `json:"errors"`
	Warnings int            `json:"warnings"`
	Notes    int            `json:"notes"`
}

func runValidate(ctx context.Context, env Env, args []string) int {
	fs := newFlagSet(env, "validate")
	jsonOut := fs.Bool("json", false, "emit JSON")
	dir := fs.String("workspace", "", "repository root (default: current directory)")
	explain := fs.Bool("explain", false, "print diagnostic details and notes")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	ws, err := openWorkspace(env, *dir)
	if err != nil {
		return fail(env, "validate", *jsonOut, ExitUsage, err, nil)
	}
	project, err := store.LoadProject(ctx, ws)
	if err != nil {
		return fail(env, "validate", *jsonOut, ExitDiagnostics, err, nil)
	}

	var bag diagnostics.Bag
	bag.Extend(canonical.Validate(project))

	var checked []string
	for _, t := range canonical.AllTargets() {
		path := store.ProfilePath(t)
		exists, perr := ws.Exists(path)
		if perr != nil || !exists {
			continue
		}
		prof, _, lerr := store.LoadProfile(ctx, ws, t, path)
		if lerr != nil {
			bag.Add(diagnostics.New(diagnostics.ProfileInvalid, diagnostics.SeverityError,
				"target profile could not be read").WithPath(path).WithDetail("%v", lerr))
			continue
		}
		checked = append(checked, path)
		bag.Extend(profiles.Validate(prof, project, path))
	}

	bag.Extend(validateManifest(ctx, ws, project))

	diags := bag.Items()
	errs, warns, notes := SummarizeDiagnostics(diags)
	data := validateData{
		Project:  project.ID,
		Entities: len(project.Entities()),
		Counts:   entityCounts(project),
		Profiles: checked,
		Errors:   errs,
		Warnings: warns,
		Notes:    notes,
	}
	code := ExitOK
	if errs > 0 {
		code = ExitDiagnostics
	}

	if *jsonOut {
		if err := WriteJSON(env, NewEnvelope("validate", code, diags, data)); err != nil {
			return ExitInternal
		}
		return code
	}
	fmt.Fprintf(env.Stdout, "Canonical project %s (%s)\n", SanitizeLine(project.Name), project.ID)
	fmt.Fprintf(env.Stdout, "  entities:        %d\n", data.Entities)
	fmt.Fprintf(env.Stdout, "  opaque blocks:   %d\n", len(project.OpaqueBlocks))
	fmt.Fprintf(env.Stdout, "  targets:         %v\n", project.Targets)
	if len(checked) > 0 {
		fmt.Fprintf(env.Stdout, "  profiles:        %v\n", checked)
	}
	PrintDiagnostics(env.Stdout, diags, *explain)
	fmt.Fprintf(env.Stdout, "\n%d error(s), %d warning(s), %d note(s).\n", errs, warns, notes)
	return code
}

// validateManifest checks that the manifest still matches the repository.
func validateManifest(ctx context.Context, ws *workspace.Workspace, project canonical.Project) []diagnostics.Diagnostic {
	var bag diagnostics.Bag
	m, err := store.LoadManifest(ctx, ws)
	if err != nil {
		bag.Add(diagnostics.New(diagnostics.ManifestInvalid, diagnostics.SeverityError,
			"manifest could not be read").WithPath(store.ManifestFile).WithDetail("%v", err))
		return bag.Items()
	}
	hash, herr := canonical.Hash(project)
	if herr == nil && m.ProjectHash != "" && m.ProjectHash != hash {
		bag.Add(diagnostics.New(diagnostics.ManifestInvalid, diagnostics.SeverityInfo,
			"the canonical project changed since the last import or apply").
			WithPath(store.ManifestFile).
			WithDetail("Generated files for at least one target are probably out of date.").
			WithSuggestion("Run `stemma plan --target <target>` to see what changed."))
	}
	for _, target := range SortedKeys(m.Targets) {
		rec := m.Targets[target]
		for _, f := range rec.GeneratedFiles {
			current, exists, ferr := ws.HashFile(f.Path)
			switch {
			case ferr != nil:
				bag.Add(diagnostics.New(diagnostics.ManifestInvalid, diagnostics.SeverityWarning,
					"a generated file could not be inspected").WithPath(f.Path).WithDetail("%v", ferr))
			case !exists:
				bag.Add(diagnostics.New(diagnostics.ManifestInvalid, diagnostics.SeverityWarning,
					"a file recorded in the manifest no longer exists").
					WithPath(f.Path).WithTarget(target).
					WithSuggestion("Run `stemma plan --target %s` to regenerate it.", target))
			case current != f.Hash:
				bag.Add(diagnostics.New(diagnostics.ManifestInvalid, diagnostics.SeverityWarning,
					"a generated file was modified after Stemma wrote it").
					WithPath(f.Path).WithTarget(target).
					WithDetail("Stemma will report this as a conflict instead of overwriting it.").
					WithSuggestion("Move the change into .stemma/project.json, or delete the file."))
			}
		}
		_ = rec
	}
	return bag.Items()
}
