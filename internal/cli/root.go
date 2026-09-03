package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/capabilities"
	"github.com/alexvinola/stemma/internal/compiler"
	"github.com/alexvinola/stemma/internal/workspace"
)

const usage = `stemma - a deterministic compiler for coding-agent context

Usage:
  stemma <command> [flags]

Commands:
  init        Create the .stemma project structure
  scan        Detect supported agent configuration (read-only)
  import      Import provider configuration into the canonical project
  validate    Validate the canonical project, profiles and manifest
  plan        Compile a target and show what would change (read-only)
  apply       Apply a reviewed plan transactionally
  check       Verify that generated output is up to date (for CI)
  explain     Explain how one entity maps to a target
  version     Print version and compatibility information

Common flags:
  --json          Emit a single machine-readable JSON document on stdout
  --workspace DIR Use DIR as the repository root (default: current directory)

Exit codes:
  0 success                     4 stale plan or filesystem conflict
  1 diagnostics prevented it    5 safe write failed or was rolled back
  2 invalid usage               6 internal compiler invariant failed
  3 unsupported target

Run "stemma <command> --help" for command-specific flags.
`

// Run executes one CLI invocation and returns the process exit code.
func Run(ctx context.Context, env Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, usage)
		return ExitUsage
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(env.Stdout, usage)
		return ExitOK
	case "init":
		return runInit(ctx, env, args[1:])
	case "scan":
		return runScan(ctx, env, args[1:])
	case "import":
		return runImport(ctx, env, args[1:])
	case "validate":
		return runValidate(ctx, env, args[1:])
	case "plan":
		return runPlan(ctx, env, args[1:])
	case "apply":
		return runApply(ctx, env, args[1:])
	case "check":
		return runCheck(ctx, env, args[1:])
	case "explain":
		return runExplain(ctx, env, args[1:])
	case "version":
		return runVersion(ctx, env, args[1:])
	default:
		fmt.Fprintf(env.Stderr, "stemma: unknown command %q\n\n", SanitizeLine(args[0]))
		fmt.Fprint(env.Stderr, usage)
		return ExitUsage
	}
}

// newFlagSet builds a flag set that reports errors to stderr without exiting.
func newFlagSet(env Env, name string) *flag.FlagSet {
	fs := flag.NewFlagSet("stemma "+name, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	return fs
}

// parseFlags parses arguments, mapping flag errors to the usage exit code.
func parseFlags(fs *flag.FlagSet, args []string) (int, bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK, false
		}
		return ExitUsage, false
	}
	return ExitOK, true
}

// parsePositional parses flags that may appear before or after positional
// arguments. Go's flag package stops at the first non-flag argument, so the
// remainder is re-parsed until no positional arguments are left.
func parsePositional(fs *flag.FlagSet, args []string) (positional []string, code int, ok bool) {
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, ExitOK, false
			}
			return nil, ExitUsage, false
		}
		if fs.NArg() == 0 {
			return positional, ExitOK, true
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
}

// openWorkspace resolves the repository root.
func openWorkspace(env Env, dir string) (*workspace.Workspace, error) {
	if dir == "" {
		dir = env.WorkingDir
	}
	if dir == "" {
		dir = "."
	}
	return workspace.Open(dir, workspace.DefaultLimits())
}

// resolveTarget validates a target identifier and its availability.
func resolveTarget(name string) (canonical.TargetFormat, error) {
	if name == "" {
		return "", fmt.Errorf("a target is required: use --target with one of %s",
			strings.Join(targetNames(capabilities.AvailableTargets()), ", "))
	}
	t := canonical.TargetFormat(name)
	if !canonical.KnownTarget(t) {
		return "", fmt.Errorf("unknown target %q: known targets are %s",
			name, strings.Join(targetNames(canonical.AllTargets()), ", "))
	}
	if !capabilities.Available(t) {
		caps := capabilities.MustFor(t)
		return t, fmt.Errorf("target %q is declared but not implemented in this build: %s",
			name, caps.Notes)
	}
	return t, nil
}

func targetNames(ts []canonical.TargetFormat) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, string(t))
	}
	return out
}

// exitCodeForError maps a compiler error to a stable exit code.
func exitCodeForError(err error) int {
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, compiler.ErrTargetUnavailable):
		return ExitUnsupportedTarget
	case errors.Is(err, compiler.ErrStalePlan):
		return ExitStalePlan
	case errors.Is(err, compiler.ErrInvariant):
		return ExitInternal
	case errors.Is(err, compiler.ErrBlocked):
		return ExitDiagnostics
	case errors.Is(err, workspace.ErrPathEscape), errors.Is(err, workspace.ErrSymlink):
		return ExitStalePlan
	default:
		return ExitDiagnostics
	}
}

// fail reports an error in the requested output mode.
func fail(env Env, command string, jsonOut bool, code int, err error, data any) int {
	if jsonOut {
		doc := NewEnvelope(command, code, nil, data)
		doc.Error = err.Error()
		if writeErr := WriteJSON(env, doc); writeErr != nil {
			fmt.Fprintf(env.Stderr, "stemma: %v\n", writeErr)
			return ExitInternal
		}
		return code
	}
	fmt.Fprintf(env.Stderr, "stemma: %s\n", SanitizeLine(err.Error()))
	return code
}

// confirm asks the user to approve an operation.
func confirm(env Env, prompt string) (bool, error) {
	if !env.StdinIsTTY {
		return false, errors.New("stdin is not a terminal: pass --yes to authorize this operation")
	}
	fmt.Fprintf(env.Stdout, "%s [y/N]: ", prompt)
	var answer string
	if _, err := fmt.Fscanln(env.Stdin, &answer); err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, nil
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}
