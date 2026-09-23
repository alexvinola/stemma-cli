package capabilities

import (
	"sort"

	"github.com/alexvinola/stemma-cli/internal/canonical"
)

// ExtensionKind classifies what a provider extension field does, which
// decides what it costs to lose it when a target cannot write it.
type ExtensionKind string

const (
	// ExtensionPresentation only affects how something is labelled or shown,
	// or mirrors a canonical field that every target projects on its own.
	// Losing it is not reported.
	ExtensionPresentation ExtensionKind = "presentation"
	// ExtensionContext changes what content reaches the agent's context.
	// Losing it makes the mapping lossy with a warning.
	ExtensionContext ExtensionKind = "context"
	// ExtensionBehaviour changes how or when the agent acts. Losing it makes
	// the mapping lossy with a warning.
	ExtensionBehaviour ExtensionKind = "behaviour"
	// ExtensionSecurity restricts or grants what the agent may do: permission
	// policies, tool allowlists, hooks, MCP servers. Losing it makes the
	// mapping lossy with a blocking error that must be accepted explicitly.
	ExtensionSecurity ExtensionKind = "security"
)

// UnclassifiedExtensionKind is the conservative default for an extension
// key the table below does not list. An unknown field may well change
// behaviour, so its loss is never silent.
const UnclassifiedExtensionKind = ExtensionBehaviour

// KnownExtensionKind reports whether k is one of the four kinds.
func KnownExtensionKind(k ExtensionKind) bool {
	switch k {
	case ExtensionPresentation, ExtensionContext, ExtensionBehaviour, ExtensionSecurity:
		return true
	default:
		return false
	}
}

// ExtensionField is the classification of one provider extension key.
//
// Keys are matched exactly, per provider, whatever entity carries them. Every
// row either cites the official documentation that defines the key (Source),
// or names the canonical field it mirrors (Mirrors) when the key is a copy
// that Stemma's importer keeps for same-provider round trips.
type ExtensionField struct {
	Provider canonical.TargetFormat `json:"provider"`
	Key      string                 `json:"key"`
	Kind     ExtensionKind          `json:"kind"`
	// Meaning is a short description of what the key does.
	Meaning string `json:"meaning"`
	// Mirrors names the canonical field this key duplicates, if any.
	Mirrors string `json:"mirrors,omitempty"`
	// Source is the documentation defining the key; zero for mirrors that
	// the provider does not document.
	Source Source `json:"source,omitzero"`
}

// Documentation pages cited by the extension classification. Titles and URLs
// match the capability rows; the dates record when the field lists below were
// last checked against them.
var (
	srcAgentSkills = Source{
		Title: "Agent Skills specification (frontmatter fields)", URL: "https://agentskills.io/specification",
		LastVerified: "2026-09-23",
	}
	srcCopilotInstructions = Source{
		Title:        "Adding repository custom instructions for GitHub Copilot",
		URL:          "https://docs.github.com/en/copilot/how-tos/copilot-on-github/customize-copilot/add-custom-instructions/add-repository-instructions",
		LastVerified: "2026-09-23",
	}
	srcCopilotAgents = Source{
		Title:        "Custom agents configuration",
		URL:          "https://docs.github.com/en/copilot/reference/custom-agents-configuration",
		LastVerified: "2026-09-23",
	}
	srcCopilotPrompts = Source{
		Title:        "Prompt files in VS Code (GitHub Copilot)",
		URL:          "https://code.visualstudio.com/docs/copilot/customization/prompt-files",
		LastVerified: "2026-09-23",
	}
	srcClaudeSkills = Source{
		Title: "Extend Claude with skills", URL: "https://code.claude.com/docs/en/skills",
		LastVerified: "2026-09-23",
	}
	srcClaudeAgents = Source{
		Title: "Create custom subagents", URL: "https://code.claude.com/docs/en/sub-agents",
		LastVerified: "2026-09-23",
	}
	srcKiroSteering = Source{
		Title: "Kiro steering documents", URL: "https://kiro.dev/docs/steering/",
		LastVerified: "2026-09-23",
	}
	srcKiroAgents = Source{
		Title:        "Kiro custom agent configuration reference",
		URL:          "https://kiro.dev/docs/custom-agents/configuration-reference/",
		LastVerified: "2026-09-23",
	}
)

// extensionTable lists every classified key, indexed by provider and key.
// ExtensionFields returns the rows sorted; their order here is for readers.
var extensionTable = buildExtensionTable()

func buildExtensionTable() map[canonical.TargetFormat]map[string]ExtensionField {
	copilot, claude, codex, kiro := canonical.TargetCopilot, canonical.TargetClaude, canonical.TargetCodex, canonical.TargetKiro
	rows := []ExtensionField{
		// GitHub Copilot.
		{Provider: copilot, Key: "description", Kind: ExtensionPresentation,
			Meaning: "descriptive label of an instructions file", Mirrors: "title"},
		{Provider: copilot, Key: "excludeAgent", Kind: ExtensionBehaviour,
			Meaning: "stops Copilot code review or Copilot cloud agent from using the instructions",
			Source:  srcCopilotInstructions},
		{Provider: copilot, Key: "agent", Kind: ExtensionBehaviour,
			Meaning: "agent that runs a prompt (ask, agent, plan or a custom agent)", Source: srcCopilotPrompts},
		{Provider: copilot, Key: "model", Kind: ExtensionBehaviour,
			Meaning: "language model used to run the prompt", Source: srcCopilotPrompts},
		{Provider: copilot, Key: "tools", Kind: ExtensionSecurity,
			Meaning: "tools or tool sets available to the prompt", Source: srcCopilotPrompts},
		{Provider: copilot, Key: "argument-hint", Kind: ExtensionPresentation,
			Meaning: "hint text shown in the chat input", Source: srcCopilotPrompts},
		{Provider: copilot, Key: "target", Kind: ExtensionBehaviour,
			Meaning: "environment a custom agent is meant for", Source: srcCopilotAgents},
		{Provider: copilot, Key: "disable-model-invocation", Kind: ExtensionBehaviour,
			Meaning: "stops the cloud agent from choosing the custom agent automatically", Source: srcCopilotAgents},
		{Provider: copilot, Key: "user-invocable", Kind: ExtensionBehaviour,
			Meaning: "whether a user can select the custom agent", Source: srcCopilotAgents},
		{Provider: copilot, Key: "infer", Kind: ExtensionBehaviour,
			Meaning: "retired switch for automatic selection of the custom agent", Source: srcCopilotAgents},
		{Provider: copilot, Key: "mcp-servers", Kind: ExtensionSecurity,
			Meaning: "additional MCP servers and tools granted to the custom agent", Source: srcCopilotAgents},

		// Claude Code.
		{Provider: claude, Key: "description", Kind: ExtensionPresentation,
			Meaning: "descriptive label of a rule file", Mirrors: "title"},
		{Provider: claude, Key: "when_to_use", Kind: ExtensionBehaviour,
			Meaning: "additional guidance on when Claude invokes the skill", Source: srcClaudeSkills},
		{Provider: claude, Key: "argument-hint", Kind: ExtensionPresentation,
			Meaning: "autocomplete hint for skill arguments", Source: srcClaudeSkills},
		{Provider: claude, Key: "arguments", Kind: ExtensionBehaviour,
			Meaning: "named arguments substituted into the skill", Source: srcClaudeSkills},
		{Provider: claude, Key: "disable-model-invocation", Kind: ExtensionBehaviour,
			Meaning: "stops Claude from loading the skill automatically", Source: srcClaudeSkills},
		{Provider: claude, Key: "user-invocable", Kind: ExtensionBehaviour,
			Meaning: "hides the skill from the slash-command menu", Source: srcClaudeSkills},
		{Provider: claude, Key: "disallowed-tools", Kind: ExtensionSecurity,
			Meaning: "tools removed while the skill is active", Source: srcClaudeSkills},
		{Provider: claude, Key: "model", Kind: ExtensionBehaviour,
			Meaning: "model used while the skill is active", Source: srcClaudeSkills},
		{Provider: claude, Key: "effort", Kind: ExtensionBehaviour,
			Meaning: "effort level while the skill or subagent is active", Source: srcClaudeSkills},
		{Provider: claude, Key: "context", Kind: ExtensionBehaviour,
			Meaning: "runs the skill in a forked subagent context", Source: srcClaudeSkills},
		{Provider: claude, Key: "agent", Kind: ExtensionBehaviour,
			Meaning: "subagent type used by a forked skill", Source: srcClaudeSkills},
		{Provider: claude, Key: "background", Kind: ExtensionBehaviour,
			Meaning: "whether a skill or subagent runs in the background", Source: srcClaudeSkills},
		{Provider: claude, Key: "hooks", Kind: ExtensionSecurity,
			Meaning: "lifecycle hooks that run commands while the skill or subagent is active", Source: srcClaudeSkills},
		{Provider: claude, Key: "paths", Kind: ExtensionContext,
			Meaning: "glob patterns that limit when the skill is activated", Source: srcClaudeSkills},
		{Provider: claude, Key: "shell", Kind: ExtensionBehaviour,
			Meaning: "shell used for command blocks in the skill", Source: srcClaudeSkills},
		{Provider: claude, Key: "disallowedTools", Kind: ExtensionSecurity,
			Meaning: "tools denied to the subagent", Source: srcClaudeAgents},
		{Provider: claude, Key: "permissionMode", Kind: ExtensionSecurity,
			Meaning: "permission mode of the subagent", Source: srcClaudeAgents},
		{Provider: claude, Key: "mcpServers", Kind: ExtensionSecurity,
			Meaning: "MCP servers available to the subagent", Source: srcClaudeAgents},
		{Provider: claude, Key: "maxTurns", Kind: ExtensionBehaviour,
			Meaning: "maximum agentic turns before the subagent stops", Source: srcClaudeAgents},
		{Provider: claude, Key: "skills", Kind: ExtensionContext,
			Meaning: "skills preloaded into the subagent's context", Source: srcClaudeAgents},
		{Provider: claude, Key: "memory", Kind: ExtensionContext,
			Meaning: "persistent memory scope of the subagent", Source: srcClaudeAgents},
		{Provider: claude, Key: "omitClaudeMd", Kind: ExtensionContext,
			Meaning: "launches the subagent without CLAUDE.md files", Source: srcClaudeAgents},
		{Provider: claude, Key: "isolation", Kind: ExtensionBehaviour,
			Meaning: "runs the subagent in a temporary git worktree", Source: srcClaudeAgents},
		{Provider: claude, Key: "color", Kind: ExtensionPresentation,
			Meaning: "display color of the subagent", Source: srcClaudeAgents},
		{Provider: claude, Key: "initialPrompt", Kind: ExtensionBehaviour,
			Meaning: "first user turn submitted when the agent runs as the main session", Source: srcClaudeAgents},
		{Provider: claude, Key: "experimental", Kind: ExtensionBehaviour,
			Meaning: "experimental subagent options", Source: srcClaudeAgents},

		// Kiro.
		{Provider: kiro, Key: "inclusion", Kind: ExtensionPresentation,
			Meaning: "steering inclusion mode", Mirrors: "activation", Source: srcKiroSteering},
		{Provider: kiro, Key: "name", Kind: ExtensionPresentation,
			Meaning: "steering identifier", Mirrors: "activation.invocationName", Source: srcKiroSteering},
		{Provider: kiro, Key: "description", Kind: ExtensionPresentation,
			Meaning: "when a steering document applies", Mirrors: "activation.trigger", Source: srcKiroSteering},
		{Provider: kiro, Key: "mcpServers", Kind: ExtensionSecurity,
			Meaning: "MCP servers the agent has access to", Source: srcKiroAgents},
		{Provider: kiro, Key: "toolAliases", Kind: ExtensionBehaviour,
			Meaning: "remapped tool names", Source: srcKiroAgents},
		{Provider: kiro, Key: "allowedTools", Kind: ExtensionSecurity,
			Meaning: "tools the agent may use without prompting", Source: srcKiroAgents},
		{Provider: kiro, Key: "permissions", Kind: ExtensionSecurity,
			Meaning: "capability-based access control rules", Source: srcKiroAgents},
		{Provider: kiro, Key: "toolsSettings", Kind: ExtensionSecurity,
			Meaning: "per-tool configuration, including shell and filesystem rules", Source: srcKiroAgents},
		{Provider: kiro, Key: "resources", Kind: ExtensionContext,
			Meaning: "files, skills and knowledge bases loaded for the agent", Source: srcKiroAgents},
		{Provider: kiro, Key: "hooks", Kind: ExtensionSecurity,
			Meaning: "commands run at agent lifecycle trigger points", Source: srcKiroAgents},
		{Provider: kiro, Key: "includeMcpJson", Kind: ExtensionSecurity,
			Meaning: "grants the MCP servers from workspace and global configuration", Source: srcKiroAgents},
		{Provider: kiro, Key: "keyboardShortcut", Kind: ExtensionPresentation,
			Meaning: "shortcut for switching to the agent", Source: srcKiroAgents},
		{Provider: kiro, Key: "welcomeMessage", Kind: ExtensionPresentation,
			Meaning: "message shown when switching to the agent", Source: srcKiroAgents},
	}
	// Optional Agent Skills metadata is shared by every provider's SKILL.md.
	for _, p := range []canonical.TargetFormat{copilot, claude, codex, kiro} {
		rows = append(rows,
			ExtensionField{Provider: p, Key: "license", Kind: ExtensionPresentation,
				Meaning: "license covering the skill", Source: srcAgentSkills},
			ExtensionField{Provider: p, Key: "compatibility", Kind: ExtensionPresentation,
				Meaning: "informational environment requirements of the skill", Source: srcAgentSkills},
			ExtensionField{Provider: p, Key: "metadata", Kind: ExtensionPresentation,
				Meaning: "free-form key-value annotations", Source: srcAgentSkills},
		)
	}
	out := map[canonical.TargetFormat]map[string]ExtensionField{}
	for _, r := range rows {
		if out[r.Provider] == nil {
			out[r.Provider] = map[string]ExtensionField{}
		}
		out[r.Provider][r.Key] = r
	}
	return out
}

// ClassifyExtension returns the classification of a provider extension key.
// ok is false when the key is not classified; callers must then treat it as
// UnclassifiedExtensionKind.
func ClassifyExtension(provider canonical.TargetFormat, key string) (ExtensionField, bool) {
	f, ok := extensionTable[provider][key]
	return f, ok
}

// ExtensionFields returns every classified key, sorted by provider then key.
func ExtensionFields() []ExtensionField {
	var out []ExtensionField
	for _, keys := range extensionTable {
		for _, f := range keys {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Key < out[j].Key
	})
	return out
}
