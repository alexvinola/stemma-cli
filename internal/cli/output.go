package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	"github.com/alexvinola/stemma/internal/diagnostics"
	"github.com/alexvinola/stemma/internal/version"
)

// Env carries the process environment a command needs. Commands never touch
// os.Stdout, os.Stderr or os.Exit directly, which keeps them testable.
type Env struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	// StdinIsTTY reports whether interactive confirmation is possible.
	StdinIsTTY bool
	// WorkingDir is the directory the command runs in.
	WorkingDir string
}

// Envelope is the versioned JSON output contract shared by every command.
type Envelope struct {
	SchemaVersion int    `json:"schemaVersion"`
	StemmaVersion string `json:"stemmaVersion"`
	Command       string `json:"command"`
	// Status is "ok" or "error".
	Status string `json:"status"`
	// ExitCode is the process exit code that accompanies this document.
	ExitCode int `json:"exitCode"`
	// Error is a machine-readable failure summary, empty on success.
	Error string `json:"error,omitempty"`
	// Diagnostics is always present, sorted and de-duplicated.
	Diagnostics []diagnostics.Diagnostic `json:"diagnostics"`
	// Data holds the command-specific payload.
	Data any `json:"data,omitempty"`
}

// NewEnvelope builds an envelope for a command.
func NewEnvelope(command string, exitCode int, diags []diagnostics.Diagnostic, data any) Envelope {
	status := "ok"
	if exitCode != ExitOK {
		status = "error"
	}
	if diags == nil {
		diags = []diagnostics.Diagnostic{}
	}
	sorted := append([]diagnostics.Diagnostic{}, diags...)
	diagnostics.Sort(sorted)
	return Envelope{
		SchemaVersion: version.ReportSchemaVersion,
		StemmaVersion: version.Version,
		Command:       command,
		Status:        status,
		ExitCode:      exitCode,
		Diagnostics:   sorted,
		Data:          data,
	}
}

// WriteJSON writes exactly one JSON document to stdout.
func WriteJSON(env Env, doc Envelope) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := env.Stdout.Write(buf.Bytes())
	return err
}

// Sanitize removes terminal control sequences from untrusted text before it is
// printed. Repository files are untrusted input.
func Sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == 0x1b:
			b.WriteString("\\e")
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\x%02x", r)
		case unicode.In(r, unicode.Cf) && r != 0x200d:
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SanitizeLine is Sanitize for single-line output: newlines become spaces.
func SanitizeLine(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(Sanitize(s), "\n", " "), "\t", " ")
}

// PrintDiagnostics renders diagnostics for humans, errors first.
func PrintDiagnostics(w io.Writer, diags []diagnostics.Diagnostic, verbose bool) {
	if len(diags) == 0 {
		return
	}
	sorted := append([]diagnostics.Diagnostic{}, diags...)
	diagnostics.Sort(sorted)
	var errs, warns, infos []diagnostics.Diagnostic
	for _, d := range sorted {
		switch d.Severity {
		case diagnostics.SeverityError:
			errs = append(errs, d)
		case diagnostics.SeverityWarning:
			warns = append(warns, d)
		default:
			infos = append(infos, d)
		}
	}
	section := func(title string, items []diagnostics.Diagnostic) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(w, "\n%s (%d)\n", title, len(items))
		for _, d := range items {
			fmt.Fprintf(w, "  %s  %s\n", d.Code, SanitizeLine(d.Summary))
			if loc := location(d); loc != "" {
				fmt.Fprintf(w, "      at %s\n", loc)
			}
			if verbose && d.Detail != "" {
				fmt.Fprintf(w, "      %s\n", SanitizeLine(d.Detail))
			}
			if verbose && d.Suggestion != "" {
				fmt.Fprintf(w, "      suggestion: %s\n", SanitizeLine(d.Suggestion))
			}
		}
	}
	section("Errors", errs)
	section("Warnings", warns)
	if verbose {
		section("Notes", infos)
	} else if len(infos) > 0 {
		fmt.Fprintf(w, "\nNotes (%d)  run with --explain for details\n", len(infos))
	}
}

func location(d diagnostics.Diagnostic) string {
	var parts []string
	if d.Path != "" {
		loc := d.Path
		if d.Position.Line > 0 {
			loc += fmt.Sprintf(":%d", d.Position.Line)
			if d.Position.Column > 0 {
				loc += fmt.Sprintf(":%d", d.Position.Column)
			}
		}
		parts = append(parts, loc)
	}
	if d.EntityID != "" {
		parts = append(parts, "entity "+d.EntityID)
	}
	if d.Target != "" {
		parts = append(parts, "target "+d.Target)
	}
	return strings.Join(parts, ", ")
}

// SummarizeDiagnostics counts diagnostics per severity.
func SummarizeDiagnostics(diags []diagnostics.Diagnostic) (errs, warns, infos int) {
	for _, d := range diags {
		switch d.Severity {
		case diagnostics.SeverityError:
			errs++
		case diagnostics.SeverityWarning:
			warns++
		default:
			infos++
		}
	}
	return
}

// SortedKeys returns map keys in deterministic order.
func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Plural renders "1 file" / "2 files".
func Plural(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// FormatTokens renders an approximate token count with thousands separators.
func FormatTokens(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return strings.Join(parts, ",")
}
