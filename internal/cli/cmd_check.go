package cli

import (
	"context"
	"fmt"

	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/compiler"
	"github.com/alexvinola/stemma/internal/diagnostics"
	"github.com/alexvinola/stemma/internal/store"
)

type checkTarget struct {
	Target    string   `json:"target"`
	UpToDate  bool     `json:"upToDate"`
	Stale     []string `json:"staleFiles"`
	Missing   []string `json:"missingFiles"`
	Conflicts []string `json:"conflicts"`
}

type checkData struct {
	Targets  []checkTarget `json:"targets"`
	UpToDate bool          `json:"upToDate"`
}

func runCheck(ctx context.Context, env Env, args []string) int {
	fs := newFlagSet(env, "check")
	jsonOut := fs.Bool("json", false, "emit JSON")
	dir := fs.String("workspace", "", "repository root (default: current directory)")
	target := fs.String("target", "", "target format to check")
	all := fs.Bool("all", false, "check every target enabled in the canonical project")
	warningsAsErrors := fs.Bool("warnings-as-errors", false, "fail when warnings are present")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	if !*all && *target == "" {
		return fail(env, "check", *jsonOut, ExitUsage,
			fmt.Errorf("check requires --target <target> or --all"), nil)
	}
	ws, err := openWorkspace(env, *dir)
	if err != nil {
		return fail(env, "check", *jsonOut, ExitUsage, err, nil)
	}
	project, err := store.LoadProject(ctx, ws)
	if err != nil {
		return fail(env, "check", *jsonOut, ExitDiagnostics, err, nil)
	}

	var targets []canonical.TargetFormat
	switch {
	case *all:
		targets = project.Targets
		if len(targets) == 0 {
			return fail(env, "check", *jsonOut, ExitUsage,
				fmt.Errorf("the canonical project enables no targets; use --target"), nil)
		}
	case *target != "":
		t, terr := resolveTarget(*target)
		if terr != nil {
			code := ExitUsage
			if t != "" {
				code = ExitUnsupportedTarget
			}
			return fail(env, "check", *jsonOut, code, terr, nil)
		}
		targets = []canonical.TargetFormat{t}
	default:
		return fail(env, "check", *jsonOut, ExitUsage,
			fmt.Errorf("check requires --target <target> or --all"), nil)
	}

	var bag diagnostics.Bag
	bag.Extend(canonical.Validate(project))
	data := checkData{UpToDate: true}
	exit := ExitOK

	for _, t := range targets {
		if _, terr := resolveTarget(string(t)); terr != nil {
			return fail(env, "check", *jsonOut, ExitUnsupportedTarget, terr, nil)
		}
		profile, _, perr := store.LoadProfile(ctx, ws, t, "")
		if perr != nil {
			return fail(env, "check", *jsonOut, ExitDiagnostics, perr, nil)
		}
		m, merr := store.LoadManifest(ctx, ws)
		if merr != nil {
			return fail(env, "check", *jsonOut, ExitDiagnostics, merr, nil)
		}
		plan, berr := compiler.BuildPlan(ctx, ws, project, compiler.PlanOptions{
			Target: t, Profile: profile, Manifest: m,
		})
		if berr != nil {
			return fail(env, "check", *jsonOut, exitCodeForError(berr), berr, nil)
		}
		bag.Extend(plan.Diagnostics)

		ct := checkTarget{Target: string(t), UpToDate: true,
			Stale: []string{}, Missing: []string{}, Conflicts: []string{}}
		for _, c := range plan.Changes {
			switch c.Kind {
			case compiler.ChangeCreate:
				ct.Missing = append(ct.Missing, c.Path)
				ct.UpToDate = false
			case compiler.ChangeUpdate:
				ct.Stale = append(ct.Stale, c.Path)
				ct.UpToDate = false
			case compiler.ChangeConflict:
				ct.Conflicts = append(ct.Conflicts, c.Path)
				ct.UpToDate = false
			}
		}
		if !ct.UpToDate {
			data.UpToDate = false
			exit = ExitDiagnostics
			bag.Add(diagnostics.New(diagnostics.OutputStale, diagnostics.SeverityError,
				fmt.Sprintf("generated output for %s is out of date", t)).
				WithTarget(string(t)).
				WithDetail("%d file(s) would be created, %d updated, %d in conflict.",
					len(ct.Missing), len(ct.Stale), len(ct.Conflicts)).
				WithSuggestion("Run `stemma apply --target %s --yes` and commit the result.", t))
		}
		data.Targets = append(data.Targets, ct)
	}

	diags := bag.Items()
	errs, warns, _ := SummarizeDiagnostics(diags)
	if errs > 0 {
		exit = ExitDiagnostics
	}
	if *warningsAsErrors && warns > 0 {
		exit = ExitDiagnostics
	}

	if *jsonOut {
		if werr := WriteJSON(env, NewEnvelope("check", exit, diags, data)); werr != nil {
			return ExitInternal
		}
		return exit
	}
	for _, ct := range data.Targets {
		status := "up to date"
		if !ct.UpToDate {
			status = "OUT OF DATE"
		}
		fmt.Fprintf(env.Stdout, "%-16s %s\n", ct.Target, status)
		for _, p := range ct.Missing {
			fmt.Fprintf(env.Stdout, "  missing    %s\n", p)
		}
		for _, p := range ct.Stale {
			fmt.Fprintf(env.Stdout, "  stale      %s\n", p)
		}
		for _, p := range ct.Conflicts {
			fmt.Fprintf(env.Stdout, "  conflict   %s\n", p)
		}
	}
	PrintDiagnostics(env.Stdout, diags, false)
	fmt.Fprintf(env.Stdout, "\nNo files were modified.\n")
	return exit
}
