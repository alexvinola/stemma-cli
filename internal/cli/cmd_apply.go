package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/alexvinola/stemma/internal/compiler"
	"github.com/alexvinola/stemma/internal/store"
)

func runApply(ctx context.Context, env Env, args []string) int {
	fs := newFlagSet(env, "apply")
	jsonOut := fs.Bool("json", false, "emit JSON")
	dir := fs.String("workspace", "", "repository root (default: current directory)")
	target := fs.String("target", "", "target format to compile")
	profilePath := fs.String("profile", "", "target profile to use")
	planPath := fs.String("plan", "", "apply a plan previously saved with --output-plan")
	yes := fs.Bool("yes", false, "apply without asking for confirmation")
	adopt := fs.Bool("adopt-untracked", false, "let Stemma take ownership of existing untracked files")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	ws, err := openWorkspace(env, *dir)
	if err != nil {
		return fail(env, "apply", *jsonOut, ExitUsage, err, nil)
	}

	var plan compiler.Plan
	if *planPath != "" {
		f, rerr := ws.ReadFile(ctx, *planPath)
		if rerr != nil {
			return fail(env, "apply", *jsonOut, ExitDiagnostics, rerr, nil)
		}
		plan, err = compiler.UnmarshalPlan(f.Data)
		if err != nil {
			return fail(env, "apply", *jsonOut, ExitDiagnostics, err, nil)
		}
		if *target != "" && string(plan.Target) != *target {
			return fail(env, "apply", *jsonOut, ExitUsage,
				fmt.Errorf("saved plan targets %q but --target says %q", plan.Target, *target), nil)
		}
	} else {
		var code int
		plan, _, code, err = buildPlan(ctx, env, *dir, *target, *profilePath, *adopt)
		if err != nil {
			return fail(env, "apply", *jsonOut, code, err, nil)
		}
	}

	writable := plan.Writable()
	if len(plan.Blocking()) > 0 {
		if *jsonOut {
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
	if len(writable) == 0 {
		if *jsonOut {
			if werr := WriteJSON(env, NewEnvelope("apply", ExitOK, plan.Diagnostics,
				compiler.ApplyResult{Written: []string{}, Unchanged: []string{}, Skipped: []string{}})); werr != nil {
				return ExitInternal
			}
			return ExitOK
		}
		fmt.Fprintf(env.Stdout, "Nothing to apply: %s is already up to date.\n", plan.Target)
		return ExitOK
	}

	if !*yes {
		if *jsonOut || !env.StdinIsTTY {
			return fail(env, "apply", *jsonOut, ExitUsage,
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
		return fail(env, "apply", *jsonOut, ExitDiagnostics, err, nil)
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
		if *jsonOut {
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

	if *jsonOut {
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
