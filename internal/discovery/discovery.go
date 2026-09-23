// Package discovery detects provider configuration files.
//
// Discovery inspects file *paths* only. Source files are never opened: a file
// is read later only if it matched a registered configuration pattern.
package discovery

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/globs"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// Role describes what a matched configuration file is used for.
type Role string

const (
	RoleRootInstructions   Role = "root-instructions"
	RoleNestedInstructions Role = "nested-instructions"
	RoleScopedInstructions Role = "scoped-instructions"
	RoleRule               Role = "rule"
	RoleSteering           Role = "steering"
	RolePrompt             Role = "prompt"
	RoleSkill              Role = "skill"
	RoleAgent              Role = "agent"
	RoleOverride           Role = "override-instructions"
)

// Match is a discovered configuration file.
type Match struct {
	Path   string                 `json:"path"`
	Format canonical.TargetFormat `json:"format"`
	Role   Role                   `json:"role"`
	// AlsoReadBy lists other providers documented to read this file, whose
	// Stemma adapter does not import it. Format stays the only owner: the
	// file is imported (and written) by that adapter alone.
	AlsoReadBy []canonical.TargetFormat `json:"alsoReadBy,omitempty"`
}

// Confidence describes how sure detection is that a format is in use.
type Confidence string

const (
	// ConfidenceHigh means a primary entry-point file was found.
	ConfidenceHigh Confidence = "high"
	// ConfidenceMedium means only secondary files were found.
	ConfidenceMedium Confidence = "medium"
)

// Detection groups the files found for one provider format.
type Detection struct {
	Format     canonical.TargetFormat `json:"format"`
	Confidence Confidence             `json:"confidence"`
	Files      []Match                `json:"files"`
}

// Result is the outcome of a scan.
type Result struct {
	Detections  []Detection `json:"detections"`
	SkippedDirs []string    `json:"skippedDirectories"`
	// LimitsReached names the walk limits that truncated the scan.
	LimitsReached []string `json:"limitsReached"`
	// UnreadableDirs lists (bounded, sorted) directories whose entries could
	// not be read; UnreadableCount counts all of them.
	UnreadableDirs  []string `json:"unreadableDirectories"`
	UnreadableCount int      `json:"unreadableDirectoryCount"`
	// Complete is false when a limit truncated the scan or a directory could
	// not be read, so configuration may exist that was never seen. It never
	// covers the fixed skip list or symbolic links, which are not inspected
	// by design. Import refuses an incomplete scan unless the caller
	// explicitly allows it.
	Complete bool `json:"complete"`
	// FilesVisited counts every regular file inspected, candidates or not.
	FilesVisited int `json:"filesVisited"`
	// EntriesVisited counts every directory entry inspected.
	EntriesVisited int                      `json:"entriesVisited"`
	Diagnostics    []diagnostics.Diagnostic `json:"diagnostics"`
}

// rule maps a path pattern to a format and role.
type rule struct {
	pattern string
	format  canonical.TargetFormat
	role    Role
	primary bool
}

// registry is the complete list of paths Stemma will ever open. Order matters:
// the first matching rule wins, so more specific patterns come first. The
// recursive "**/" instruction patterns come last, so that a provider's own
// directories (.claude/rules, .kiro/steering, ...) always win over them.
//
// Every pattern ends in ".md" or ".json"; candidateExtension relies on it and
// TestRegistryExtensions enforces it.
var registry = []rule{
	// GitHub Copilot.
	{".github/copilot-instructions.md", canonical.TargetCopilot, RoleRootInstructions, true},
	{".github/instructions/**/*.instructions.md", canonical.TargetCopilot, RoleScopedInstructions, true},
	{".github/prompts/**/*.prompt.md", canonical.TargetCopilot, RolePrompt, false},
	{".github/skills/*/SKILL.md", canonical.TargetCopilot, RoleSkill, false},
	{".github/agents/*.md", canonical.TargetCopilot, RoleAgent, false},

	// Claude Code.
	{"CLAUDE.md", canonical.TargetClaude, RoleRootInstructions, true},
	{".claude/CLAUDE.md", canonical.TargetClaude, RoleRootInstructions, true},
	{".claude/rules/**/*.md", canonical.TargetClaude, RoleRule, true},
	{".claude/skills/*/SKILL.md", canonical.TargetClaude, RoleSkill, false},
	{".claude/agents/*.md", canonical.TargetClaude, RoleAgent, false},

	// Codex / AGENTS.md.
	{"AGENTS.md", canonical.TargetCodex, RoleRootInstructions, true},
	{"AGENTS.override.md", canonical.TargetCodex, RoleOverride, false},
	{".agents/skills/*/SKILL.md", canonical.TargetCodex, RoleSkill, false},

	// Kiro.
	{".kiro/steering/**/*.md", canonical.TargetKiro, RoleSteering, true},
	{".kiro/skills/*/SKILL.md", canonical.TargetKiro, RoleSkill, false},
	{".kiro/agents/*.json", canonical.TargetKiro, RoleAgent, false},

	// Directory-scoped instruction files, anywhere below the root.
	{"**/CLAUDE.md", canonical.TargetClaude, RoleNestedInstructions, false},
	{"**/AGENTS.md", canonical.TargetCodex, RoleNestedInstructions, false},
	{"**/AGENTS.override.md", canonical.TargetCodex, RoleOverride, false},
}

// sharedReader records a provider that is documented to read a file another
// adapter owns, but whose Stemma adapter does not import it. It never changes
// which adapter owns the file; it only makes the overlap visible, so that a
// file is not silently left out when that provider is imported explicitly.
type sharedReader struct {
	pattern string
	format  canonical.TargetFormat
}

// sharedReaders lists the overlaps Stemma reports. Kiro reads AGENTS.md at the
// workspace root and in subdirectories (https://kiro.dev/docs/steering/,
// verified 2026-09-23); Stemma models AGENTS.md with the Codex adapter only,
// so that two targets never own one file.
var sharedReaders = []sharedReader{
	{"AGENTS.md", canonical.TargetKiro},
	{"**/AGENTS.md", canonical.TargetKiro},
}

// Registry returns the registered configuration patterns, sorted, for
// documentation and tests.
func Registry() []string {
	out := make([]string, 0, len(registry))
	for _, r := range registry {
		out = append(out, r.pattern)
	}
	sort.Strings(out)
	return out
}

// Classify returns the format and role of a repository-relative path, or
// ok=false when the path is not a registered configuration file.
//
// A path that is not a safe, normalized repository path never classifies, so
// discovery can never hand the importer something the workspace layer would
// refuse to open.
func Classify(rel string) (canonical.TargetFormat, Role, bool) {
	clean, err := workspace.NormalizeRel(rel)
	if err != nil || clean != rel {
		return "", "", false
	}
	// A .claude/rules file must not also be picked up as a nested CLAUDE.md,
	// and a nested AGENTS.md must not shadow the root one; ordering handles it.
	if !candidateExtension(rel) {
		return "", "", false
	}
	for _, r := range registry {
		if globs.Match(r.pattern, rel) {
			return r.format, r.role, true
		}
	}
	return "", "", false
}

// IsRegistered reports whether Stemma is allowed to open the path.
func IsRegistered(rel string) bool {
	_, _, ok := Classify(rel)
	return ok
}

// AlsoReadBy returns the other providers documented to read a registered
// path, in deterministic target order, excluding the path's owner. It returns
// nil for an unregistered path.
func AlsoReadBy(rel string) []canonical.TargetFormat {
	owner, _, ok := Classify(rel)
	if !ok {
		return nil
	}
	var out []canonical.TargetFormat
	for _, r := range sharedReaders {
		if r.format == owner || !globs.Match(r.pattern, rel) {
			continue
		}
		dup := false
		for _, f := range out {
			dup = dup || f == r.format
		}
		if !dup {
			out = append(out, r.format)
		}
	}
	canonical.SortTargets(out)
	return out
}

// candidateExtension is a cheap path-only pre-filter: every registered
// pattern ends in ".md" or ".json", so no other file can ever classify.
func candidateExtension(rel string) bool {
	return strings.HasSuffix(rel, ".md") || strings.HasSuffix(rel, ".json")
}

// Scan walks the workspace and classifies configuration files. It never opens
// a file.
//
// Only registered paths count against the workspace's candidate budget
// (MaxFiles); every other entry only counts against MaxEntries. Source code
// therefore cannot exhaust the budget before configuration is reached.
func Scan(ctx context.Context, ws *workspace.Workspace) (Result, error) {
	walk, err := ws.WalkFiltered(ctx, "", IsRegistered)
	if err != nil {
		return Result{}, err
	}
	res := Result{
		SkippedDirs:     walk.SkippedDirs,
		LimitsReached:   walk.LimitsReached,
		UnreadableDirs:  walk.UnreadableDirs,
		UnreadableCount: walk.UnreadableCount,
		Complete:        walk.Complete(),
		FilesVisited:    walk.FilesVisited,
		EntriesVisited:  walk.EntriesVisited,
		Detections:      []Detection{},
		Diagnostics:     []diagnostics.Diagnostic{},
	}
	byFormat := map[canonical.TargetFormat]*Detection{}
	var bag diagnostics.Bag
	for _, rel := range walk.Files {
		format, role, ok := Classify(rel)
		if !ok {
			continue
		}
		d, seen := byFormat[format]
		if !seen {
			d = &Detection{Format: format, Confidence: ConfidenceMedium, Files: []Match{}}
			byFormat[format] = d
		}
		d.Files = append(d.Files, Match{Path: rel, Format: format, Role: role, AlsoReadBy: AlsoReadBy(rel)})
		if isPrimary(rel) {
			d.Confidence = ConfidenceHigh
		}
	}
	for _, f := range canonical.AllTargets() {
		d, ok := byFormat[f]
		if !ok {
			continue
		}
		sort.Slice(d.Files, func(i, j int) bool { return d.Files[i].Path < d.Files[j].Path })
		res.Detections = append(res.Detections, *d)
	}
	limits := ws.Limits()
	for _, limit := range walk.LimitsReached {
		d := diagnostics.New(diagnostics.FileLimitReached, diagnostics.SeverityWarning,
			"scan did not cover the whole workspace because a resource limit was reached: "+limit).
			WithSuggestion("Scan a smaller workspace, or move configuration out of the truncated area. " +
				"stemma import refuses an incomplete scan unless --allow-incomplete-scan is given.")
		switch limit {
		case workspace.LimitMaxDepth:
			d = d.WithDetail("Directories deeper than %d levels were not inspected: %s. "+
				"Directory-scoped instruction files below them were not discovered.",
				limits.MaxDepth, depthTruncated(walk.SkippedDirs, limits.MaxDepth))
		case workspace.LimitMaxFiles:
			d = d.WithDetail("More than %d registered configuration files were found; the rest were not discovered.",
				limits.MaxFiles)
		case workspace.LimitMaxEntries:
			d = d.WithDetail("The walk inspected %d directory entries and stopped; configuration "+
				"in the entries it did not reach was not discovered.", limits.MaxEntries)
		default:
			d = d.WithDetail("Some configuration files may not have been discovered.")
		}
		bag.Add(d)
	}
	for _, dir := range walk.UnreadableDirs {
		bag.Add(diagnostics.New(diagnostics.DirectoryUnreadable, diagnostics.SeverityWarning,
			"a directory could not be read, so configuration inside it was not discovered").
			WithPath(dir).
			WithDetail("Stemma could not list this directory's entries, typically because of its " +
				"permissions. Nothing below it was inspected.").
			WithSuggestion("Make the directory readable, or move configuration out of it. " +
				"stemma import refuses an incomplete scan unless --allow-incomplete-scan is given."))
	}
	if extra := walk.UnreadableCount - len(walk.UnreadableDirs); extra > 0 {
		bag.Add(diagnostics.New(diagnostics.DirectoryUnreadable, diagnostics.SeverityWarning,
			fmt.Sprintf("%d more directories could not be read", extra)).
			WithDetail("Only the first %d unreadable directories are listed.", workspace.MaxListedUnreadable))
	}
	if len(res.Detections) == 0 {
		bag.Add(diagnostics.New(diagnostics.NoSourcesDetected, diagnostics.SeverityInfo,
			"no supported agent configuration was detected").
			WithDetail("Stemma looked for: %s.", strings.Join(Registry(), ", ")))
	}
	res.Diagnostics = bag.Items()
	return res, nil
}

// maxListed bounds how many truncated directories a diagnostic names.
const maxListed = 10

// depthTruncated lists the skipped directories that the depth limit, not the
// fixed skip list, excluded, naming at most maxListed of them. The input is
// already sorted.
func depthTruncated(skipped []string, maxDepth int) string {
	var names []string
	total := 0
	for _, rel := range skipped {
		if strings.Count(rel, "/")+1 <= maxDepth || workspace.IsSkippedDir(path.Base(rel)) {
			continue
		}
		total++
		if len(names) < maxListed {
			names = append(names, rel)
		}
	}
	out := strings.Join(names, ", ")
	if total > len(names) {
		out += fmt.Sprintf(" and %d more", total-len(names))
	}
	return out
}

// IncompleteReasons describes, deterministically, why the scan is incomplete.
// It is empty for a complete scan.
func (r Result) IncompleteReasons() []string {
	var out []string
	if len(r.LimitsReached) > 0 {
		out = append(out, "limits reached: "+strings.Join(r.LimitsReached, ", "))
	}
	if r.UnreadableCount == 1 {
		out = append(out, "1 unreadable directory")
	} else if r.UnreadableCount > 1 {
		out = append(out, fmt.Sprintf("%d unreadable directories", r.UnreadableCount))
	}
	return out
}

// AllMatches returns every matched file across all detections, sorted by path.
func (r Result) AllMatches() []Match {
	var out []Match
	for _, d := range r.Detections {
		out = append(out, d.Files...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func isPrimary(rel string) bool {
	for _, r := range registry {
		if r.primary && globs.Match(r.pattern, rel) {
			return true
		}
	}
	return false
}

// Formats returns the detected formats in deterministic order.
func (r Result) Formats() []canonical.TargetFormat {
	out := make([]canonical.TargetFormat, 0, len(r.Detections))
	for _, d := range r.Detections {
		out = append(out, d.Format)
	}
	canonical.SortTargets(out)
	return out
}

// Files returns the matched files for a format.
func (r Result) Files(f canonical.TargetFormat) []Match {
	for _, d := range r.Detections {
		if d.Format == f {
			return d.Files
		}
	}
	return nil
}

// SkillName derives the skill directory name from a SKILL.md path.
func SkillName(rel string) string {
	return path.Base(path.Dir(rel))
}
