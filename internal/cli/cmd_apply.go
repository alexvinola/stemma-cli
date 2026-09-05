package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/compiler"
	"github.com/alexvinola/stemma-cli/internal/store"
)

func runApply(ctx context.Context, env Env, args []string) int {
	fs := newFlagSet(env, "apply")
	jsonOut := fs.Bool("json", false, "emit JSON")
	dir := fs.String("workspace", "", "repository root (default: current directory)")
	var targets stringList
	fs.Var(&targets, "target", "target format to compile (repeatable, or comma-separated)")
	all := fs.Bool("all", false, "apply every target enabled in the canonical project")
	profilePath := fs.String("profile", "", "target profile to use")
	planPath := fs.String("plan", "", "apply a plan previously saved with --output-plan")
	yes := fs.Bool("yes", false, "apply without asking for confirmation")
	adopt := fs.Bool("adopt-untracked", false, "let Stemma take ownership of existing untracked files")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	if *planPath != "" && (*all || len(targets) > 1) {
		return fail(env, "apply", *jsonOut, ExitUsage,
			fmt.Errorf("--plan applies a single saved plan; drop --all and extra --target flags"), nil)
	}

	var selected []canonical.TargetFormat
	if *planPath == "" {
		var code int
		var err error
		selected, code, err = resolveTargetList(ctx, env, *dir, targets, *all)
		if err != nil {
			return fail(env, "apply", *jsonOut, code, err, nil)
		}
	} else {
		selected = []canonical.TargetFormat{""} // the target comes from the saved plan
	}

	exit := ExitOK
	for _, t := range selected {
		code := applyOne(ctx, env, *dir, string(t), *profilePath, *planPath, *jsonOut, *adopt, *yes)
		if code != ExitOK {
			// Stop at the first failure: later targets would compile against a
			// repository that is now in an unexpected state.
			return code
		}
	}
	return exit
}

// applyOne applies exactly one target, or one saved plan.
func applyOne(
	ctx context.Context, env Env, dir, target, profilePath, planPath string,
	jsonOut, adopt, yes bool,
) int {
	ws, err := openWorkspace(env, dir)
	if err != nil {
		return fail(env, "apply", jsonOut, ExitUsage, err, nil)
	}

	var plan compiler.Plan
	if planPath != "" {
		f, rerr := ws.ReadFile(ctx, planPath)
		if rerr != nil {
			return fail(env, "apply", jsonOut, ExitDiagnostics, rerr, nil)
		}
		saved, uerr := compiler.UnmarshalPlan(f.Data)
		if uerr != nil {
			return fail(env, "apply", jsonOut, ExitDiagnostics, uerr, nil)
		}
		if target != "" && string(saved.Target) != target {
			return fail(env, "apply", jsonOut, ExitUsage,
				fmt.Errorf("saved plan targets %q but --target says %q", saved.Target, target), nil)
		}
		// A saved plan states what compiling the project would produce. It is
		// not authority to write: the file lives in the repository, and the
		// workflow it exists for — commit a plan, review it, replay it in CI —
		// is exactly the one where a pull request can rewrite it.
		//
		// So rebuild from the canonical project, refuse unless the saved plan
		// agrees, and then apply the rebuild. The bytes written are always the
		// ones this binary just produced, never the ones the file carried, so
		// the ownership rules in classify() cannot be bypassed by editing it.
		rebuilt, _, code, berr := buildPlan(ctx, env, dir, string(saved.Target), profilePath, adopt)
		if berr != nil {
			return fail(env, "apply", jsonOut, code, berr, nil)
		}
		if verr := compiler.VerifyPlanMatches(saved, rebuilt); verr != nil {
			return fail(env, "apply", jsonOut, ExitStalePlan, verr, nil)
		}
		plan = rebuilt
	} else {
		var code int
		plan, _, code, err = buildPlan(ctx, env, dir, target, profilePath, adopt)
		if err != nil {
			return fail(env, "apply", jsonOut, code, err, nil)
		}
	}

	writable := plan.Writable()
	if len(plan.Blocking()) > 0 {
		if jsonOut {
			doc := NewEnvelope("apply", ExitDiagnostics, plan.Diagnostics, nil)
			doc.Error = "blocking diagnostics prevent apply"
			if werr := WriteJSON(env, doc); werr != nil {
				return ExitInternal
			}
			return ExitDiagnostics
		}
		PrintDiagnostics(env.Stderr, plan.Diagnostics, true)
		fmt.Fprintf(env.Stderr, "stemma: apply refused; resolve the errors above and re-plan.\n")
		return ExitDiagnostics
	}
	// A plan with nothing to write still has to run: the manifest is how Stemma
	// records which files it owns, and a target whose output is already correct
	// (the format you imported from, typically) would otherwise stay untracked
	// and be reported as a conflict on the next real change.
	needsOwnership := false
	for _, c := range plan.Changes {
		if c.Kind == compiler.ChangeUnchanged {
			needsOwnership = true
			break
		}
	}
	if len(writable) == 0 && !needsOwnership {
		if jsonOut {
			if werr := WriteJSON(env, NewEnvelope("apply", ExitOK, plan.Diagnostics,
				compiler.ApplyResult{Written: []string{}, Unchanged: []string{}, Skipped: []string{}})); werr != nil {
				return ExitInternal
			}
			return ExitOK
		}
		fmt.Fprintf(env.Stdout, "Nothing to apply: %s is already up to date.\n", plan.Target)
		return ExitOK
	}

	if !yes && len(writable) > 0 {
		if jsonOut || !env.StdinIsTTY {
			return fail(env, "apply", jsonOut, ExitUsage,
				fmt.Errorf("apply needs confirmation: re-run with --yes to authorize %s",
					Plural(len(writable), "file write", "file writes")), nil)
		}
		fmt.Fprintf(env.Stdout, "The following files will be written:\n")
		for _, c := range writable {
			fmt.Fprintf(env.Stdout, "  %-10s %s\n", c.Kind, c.Path)
		}
		ok, cerr := confirm(env, "Apply these changes?")
		if cerr != nil {
			return fail(env, "apply", false, ExitUsage, cerr, nil)
		}
		if !ok {
			fmt.Fprintf(env.Stdout, "Apply cancelled; nothing was written.\n")
			return ExitOK
		}
	}

	m, err := store.LoadManifest(ctx, ws)
	if err != nil {
		return fail(env, "apply", jsonOut, ExitDiagnostics, err, nil)
	}
	result, err := compiler.Apply(ctx, ws, plan, compiler.ApplyOptions{
		Manifest:     m,
		ManifestPath: store.ManifestFile,
	})
	if err != nil {
		code := exitCodeForError(err)
		if code == ExitDiagnostics && len(result.Diagnostics) == 0 {
			code = ExitWriteFailed
		}
		if isWriteFailure(err) {
			code = ExitWriteFailed
		}
		if jsonOut {
			doc := NewEnvelope("apply", code, result.Diagnostics, result)
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

	if jsonOut {
		if werr := WriteJSON(env, NewEnvelope("apply", ExitOK, result.Diagnostics, result)); werr != nil {
			return ExitInternal
		}
		return ExitOK
	}
	fmt.Fprintf(env.Stdout, "Applied %s for target %s\n",
		Plural(len(result.Written), "file", "files"), plan.Target)
	for _, p := range result.Written {
		fmt.Fprintf(env.Stdout, "  wrote      %s\n", p)
	}
	for _, p := range result.Unchanged {
		fmt.Fprintf(env.Stdout, "  unchanged  %s\n", p)
	}
	for _, p := range result.Skipped {
		fmt.Fprintf(env.Stdout, "  skipped    %s\n", p)
	}
	PrintDiagnostics(env.Stdout, result.Diagnostics, false)
	return ExitOK
}

func isWriteFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"rolled back", "rollback", "replace ", "temporary file"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
