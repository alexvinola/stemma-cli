package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/discovery"
)

func runScan(ctx context.Context, env Env, args []string) int {
	fs := newFlagSet(env, "scan")
	jsonOut := fs.Bool("json", false, "emit JSON")
	dir := fs.String("workspace", "", "repository root (default: current directory)")
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
		return fail(env, "scan", *jsonOut, ExitUsage, err, nil)
	}
	result, err := discovery.Scan(ctx, ws)
	if err != nil {
		return fail(env, "scan", *jsonOut, ExitDiagnostics, err, nil)
	}

	if *jsonOut {
		if err := WriteJSON(env, NewEnvelope("scan", ExitOK, result.Diagnostics, result)); err != nil {
			return ExitInternal
		}
		return ExitOK
	}

	if len(result.Detections) == 0 {
		fmt.Fprintf(env.Stdout, "No supported agent configuration found.\n")
		fmt.Fprintf(env.Stdout, "Stemma looks only at these paths:\n")
		for _, p := range discovery.Registry() {
			fmt.Fprintf(env.Stdout, "  %s\n", p)
		}
	} else {
		fmt.Fprintf(env.Stdout, "Detected agent configuration\n")
		for _, d := range result.Detections {
			fmt.Fprintf(env.Stdout, "\n  %s  (confidence: %s, %s)\n",
				d.Format, d.Confidence, Plural(len(d.Files), "file", "files"))
			for _, f := range d.Files {
				line := fmt.Sprintf("    %-52s %s", SanitizeLine(f.Path), f.Role)
				if len(f.AlsoReadBy) > 0 {
					readers := make([]string, 0, len(f.AlsoReadBy))
					for _, r := range f.AlsoReadBy {
						readers = append(readers, string(r))
					}
					line += " (also read by " + strings.Join(readers, ", ") + ")"
				}
				fmt.Fprintln(env.Stdout, line)
			}
		}
	}
	fmt.Fprintf(env.Stdout, "\nVisited %s; skipped %s.\n",
		Plural(result.FilesVisited, "file", "files"),
		Plural(len(result.SkippedDirs), "directory", "directories"))
	if !result.Complete {
		fmt.Fprintf(env.Stdout, "Scan INCOMPLETE: limits reached: %s.\n", strings.Join(result.LimitsReached, ", "))
		fmt.Fprintf(env.Stdout, "Configuration may exist that was not discovered; "+
			"stemma import refuses this scan unless --allow-incomplete-scan is given.\n")
	}
	fmt.Fprintf(env.Stdout, "No files were read or modified.\n")
	PrintDiagnostics(env.Stdout, result.Diagnostics, false)
	return ExitOK
}
