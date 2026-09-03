// Package diagnostics defines Stemma's stable diagnostic contract.
//
// Diagnostic codes are part of the public CLI surface. Human-readable text may
// be improved over time; codes must not be renamed casually after release.
package diagnostics

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Severity classifies a diagnostic.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Rank returns a deterministic ordering weight (errors first).
func (s Severity) Rank() int {
	switch s {
	case SeverityError:
		return 0
	case SeverityWarning:
		return 1
	case SeverityInfo:
		return 2
	default:
		return 3
	}
}

// Code is a stable diagnostic identifier.
type Code string

// Stable diagnostic codes. Do not rename these after release.
const (
	// 1xxx: discovery and parsing.
	UnrecognizedFormat  Code = "STEMMA1001_UNRECOGNIZED_FORMAT"
	FileLimitReached    Code = "STEMMA1002_LIMIT_REACHED"
	FileUnreadable      Code = "STEMMA1003_FILE_UNREADABLE"
	InvalidEncoding     Code = "STEMMA1004_INVALID_ENCODING"
	InvalidFrontMatter  Code = "STEMMA1101_INVALID_FRONT_MATTER"
	FrontMatterTooLarge Code = "STEMMA1102_FRONT_MATTER_TOO_LARGE"
	UnsafeYAMLConstruct Code = "STEMMA1103_UNSAFE_YAML_CONSTRUCT"
	UnknownSectionKept  Code = "STEMMA1201_UNKNOWN_SECTION_PRESERVED"
	UnknownKeysKept     Code = "STEMMA1202_UNKNOWN_KEYS_PRESERVED"
	OpaqueBlockKept     Code = "STEMMA1203_OPAQUE_BLOCK_PRESERVED"
	MultipleSources     Code = "STEMMA1301_MULTIPLE_SOURCES"
	NoSourcesDetected   Code = "STEMMA1302_NO_SOURCES_DETECTED"
	MixedLineEndings    Code = "STEMMA1401_MIXED_LINE_ENDINGS"
	InvalidAgentJSON    Code = "STEMMA1501_INVALID_AGENT_JSON"
	DuplicateJSONKey    Code = "STEMMA1502_DUPLICATE_JSON_KEY"

	// 2xxx: canonical validation.
	DuplicateEntityID  Code = "STEMMA2001_DUPLICATE_ENTITY_ID"
	InvalidEntityID    Code = "STEMMA2002_INVALID_ENTITY_ID"
	MissingRequired    Code = "STEMMA2003_MISSING_REQUIRED_FIELD"
	UnknownSchema      Code = "STEMMA2004_UNSUPPORTED_SCHEMA_VERSION"
	InvalidActivation  Code = "STEMMA2005_INVALID_ACTIVATION"
	InvalidGlob        Code = "STEMMA2101_INVALID_GLOB"
	DanglingProvenance Code = "STEMMA2201_DANGLING_PROVENANCE"
	ProfileInvalid     Code = "STEMMA2301_INVALID_PROFILE"
	ProfileUnknownID   Code = "STEMMA2302_PROFILE_OVERRIDES_UNKNOWN_ENTITY"
	ManifestInvalid    Code = "STEMMA2401_MANIFEST_INCONSISTENT"

	// 3xxx: projection.
	TargetUnavailable     Code = "STEMMA3001_TARGET_UNAVAILABLE"
	TargetNotEnabled      Code = "STEMMA3002_TARGET_NOT_ENABLED"
	ExcludeNotRepresent   Code = "STEMMA3101_EXCLUDE_NOT_REPRESENTABLE"
	DirectoryScopeAmbig   Code = "STEMMA3201_DIRECTORY_SCOPE_AMBIGUOUS"
	DirectoryScopeBroader Code = "STEMMA3202_DIRECTORY_SCOPE_BROADENED"
	AgentToolsNeedReview  Code = "STEMMA3301_AGENT_TOOLS_REQUIRE_REVIEW"
	AgentNotNative        Code = "STEMMA3302_AGENT_NOT_NATIVELY_SUPPORTED"
	// 3401 is unassigned: every implemented provider can express several
	// include patterns, so no adapter needs to report adapting them.
	OnDemandAdapted        Code = "STEMMA3402_ON_DEMAND_ADAPTED"
	OpaqueNotReemitted     Code = "STEMMA3501_OPAQUE_BLOCK_NOT_REEMITTED"
	TargetOverridesContent Code = "STEMMA3601_TARGET_CONTENT_OVERRIDDEN"
	RegeneratedFile        Code = "STEMMA3701_FILE_REGENERATED"

	// 4xxx: filesystem and transactions.
	PathEscape          Code = "STEMMA4001_PATH_ESCAPE"
	SymlinkRejected     Code = "STEMMA4002_SYMLINK_REJECTED"
	StalePlan           Code = "STEMMA4101_STALE_PLAN"
	WriteRolledBack     Code = "STEMMA4201_WRITE_ROLLED_BACK"
	RecoveryDataWritten Code = "STEMMA4202_RECOVERY_DATA_WRITTEN"
	UntrackedDestConfl  Code = "STEMMA4301_UNTRACKED_DESTINATION"
	DeleteProposed      Code = "STEMMA4401_DELETE_PROPOSED"
	OutputStale         Code = "STEMMA4501_OUTPUT_STALE"

	// 5xxx: budgets.
	TokenBudgetExceeded  Code = "STEMMA5001_TOKEN_BUDGET_EXCEEDED"
	AlwaysOnContextLarge Code = "STEMMA5002_ALWAYS_ON_CONTEXT_LARGE"

	// 6xxx: internal invariants.
	InternalInvariant Code = "STEMMA6001_INTERNAL_INVARIANT"
)

// Position identifies a location inside a source file. Zero values mean the
// position is unknown and must not be rendered.
type Position struct {
	Line   int `json:"line,omitempty"`
	Column int `json:"column,omitempty"`
}

// Diagnostic is a single structured message.
type Diagnostic struct {
	Code        Code     `json:"code"`
	Severity    Severity `json:"severity"`
	Summary     string   `json:"summary"`
	Detail      string   `json:"detail,omitempty"`
	Path        string   `json:"path,omitempty"`
	Position    Position `json:"position,omitzero"`
	EntityID    string   `json:"entityId,omitempty"`
	Target      string   `json:"target,omitempty"`
	Suggestion  string   `json:"suggestion,omitempty"`
	Blocking    bool     `json:"blocking"`
	Fingerprint string   `json:"fingerprint"`
}

// New builds a diagnostic and computes its stable fingerprint.
func New(code Code, sev Severity, summary string) Diagnostic {
	d := Diagnostic{Code: code, Severity: sev, Summary: summary}
	d.Blocking = sev == SeverityError
	d.Fingerprint = d.computeFingerprint()
	return d
}

// WithPath returns a copy anchored to a repository-relative path.
func (d Diagnostic) WithPath(path string) Diagnostic {
	d.Path = path
	d.Fingerprint = d.computeFingerprint()
	return d
}

// WithPosition returns a copy anchored to a line and column (1-based).
func (d Diagnostic) WithPosition(line, col int) Diagnostic {
	d.Position = Position{Line: line, Column: col}
	d.Fingerprint = d.computeFingerprint()
	return d
}

// WithEntity returns a copy anchored to a canonical entity.
func (d Diagnostic) WithEntity(id string) Diagnostic {
	d.EntityID = id
	d.Fingerprint = d.computeFingerprint()
	return d
}

// WithTarget returns a copy anchored to a target format.
func (d Diagnostic) WithTarget(target string) Diagnostic {
	d.Target = target
	d.Fingerprint = d.computeFingerprint()
	return d
}

// WithDetail returns a copy carrying a longer explanation.
func (d Diagnostic) WithDetail(format string, args ...any) Diagnostic {
	d.Detail = fmt.Sprintf(format, args...)
	d.Fingerprint = d.computeFingerprint()
	return d
}

// WithSuggestion returns a copy carrying a suggested resolution.
func (d Diagnostic) WithSuggestion(format string, args ...any) Diagnostic {
	d.Suggestion = fmt.Sprintf(format, args...)
	d.Fingerprint = d.computeFingerprint()
	return d
}

// WithBlocking returns a copy that explicitly blocks (or unblocks) apply.
func (d Diagnostic) WithBlocking(blocking bool) Diagnostic {
	d.Blocking = blocking
	d.Fingerprint = d.computeFingerprint()
	return d
}

// computeFingerprint derives a stable identifier for explicit acceptance.
//
// The fingerprint intentionally excludes free-form human prose (summary,
// detail, suggestion) so that improving a message does not invalidate a
// previously accepted diagnostic.
func (d Diagnostic) computeFingerprint() string {
	h := sha256.New()
	for _, part := range []string{
		string(d.Code),
		string(d.Severity),
		d.Path,
		d.EntityID,
		d.Target,
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return "dg_" + hex.EncodeToString(h.Sum(nil))[:16]
}

// Bag accumulates diagnostics in a deterministic way.
type Bag struct {
	items []Diagnostic
}

// Add appends a diagnostic.
func (b *Bag) Add(d Diagnostic) {
	if b == nil {
		return
	}
	b.items = append(b.items, d)
}

// Extend appends many diagnostics.
func (b *Bag) Extend(ds []Diagnostic) {
	for _, d := range ds {
		b.Add(d)
	}
}

// Len reports how many diagnostics were collected.
func (b *Bag) Len() int {
	if b == nil {
		return 0
	}
	return len(b.items)
}

// Items returns a sorted, de-duplicated copy of the collected diagnostics.
func (b *Bag) Items() []Diagnostic {
	if b == nil || len(b.items) == 0 {
		return []Diagnostic{}
	}
	out := make([]Diagnostic, len(b.items))
	copy(out, b.items)
	Sort(out)
	return dedupe(out)
}

func dedupe(in []Diagnostic) []Diagnostic {
	out := in[:0:0]
	seen := make(map[string]struct{}, len(in))
	for _, d := range in {
		key := strings.Join([]string{
			string(d.Code), d.Path, d.EntityID, d.Target, d.Summary, d.Detail,
			fmt.Sprintf("%d:%d", d.Position.Line, d.Position.Column),
		}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, d)
	}
	return out
}

// Sort orders diagnostics deterministically: severity, then code, then path,
// then position, then entity, then target, then summary.
func Sort(ds []Diagnostic) {
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i], ds[j]
		if a.Severity != b.Severity {
			return a.Severity.Rank() < b.Severity.Rank()
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Position.Line != b.Position.Line {
			return a.Position.Line < b.Position.Line
		}
		if a.Position.Column != b.Position.Column {
			return a.Position.Column < b.Position.Column
		}
		if a.EntityID != b.EntityID {
			return a.EntityID < b.EntityID
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		return a.Summary < b.Summary
	})
}

// HasBlocking reports whether any diagnostic blocks apply.
func HasBlocking(ds []Diagnostic) bool {
	for _, d := range ds {
		if d.Blocking {
			return true
		}
	}
	return false
}

// HasSeverity reports whether any diagnostic has the given severity.
func HasSeverity(ds []Diagnostic, sev Severity) bool {
	for _, d := range ds {
		if d.Severity == sev {
			return true
		}
	}
	return false
}

// Filter returns the diagnostics accepted by keep.
func Filter(ds []Diagnostic, keep func(Diagnostic) bool) []Diagnostic {
	out := make([]Diagnostic, 0, len(ds))
	for _, d := range ds {
		if keep(d) {
			out = append(out, d)
		}
	}
	return out
}

// Accept marks diagnostics whose fingerprint appears in accepted as
// non-blocking and downgrades them to info. It is used for profile-level
// acceptance of known lossy mappings.
func Accept(ds []Diagnostic, accepted []string) []Diagnostic {
	if len(accepted) == 0 {
		return ds
	}
	set := make(map[string]struct{}, len(accepted))
	for _, a := range accepted {
		set[a] = struct{}{}
	}
	out := make([]Diagnostic, 0, len(ds))
	for _, d := range ds {
		if _, ok := set[d.Fingerprint]; ok {
			d.Severity = SeverityInfo
			d.Blocking = false
			d.Detail = strings.TrimSpace(d.Detail + " (accepted in target profile)")
		}
		out = append(out, d)
	}
	return out
}
