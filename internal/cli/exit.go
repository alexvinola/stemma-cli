// Package cli implements the stemma command-line interface.
//
// Human output goes to stdout as plain text; logs and error prose go to
// stderr. With --json, stdout carries exactly one JSON document and nothing
// else, so it can be piped into other tools.
package cli

// Exit codes are part of the public contract. See docs/diagnostics.md.
const (
	// ExitOK: the command succeeded.
	ExitOK = 0
	// ExitDiagnostics: validation or compilation diagnostics prevented success.
	ExitDiagnostics = 1
	// ExitUsage: invalid command-line usage.
	ExitUsage = 2
	// ExitUnsupportedTarget: the requested target is unknown or unavailable.
	ExitUnsupportedTarget = 3
	// ExitStalePlan: the plan was stale or the filesystem conflicted.
	ExitStalePlan = 4
	// ExitWriteFailed: a safe write failed or was rolled back.
	ExitWriteFailed = 5
	// ExitInternal: a compiler invariant failed.
	ExitInternal = 6
)

// ExitCodeName returns a stable name for an exit code.
func ExitCodeName(code int) string {
	switch code {
	case ExitOK:
		return "ok"
	case ExitDiagnostics:
		return "diagnostics"
	case ExitUsage:
		return "usage"
	case ExitUnsupportedTarget:
		return "unsupported-target"
	case ExitStalePlan:
		return "stale-plan"
	case ExitWriteFailed:
		return "write-failed"
	case ExitInternal:
		return "internal"
	default:
		return "unknown"
	}
}
