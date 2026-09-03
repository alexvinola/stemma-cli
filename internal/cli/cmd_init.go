package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/capabilities"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/profiles"
	"github.com/alexvinola/stemma-cli/internal/store"
)

type initData struct {
	Created []string `json:"created"`
	Project string   `json:"projectId"`
	Name    string   `json:"projectName"`
}

func runInit(ctx context.Context, env Env, args []string) int {
	fs := newFlagSet(env, "init")
	jsonOut := fs.Bool("json", false, "emit JSON")
	dir := fs.String("workspace", "", "repository root (default: current directory)")
	name := fs.String("name", "", "project name (default: the workspace directory name)")
	withProfiles := fs.Bool("with-profiles", false, "also create default target profiles")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	ws, err := openWorkspace(env, *dir)
	if err != nil {
		return fail(env, "init", *jsonOut, ExitUsage, err, nil)
	}
	exists, err := store.HasProject(ws)
	if err != nil {
		return fail(env, "init", *jsonOut, ExitDiagnostics, err, nil)
	}
	if exists {
		return fail(env, "init", *jsonOut, ExitDiagnostics,
			fmt.Errorf("%s already exists; stemma init never overwrites an existing canonical project",
				store.ProjectFile), nil)
	}

	projectName := *name
	if projectName == "" {
		projectName = filepath.Base(ws.Root())
	}
	project := canonical.NewProject(compiler.DeriveProjectID(projectName), projectName)
	if _, err := store.SaveProject(ctx, ws, project, false); err != nil {
		return fail(env, "init", *jsonOut, ExitWriteFailed, err, nil)
	}
	created := []string{store.ProjectFile, store.ProvenanceFile}

	if *withProfiles {
		for _, t := range capabilities.AvailableTargets() {
			if err := store.SaveProfile(ws, profiles.Default(t)); err != nil {
				return fail(env, "init", *jsonOut, ExitWriteFailed, err, nil)
			}
			created = append(created, store.ProfilePath(t))
		}
	}

	data := initData{Created: created, Project: project.ID, Name: project.Name}
	if *jsonOut {
		if err := WriteJSON(env, NewEnvelope("init", ExitOK, nil, data)); err != nil {
			return ExitInternal
		}
		return ExitOK
	}
	fmt.Fprintf(env.Stdout, "Created canonical project %q (%s)\n", SanitizeLine(project.Name), project.ID)
	for _, c := range created {
		fmt.Fprintf(env.Stdout, "  %s\n", c)
	}
	fmt.Fprintf(env.Stdout, "\nNext: run `stemma scan` to see what agent configuration this repository has,\n")
	fmt.Fprintf(env.Stdout, "then `stemma import --from <format>` to populate the canonical project.\n")
	return ExitOK
}
