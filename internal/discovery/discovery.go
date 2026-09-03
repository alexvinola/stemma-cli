// Package discovery detects provider configuration files.
//
// Discovery inspects file *paths* only. Source files are never opened: a file
// is read later only if it matched a registered configuration pattern.
package discovery

import (
	"context"
	"path"
	"sort"
	"strings"

	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/diagnostics"
	"github.com/alexvinola/stemma/internal/globs"
	"github.com/alexvinola/stemma/internal/workspace"
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
	Detections    []Detection              `json:"detections"`
	SkippedDirs   []string                 `json:"skippedDirectories"`
	LimitsReached []string                 `json:"limitsReached"`
	FilesVisited  int                      `json:"filesVisited"`
	Diagnostics   []diagnostics.Diagnostic `json:"diagnostics"`
}

// rule maps a path pattern to a format and role.
type rule struct {
	pattern string
	format  canonical.TargetFormat
	role    Role
	primary bool
}

// registry is the complete list of paths Stemma will ever open. Order matters:
// the first matching rule wins, so more specific patterns come first.
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
	{"**/AGENTS.md", canonical.TargetCodex, RoleNestedInstructions, false},
	{"**/AGENTS.override.md", canonical.TargetCodex, RoleOverride, false},

	// Kiro.
	{".kiro/steering/**/*.md", canonical.TargetKiro, RoleSteering, true},
	{".kiro/skills/*/SKILL.md", canonical.TargetKiro, RoleSkill, false},
	{".kiro/agents/*.json", canonical.TargetKiro, RoleAgent, false},
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

// Scan walks the workspace and classifies configuration files. It never opens
// a file.
func Scan(ctx context.Context, ws *workspace.Workspace) (Result, error) {
	walk, err := ws.Walk(ctx, "")
	if err != nil {
		return Result{}, err
	}
	res := Result{
		SkippedDirs:   walk.SkippedDirs,
		LimitsReached: walk.LimitsReached,
		FilesVisited:  len(walk.Files),
		Detections:    []Detection{},
		Diagnostics:   []diagnostics.Diagnostic{},
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
		d.Files = append(d.Files, Match{Path: rel, Format: format, Role: role})
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
	for _, limit := range walk.LimitsReached {
		bag.Add(diagnostics.New(diagnostics.FileLimitReached, diagnostics.SeverityWarning,
			"scan stopped early because a resource limit was reached: "+limit).
			WithDetail("Some configuration files may not have been discovered.").
			WithSuggestion("Reduce the size of the workspace or scan a subdirectory."))
	}
	if len(res.Detections) == 0 {
		bag.Add(diagnostics.New(diagnostics.NoSourcesDetected, diagnostics.SeverityInfo,
			"no supported agent configuration was detected").
			WithDetail("Stemma looked for: %s.", strings.Join(Registry(), ", ")))
	}
	res.Diagnostics = bag.Items()
	return res, nil
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
