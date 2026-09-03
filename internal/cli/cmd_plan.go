package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/adapters"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/store"
	"github.com/alexvinola/stemma-cli/internal/tokenestimate"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

func runPlan(ctx context.Context, env Env, args []string) int {
	fs := newFlagSet(env, "plan")
	jsonOut := fs.Bool("json", false, "emit JSON")
	dir := fs.String("workspace", "", "repository root (default: current directory)")
	target := fs.String("target", "", "target format to compile")
	profilePath := fs.String("profile", "", "target profile to use (default: .stemma/profiles/<target>.json)")
	showUnchanged := fs.Bool("show-unchanged", false, "list files that would not change")
	explain := fs.Bool("explain", false, "print per-entity projection details")
	outputPlan := fs.String("output-plan", "", "write the plan as JSON to this path for later apply")
	adopt := fs.Bool("adopt-untracked", false, "let Stemma take ownership of existing untracked files")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	plan, ws, code, err := buildPlan(ctx, env, *dir, *target, *profilePath, *adopt)
	if err != nil {
		return fail(env, "plan", *jsonOut, code, err, nil)
	}

	if *outputPlan != "" {
		path, perr := workspace.NormalizeRel(*outputPlan)
		if perr != nil {
			return fail(env, "plan", *jsonOut, ExitUsage, fmt.Errorf("invalid --output-plan: %w", perr), nil)
		}
		data, merr := compiler.MarshalPlan(plan)
		if merr != nil {
			return fail(env, "plan", *jsonOut, ExitInternal, merr, nil)
		}
		tx := ws.Begin()
		if aerr := tx.Add(workspace.WriteOp{Path: path, Content: data, Mode: 0o644}); aerr != nil {
			return fail(env, "plan", *jsonOut, ExitWriteFailed, aerr, nil)
		}
		if cerr := tx.Commit(); cerr != nil {
			return fail(env, "plan", *jsonOut, ExitWriteFailed, cerr, nil)
		}
	}

	exit := ExitOK
	if len(plan.Blocking()) > 0 {
		exit = ExitDiagnostics
	}
	if *jsonOut {
		if err := WriteJSON(env, NewEnvelope("plan", exit, plan.Diagnostics, plan)); err != nil {
			return ExitInternal
		}
		return exit
	}
	printPlan(env, plan, *showUnchanged, *explain)
	if *outputPlan != "" {
		fmt.Fprintf(env.Stdout, "\nPlan written to %s\n", *outputPlan)
	}
	fmt.Fprintf(env.Stdout, "\nNothing was modified. Run `stemma apply --target %s` to write these changes.\n",
		plan.Target)
	return exit
}

// buildPlan loads the project and profile and compiles a plan.
func buildPlan(
	ctx context.Context, env Env, dir, target, profilePath string, adopt bool,
) (compiler.Plan, *workspace.Workspace, int, error) {
	t, err := resolveTarget(target)
	if err != nil {
		code := ExitUsage
		if t != "" {
			code = ExitUnsupportedTarget
		}
		return compiler.Plan{}, nil, code, err
	}
	ws, err := openWorkspace(env, dir)
	if err != nil {
		return compiler.Plan{}, nil, ExitUsage, err
	}
	project, err := store.LoadProject(ctx, ws)
	if err != nil {
		return compiler.Plan{}, ws, ExitDiagnostics, err
	}
	profile, _, err := store.LoadProfile(ctx, ws, t, profilePath)
	if err != nil {
		return compiler.Plan{}, ws, ExitDiagnostics, err
	}
	m, err := store.LoadManifest(ctx, ws)
	if err != nil {
		return compiler.Plan{}, ws, ExitDiagnostics, err
	}
	plan, err := compiler.BuildPlan(ctx, ws, project, compiler.PlanOptions{
		Target:         t,
		Profile:        profile,
		Manifest:       m,
		AdoptUntracked: adopt,
	})
	if err != nil {
		return compiler.Plan{}, ws, exitCodeForError(err), err
	}
	return plan, ws, ExitOK, nil
}

func printPlan(env Env, plan compiler.Plan, showUnchanged, explain bool) {
	counts := plan.CountByKind()
	fmt.Fprintf(env.Stdout, "Plan for target %s\n", plan.Target)
	fmt.Fprintf(env.Stdout, "\nFiles\n")
	for _, c := range plan.Changes {
		switch c.Kind {
		case compiler.ChangeUnchanged:
			if showUnchanged {
				fmt.Fprintf(env.Stdout, "  unchanged  %s\n", c.Path)
			}
		case compiler.ChangeCreate:
			fmt.Fprintf(env.Stdout, "  create     %s\n", c.Path)
		case compiler.ChangeUpdate:
			fmt.Fprintf(env.Stdout, "  update     %s\n", c.Path)
		case compiler.ChangeDeleteProposed:
			fmt.Fprintf(env.Stdout, "  stale      %s  (delete proposed; Stemma will not remove it)\n", c.Path)
		case compiler.ChangeConflict:
			fmt.Fprintf(env.Stdout, "  conflict   %s  (%s)\n", c.Path, SanitizeLine(c.Reason))
		}
	}
	if counts[compiler.ChangeUnchanged] > 0 && !showUnchanged {
		fmt.Fprintf(env.Stdout, "  (%d unchanged file(s) hidden; use --show-unchanged)\n",
			counts[compiler.ChangeUnchanged])
	}
	if len(plan.Changes) == 0 {
		fmt.Fprintf(env.Stdout, "  (no files)\n")
	}

	fmt.Fprintf(env.Stdout, "\nProjection\n")
	for _, key := range []string{"exact", "adapted", "lossy", "blocked", "skipped-explicitly"} {
		fmt.Fprintf(env.Stdout, "  %-19s %d\n", key, plan.Outcomes[key])
	}
	if explain {
		fmt.Fprintf(env.Stdout, "\nEntities\n")
		for _, m := range plan.Mappings {
			fmt.Fprintf(env.Stdout, "  %-10s %-34s %s\n", m.Outcome, m.EntityID, adapters.ScopeLabel(m.Activation))
			if len(m.Files) > 0 {
				fmt.Fprintf(env.Stdout, "             -> %s\n", strings.Join(m.Files, ", "))
			}
			fmt.Fprintf(env.Stdout, "             %s\n", SanitizeLine(m.Explanation))
		}
	}

	printTokenReport(env, plan.TokenReport)
	PrintDiagnostics(env.Stdout, plan.Diagnostics, explain)

	if len(plan.Blocking()) > 0 {
		fmt.Fprintf(env.Stdout, "\nThis plan cannot be applied until the errors above are resolved.\n")
	}
}

func printTokenReport(env Env, r tokenestimate.Report) {
	fmt.Fprintf(env.Stdout, "\nContext estimate\n")
	fmt.Fprintf(env.Stdout, "  Canonical always-on:   ~%s tokens\n", FormatTokens(r.SourceAlwaysOn))
	fmt.Fprintf(env.Stdout, "  Target always-on:      ~%s tokens\n", FormatTokens(r.TargetAlwaysOn))
	fmt.Fprintf(env.Stdout, "  Largest target scope:  ~%s tokens", FormatTokens(r.LargestScope))
	if r.LargestScopeName != "" {
		fmt.Fprintf(env.Stdout, "  (%s)", SanitizeLine(r.LargestScopeName))
	}
	fmt.Fprintf(env.Stdout, "\n")
	fmt.Fprintf(env.Stdout, "  Worst-case request:    ~%s tokens\n", FormatTokens(r.WorstCaseRequest))
	fmt.Fprintf(env.Stdout, "  On demand:             ~%s tokens\n", FormatTokens(r.OnDemand))
	fmt.Fprintf(env.Stdout, "  Documentation only:    ~%s tokens\n", FormatTokens(r.DocumentationOnly))
	if r.ReductionPercent != nil {
		fmt.Fprintf(env.Stdout, "  Estimated reduction:    %d%%\n", *r.ReductionPercent)
	}
	fmt.Fprintf(env.Stdout, "\n  Approximation only. No provider tokenizer was used.\n  Method: %s\n", r.Method)
}
