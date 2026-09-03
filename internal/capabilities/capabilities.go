// Package capabilities centralises what each provider can actually express.
//
// Every projection decision that depends on provider behaviour must consult
// this table instead of testing target identifiers inline, so that provider
// compatibility can be audited in one place.
//
// The table is a *baseline*: it records what Stemma was written against, with
// the documentation source and the date it was last verified. See
// docs/provider-compatibility.md.
package capabilities

import (
	"sort"

	"github.com/alexvinola/stemma/internal/canonical"
	"github.com/alexvinola/stemma/internal/version"
)

// Source records where a capability claim came from.
type Source struct {
	// Title is the human name of the documentation page.
	Title string `json:"title"`
	// URL is the official documentation URL.
	URL string `json:"url"`
	// LastVerified is the ISO date the claim was last checked by a human.
	LastVerified string `json:"lastVerified"`
}

// Capabilities describes one provider's expressive power.
type Capabilities struct {
	Target canonical.TargetFormat `json:"target"`
	// Available reports whether Stemma can compile to this target at all.
	// Declared-but-unimplemented targets are Available=false.
	Available bool `json:"available"`
	// Baseline identifies the compatibility baseline this row belongs to.
	Baseline string `json:"baseline"`

	// AlwaysOn: the provider loads a file into every request.
	AlwaysOn bool `json:"alwaysOn"`
	// PathScoped: the provider can scope instructions to matching files.
	PathScoped bool `json:"pathScoped"`
	// IncludeGlobs: path scoping accepts glob include patterns.
	IncludeGlobs bool `json:"includeGlobs"`
	// ExcludeGlobs: path scoping accepts negative patterns.
	ExcludeGlobs bool `json:"excludeGlobs"`
	// MultipleIncludePatterns: more than one include pattern per scoped unit.
	MultipleIncludePatterns bool `json:"multipleIncludePatterns"`
	// DirectoryScoped: scoping is expressed by file location, not patterns.
	DirectoryScoped bool `json:"directoryScoped"`
	// DirectoryPrecedence: nested files refine ancestors.
	DirectoryPrecedence bool `json:"directoryPrecedence"`
	// NativeSkills: the provider has a first-class skill format.
	NativeSkills bool `json:"nativeSkills"`
	// NativeAgents: the provider has first-class specialist agents.
	NativeAgents bool `json:"nativeAgents"`
	// NativeProcedures: the provider has a first-class prompt/procedure format.
	NativeProcedures bool `json:"nativeProcedures"`
	// ManualActivation: content can be invoked explicitly by a user.
	ManualActivation bool `json:"manualActivation"`
	// AgentToolAllowlist: agent definitions can declare permitted tools.
	AgentToolAllowlist bool `json:"agentToolAllowlist"`
	// ReemitOpaqueBlocks: Stemma can write unknown provider content back.
	ReemitOpaqueBlocks bool `json:"reemitOpaqueBlocks"`

	// RecognizedPaths lists the configuration paths Stemma reads and writes.
	RecognizedPaths []string `json:"recognizedPaths"`
	// SupportedMetadata lists front matter keys Stemma understands.
	SupportedMetadata []string `json:"supportedMetadata"`
	// Notes documents caveats that a boolean cannot express.
	Notes string `json:"notes,omitempty"`
	// Sources lists the official documentation this row is based on.
	Sources []Source `json:"sources"`
}

var table = map[canonical.TargetFormat]Capabilities{
	canonical.TargetCopilot: {
		Target:                  canonical.TargetCopilot,
		Available:               true,
		AlwaysOn:                true,
		PathScoped:              true,
		IncludeGlobs:            true,
		ExcludeGlobs:            false,
		MultipleIncludePatterns: true,
		DirectoryScoped:         false,
		DirectoryPrecedence:     false,
		NativeSkills:            true,
		NativeAgents:            true,
		NativeProcedures:        true,
		ManualActivation:        true,
		AgentToolAllowlist:      true,
		ReemitOpaqueBlocks:      true,
		RecognizedPaths: []string{
			".github/agents/*.md",
			".github/copilot-instructions.md",
			".github/instructions/**/*.instructions.md",
			".github/prompts/*.prompt.md",
			".github/skills/*/SKILL.md",
		},
		SupportedMetadata: []string{"applyTo", "description", "mode", "model", "name", "tools"},
		Notes: "applyTo accepts a comma-separated list of glob patterns. The documented front matter " +
			"has no negative pattern syntax, so canonical exclude patterns cannot be represented. " +
			"Copilot also reads AGENTS.md and CLAUDE.md; Stemma never writes those files for this " +
			"target, to avoid two targets owning one file. Unknown keys such as excludeAgent are " +
			"preserved as provider extensions rather than interpreted.",
		Sources: []Source{
			{
				Title:        "Adding repository custom instructions for GitHub Copilot",
				URL:          "https://docs.github.com/en/copilot/how-tos/configure-custom-instructions/add-repository-instructions",
				LastVerified: "2026-09-02",
			},
			{
				Title:        "About agent skills",
				URL:          "https://docs.github.com/en/copilot/concepts/agents/about-agent-skills",
				LastVerified: "2026-09-02",
			},
			{
				Title:        "Creating custom agents for Copilot cloud agent",
				URL:          "https://docs.github.com/en/copilot/how-tos/copilot-on-github/customize-copilot/customize-cloud-agent/create-custom-agents",
				LastVerified: "2026-09-02",
			},
		},
	},
	canonical.TargetClaude: {
		Target:                  canonical.TargetClaude,
		Available:               true,
		AlwaysOn:                true,
		PathScoped:              true,
		IncludeGlobs:            true,
		ExcludeGlobs:            false,
		MultipleIncludePatterns: true,
		DirectoryScoped:         true,
		DirectoryPrecedence:     true,
		NativeSkills:            true,
		NativeAgents:            true,
		NativeProcedures:        false,
		ManualActivation:        true,
		AgentToolAllowlist:      true,
		ReemitOpaqueBlocks:      true,
		RecognizedPaths: []string{
			".claude/CLAUDE.md",
			".claude/agents/*.md",
			".claude/rules/**/*.md",
			".claude/skills/*/SKILL.md",
			"CLAUDE.md",
		},
		SupportedMetadata: []string{"allowed-tools", "description", "model", "name", "paths", "tools"},
		Notes: "Rules in .claude/rules/ are discovered recursively; a rule without paths front matter " +
			"loads unconditionally, and one with paths loads when Claude reads a matching file. " +
			"Procedures have no dedicated format and are exported as skills. Imported files " +
			"(@path syntax) still enter the context window at launch, so Stemma never presents " +
			"imports as a context reduction. Claude's glob dialect supports brace expansion; " +
			"Stemma passes braces through verbatim and treats them literally in its own matching.",
		Sources: []Source{{
			Title:        "How Claude remembers your project (CLAUDE.md and .claude/rules/)",
			URL:          "https://code.claude.com/docs/en/memory",
			LastVerified: "2026-09-02",
		}},
	},
	canonical.TargetCodex: {
		Target:                  canonical.TargetCodex,
		Available:               true,
		AlwaysOn:                true,
		PathScoped:              true,
		IncludeGlobs:            false,
		ExcludeGlobs:            false,
		MultipleIncludePatterns: false,
		DirectoryScoped:         true,
		DirectoryPrecedence:     true,
		NativeSkills:            true,
		NativeAgents:            false,
		NativeProcedures:        false,
		ManualActivation:        true,
		AgentToolAllowlist:      false,
		ReemitOpaqueBlocks:      true,
		RecognizedPaths: []string{
			".agents/skills/*/SKILL.md",
			"**/AGENTS.md",
			"**/AGENTS.override.md",
			"AGENTS.md",
			"AGENTS.override.md",
		},
		SupportedMetadata: []string{"description", "name"},
		Notes: "Scoping is expressed only by file location: a nested AGENTS.md applies to its " +
			"directory subtree. Glob patterns have no representation, so a path-scoped rule is " +
			"only projected natively when its patterns resolve to a single concrete directory. " +
			"There is no native specialist-agent format.",
		Sources: []Source{{
			Title:        "AGENTS.md open format (nested files, nearest file wins)",
			URL:          "https://agents.md/",
			LastVerified: "2026-09-02",
		}},
	},
	canonical.TargetKiro: {
		Target:                  canonical.TargetKiro,
		Available:               true,
		AlwaysOn:                true,
		PathScoped:              true,
		IncludeGlobs:            true,
		ExcludeGlobs:            false,
		MultipleIncludePatterns: true,
		DirectoryScoped:         false,
		DirectoryPrecedence:     false,
		NativeSkills:            true,
		NativeAgents:            true,
		NativeProcedures:        false,
		ManualActivation:        true,
		AgentToolAllowlist:      true,
		ReemitOpaqueBlocks:      true,
		RecognizedPaths: []string{
			".kiro/agents/*.json",
			".kiro/skills/*/SKILL.md",
			".kiro/steering/*.md",
		},
		SupportedMetadata: []string{"description", "fileMatchPattern", "inclusion", "name"},
		Notes: "Steering documents declare inclusion: always | fileMatch | manual | auto. " +
			"fileMatchPattern accepts a single pattern or an array of patterns. inclusion: auto " +
			"additionally carries name and description; Stemma preserves that mode as an " +
			"on-demand activation with a trigger description. product.md, tech.md and " +
			"structure.md are the documented foundation files, which is why Stemma assigns " +
			"context kinds to them by filename.",
		Sources: []Source{{
			Title:        "Kiro steering documents",
			URL:          "https://kiro.dev/docs/steering/",
			LastVerified: "2026-09-02",
		}},
	},
	canonical.TargetCursor: {
		Target:    canonical.TargetCursor,
		Available: false,
		Notes: "Declared target identifier only. No importer or exporter is implemented, so " +
			"Stemma refuses to compile for Cursor rather than producing plausible output.",
		RecognizedPaths:   []string{},
		SupportedMetadata: []string{},
		Sources:           []Source{},
	},
}

// For returns the capability row for a target.
func For(t canonical.TargetFormat) (Capabilities, bool) {
	c, ok := table[t]
	if !ok {
		return Capabilities{}, false
	}
	c.Baseline = version.CompatibilityBaseline
	return c, true
}

// MustFor returns the capability row, or a zero row marked unavailable.
func MustFor(t canonical.TargetFormat) Capabilities {
	if c, ok := For(t); ok {
		return c
	}
	return Capabilities{Target: t, Available: false, Baseline: version.CompatibilityBaseline}
}

// Available reports whether a target can be compiled.
func Available(t canonical.TargetFormat) bool {
	c, ok := For(t)
	return ok && c.Available
}

// All returns every capability row in deterministic target order.
func All() []Capabilities {
	out := make([]Capabilities, 0, len(table))
	for t := range table {
		out = append(out, MustFor(t))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	return out
}

// AvailableTargets lists implemented targets in deterministic order.
func AvailableTargets() []canonical.TargetFormat {
	var out []canonical.TargetFormat
	for _, c := range All() {
		if c.Available {
			out = append(out, c.Target)
		}
	}
	return out
}
