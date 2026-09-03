package cli

import (
	"context"
	"fmt"
	"runtime"

	"github.com/alexvinola/stemma/internal/adapters/registry"
	"github.com/alexvinola/stemma/internal/capabilities"
	"github.com/alexvinola/stemma/internal/version"
)

type versionData struct {
	Version               string   `json:"version"`
	GoVersion             string   `json:"goVersion"`
	Platform              string   `json:"platform"`
	CanonicalSchema       int      `json:"canonicalSchemaVersion"`
	ProfileSchema         int      `json:"profileSchemaVersion"`
	ManifestSchema        int      `json:"manifestSchemaVersion"`
	ReportSchema          int      `json:"reportSchemaVersion"`
	PlanSchema            int      `json:"planSchemaVersion"`
	ImporterVersion       string   `json:"importerVersion"`
	CompatibilityBaseline string   `json:"compatibilityBaseline"`
	ImplementedTargets    []string `json:"implementedTargets"`
	DeclaredTargets       []string `json:"declaredTargets"`
}

func runVersion(_ context.Context, env Env, args []string) int {
	fs := newFlagSet(env, "version")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	data := versionData{
		Version:               version.Version,
		GoVersion:             runtime.Version(),
		Platform:              runtime.GOOS + "/" + runtime.GOARCH,
		CanonicalSchema:       version.CanonicalSchemaVersion,
		ProfileSchema:         version.ProfileSchemaVersion,
		ManifestSchema:        version.ManifestSchemaVersion,
		ReportSchema:          version.ReportSchemaVersion,
		PlanSchema:            version.PlanSchemaVersion,
		ImporterVersion:       version.ImporterVersion,
		CompatibilityBaseline: version.CompatibilityBaseline,
	}
	for _, t := range registry.Implemented() {
		data.ImplementedTargets = append(data.ImplementedTargets, string(t))
	}
	for _, c := range capabilities.All() {
		label := string(c.Target)
		if !c.Available {
			label += " (declared, not implemented)"
		}
		data.DeclaredTargets = append(data.DeclaredTargets, label)
	}

	if *jsonOut {
		if err := WriteJSON(env, NewEnvelope("version", ExitOK, nil, data)); err != nil {
			return ExitInternal
		}
		return ExitOK
	}
	fmt.Fprintf(env.Stdout, "stemma %s\n", data.Version)
	fmt.Fprintf(env.Stdout, "  built with:              %s (%s)\n", data.GoVersion, data.Platform)
	fmt.Fprintf(env.Stdout, "  canonical schema:        v%d\n", data.CanonicalSchema)
	fmt.Fprintf(env.Stdout, "  profile schema:          v%d\n", data.ProfileSchema)
	fmt.Fprintf(env.Stdout, "  manifest schema:         v%d\n", data.ManifestSchema)
	fmt.Fprintf(env.Stdout, "  report schema:           v%d\n", data.ReportSchema)
	fmt.Fprintf(env.Stdout, "  plan schema:             v%d\n", data.PlanSchema)
	fmt.Fprintf(env.Stdout, "  importer version:        %s\n", data.ImporterVersion)
	fmt.Fprintf(env.Stdout, "  compatibility baseline:  %s\n", data.CompatibilityBaseline)
	fmt.Fprintf(env.Stdout, "  targets:\n")
	for _, t := range data.DeclaredTargets {
		fmt.Fprintf(env.Stdout, "    %s\n", t)
	}
	return ExitOK
}
