# Provider compatibility

This is Stemma's compatibility **baseline**: what the adapters were written
against, where the claim comes from, and when it was last checked by a human.

Baseline identifier: `2026-09-provider-baseline-1`
(`stemma version` prints the baseline of the binary you are running.)

Provider formats evolve. When a provider changes, update
`internal/capabilities`, this document, the adapter and the fixtures together.

Status vocabulary used below:

- **Implemented** — Stemma reads and/or writes it, with tests.
- **Partial** — Handled, with a documented limitation.
- **Lossy** — Represented, but canonical information cannot be carried over.
- **Unsupported** — Not represented at all.
- **Planned** — Not implemented; Stemma refuses rather than approximating.

## Capability matrix

| Capability | Copilot | Claude Code | Codex (`AGENTS.md`) | Kiro | Cursor |
| --- | --- | --- | --- | --- | --- |
| Always-on instructions | yes | yes | yes | yes | — |
| Path-scoped instructions | yes | yes | by directory only | yes | — |
| Include glob patterns | yes | yes | no | yes | — |
| Exclude glob patterns | no | no | no | no | — |
| Several include patterns per unit | yes | yes | n/a | yes | — |
| Directory hierarchy affects precedence | no | yes | yes | no | — |
| Native skills | yes | yes | yes | yes | — |
| Native specialist agents | yes | yes | **no** | yes | — |
| Native procedures / prompt files | yes | no | no | no | — |
| Manual invocation | yes | yes | yes | yes | — |
| Agent tool allowlist | yes | yes | no | yes | — |
| Opaque provider content re-emitted | yes | yes | yes | yes | — |

## Recognized metadata types

Stemma checks recognized fields before creating any entity. Missing optional
fields keep their existing defaults; a present value of the wrong type,
including `null`, is a blocking `STEMMA1101` error. The diagnostic names the
source file, key, expected type and found type. A malformed list also identifies
the first non-string member. The entire provider file is preserved verbatim as
an opaque block; the CLI aborts the import without writing or replacing the
canonical project. Invalid metadata cannot become always-on context or silently
remove a tool restriction.

| Import surface | Strings | String or list of strings | Boolean |
| --- | --- | --- | --- |
| Copilot instructions | `applyTo`, `description` | — | — |
| Copilot prompts | `name`, `description` | — | — |
| Claude rules | `description`, `priority` | `paths` | `enabled` |
| Kiro steering | `inclusion`, `name`, `description` | `fileMatchPattern` | — |
| Skills (all four providers) | `name`, `description` | `allowed-tools`, `allowedTools`, `tools` | — |
| Copilot / Claude Markdown agents | `name`, `description`, `model` | `tools`, `allowed-tools`, `allowedTools` | — |

This table describes Stemma's import contract, including its existing tool-key
aliases and Claude rule `priority`/`enabled` metadata; it does not claim that
all providers interpret these fields. All aliases are checked even if another
alias takes precedence. Unknown metadata and prompt `mode`/`model` values remain
provider extensions, without coercion or interpretation. Existing handling of
valid empty strings/lists is unchanged.

Kiro JSON agents use the same policy with `STEMMA1501`: `name`, `description`,
`prompt`, `instructions` and `model` must be strings; `tools` must be an array of
strings. JSON `null` is not an absent field or an empty tool name. The existing
`instructions` alias remains supported.

Canonical entity files also reject wrong types, including nested activation
fields and extension-provider mappings, and report the found type. Arbitrary
values inside a valid provider extension mapping remain preserved.

## Generated names and destination collisions

Fallback filenames use the complete canonical ID with its type prefix, for
example `rule.testing` becomes `rule-testing.instructions.md` for Copilot.
Titles may be identical without merging entities, including entities of different
types. Imported filename/directory hints and explicit profile destinations still
have precedence. Conflicting destinations (including file/parent-directory
conflicts) produce blocking `STEMMA6001_INTERNAL_INVARIANT`, blocked mappings and
CLI exit 6. Destination identity uses Unicode simple case folding on every
platform, so case-only aliases such as `Scope.md` and `scope.md`, or a file
`Scope` and a child of `scope/`, are rejected even on a case-sensitive host. It
does not fold Unicode normalization variants or multi-rune expansions.
Independently emitted files are never concatenated or overwritten.
Intentional `CLAUDE.md`, `AGENTS.md` and Copilot root aggregates remain supported.

Regenerated skills use their directory as the front matter `name`, as required
by the [Agent Skills specification](https://agentskills.io/specification)
(verified 2026-09-06). A changed invocation name is reported as `adapted` and
explained in its mapping. Fallback skill directories exceeding 64 characters
use the full SHA-256 digest of the canonical ID. Unchanged imported originals
remain eligible for byte-identical reuse.

**Existing projects:** cross-provider output without a preserved target hint may
move from a title-based filename to an ID-based filename. Review `plan` before
applying: Stemma proposes deletion of old generated paths but never deletes them.
Remove obsolete provider files yourself after reviewing their replacements to
avoid loading both versions. Profile destinations can keep an existing name if
it does not conflict. Skill invocations may change alongside their directories;
the mapping explains the new name.

## GitHub Copilot

| Item | Status | Notes |
| --- | --- | --- |
| `.github/copilot-instructions.md` | Implemented | Split into always-on context documents by heading |
| `.github/instructions/**/*.instructions.md` | Implemented | `applyTo` is a comma-separated glob list; the split honours brace nesting, so a comma inside `{ts,tsx}` is not read as a separator |
| `.github/prompts/**/*.prompt.md` | Implemented | Imported as procedures; `mode`, `model` and other keys preserved as extensions and classified |
| `.github/skills/*/SKILL.md` | Implemented | Skills round-trip natively |
| `.github/agents/*.md` | Implemented | Markdown with `name`, `description`, `tools` front matter |
| `applyTo` exclude patterns | Lossy | The documented front matter has no negative syntax. Canonical excludes produce `STEMMA3101` and are written only as a scope note in the file body |
| Brace expansion (`{ts,tsx}`) | Implemented | Groups are expanded on import and projection, including handwritten canonical entities and profiles. Oversized groups are rejected on import with blocking `STEMMA2102`; literal commas remain `lossy` with `STEMMA3102` |
| A pattern carrying a literal comma | Lossy | `applyTo` cannot represent it unambiguously: `STEMMA3102`, plus a scope note in the file body |
| An unclosed brace group in `applyTo` | Rejected | The signature of a group already split on its own comma. `STEMMA2101` names the file rather than importing a scope that matches nothing |
| `excludeAgent` and other unknown keys | Partial | Preserved as provider extensions and written back when a Copilot file is regenerated; not interpreted. Other targets report each one by [classification](#provider-extension-classification): `excludeAgent` is behaviour (`STEMMA3801`), prompt `tools` is security (`STEMMA3802`) |
| `AGENTS.md` / `CLAUDE.md` fallbacks | Unsupported by design | Copilot also reads these, but Stemma never writes them *for the Copilot target*, so two targets never own one file |

Instruction, skill and agent metadata sources last verified 2026-09-06:

- [Adding repository custom instructions for GitHub Copilot](https://docs.github.com/en/copilot/how-tos/copilot-on-github/customize-copilot/add-custom-instructions/add-repository-instructions)
  — confirms the three instruction file locations, the `applyTo` front matter,
  comma-separated multiple patterns, and that Copilot also reads `AGENTS.md`
  and `CLAUDE.md`.
- [About agent skills](https://docs.github.com/en/copilot/concepts/agents/about-agent-skills)
  — confirms `.github/skills`, `.claude/skills` and `.agents/skills` with
  `SKILL.md`.
- [Custom agents configuration](https://docs.github.com/en/copilot/reference/custom-agents-configuration)
  — specifies string metadata and string/string-list tools.

Prompt-file metadata source, last verified 2026-09-23:
[Prompt files in VS Code](https://code.visualstudio.com/docs/copilot/customization/prompt-files)
— lists `description`, `name`, `argument-hint`, `agent`, `model` and `tools`,
which the extension classification relies on. It does not document `mode`.

## Claude Code

| Item | Status | Notes |
| --- | --- | --- |
| `CLAUDE.md`, `.claude/CLAUDE.md` | Implemented | Always-on project instructions; the file that was imported is written back |
| `.claude/rules/**/*.md` | Implemented | Discovered recursively; directory name makes "rule" structurally explicit |
| `paths:` front matter | Implemented | Maps to path-scoped activation; a rule without `paths` is always-on |
| `.claude/skills/*/SKILL.md` | Implemented | Skills round-trip natively |
| `.claude/agents/*.md` | Implemented | `name`, `description`, `tools`, `model`; other documented keys such as `permissionMode` or `hooks` are preserved as extensions, written back for Claude and [classified](#provider-extension-classification) for other targets |
| Exclude patterns | Lossy | `paths` has no documented negative syntax; `STEMMA3101` |
| Procedures | Lossy → Adapted | No native procedure format; exported as skills, reported as `adapted` |
| `@path` imports | Partial | Preserved verbatim as text, never resolved. Stemma warns that imported files still enter the context window, so imports are **not** presented as a context reduction |
| Brace expansion (`{ts,tsx}`) | Implemented | Expanded on import and projection, bounded at 1000 alternatives and 32 nested groups. Oversized groups are rejected on import with blocking `STEMMA2102`; braces inside character classes are preserved |
| `CLAUDE.local.md`, user- and policy-scope files | Unsupported | Personal or machine-level files are out of scope for a repository compiler |
| Auto memory (`~/.claude/projects/**`) | Unsupported | Machine-local, written by the agent, not repository configuration |

Source, last verified 2026-09-06:
[How Claude remembers your project](https://code.claude.com/docs/en/memory)
— confirms `CLAUDE.md` and `.claude/CLAUDE.md`, `.claude/rules/` with recursive
discovery, `paths:` front matter with multiple glob patterns and brace
expansion, and that `@`-imports still load into context at launch.

Skill and agent metadata sources, last verified 2026-09-06:
[Extend Claude with skills](https://code.claude.com/docs/en/skills) and
[Create custom subagents](https://code.claude.com/docs/en/sub-agents).

## Codex / `AGENTS.md`

| Item | Status | Notes |
| --- | --- | --- |
| Root `AGENTS.md` | Implemented | Always-on context |
| Nested `<dir>/AGENTS.md` | Implemented | Imported as path-scoped context for `<dir>/**`; the nearest file wins |
| `AGENTS.override.md` | Partial | Override semantics are **not modelled**. The file is preserved verbatim as an opaque block and written back unchanged |
| `.agents/skills/*/SKILL.md` | Implemented | Skills round-trip natively |
| Glob-based scoping | Unsupported | Scoping is file location only. A path-scoped rule is projected natively only when its patterns resolve to a single concrete directory |
| Ambiguous scopes | Lossy | If no single directory can be derived, Stemma refuses to invent one: the content stays in the root file with `STEMMA3201`, and a profile can pin a directory or skip the entity |
| Broadened scopes | Lossy | `src/api/*.ts` becomes the directory `src/api`, which matches more files: `STEMMA3202` |
| Specialist agents | Lossy | No native format. Definitions are flattened into always-on guidance with `STEMMA3302`. Stemma never calls this a native agent |
| Procedures | Adapted | Delivered as skills |

Source, last verified 2026-09-02:
[agents.md](https://agents.md/) — confirms the root file, nested files per
package, that the nearest file in the tree takes precedence, and that the format
is plain Markdown with no front matter or glob scoping.

Skill metadata source, last verified 2026-09-06:
[Build skills](https://learn.chatgpt.com/docs/build-skills) — confirms the
`name`/`description` metadata and repository `.agents/skills/` location.

## Kiro

| Item | Status | Notes |
| --- | --- | --- |
| `.kiro/steering/*.md` | Implemented | `inclusion: always` (documented default when absent) |
| `inclusion: fileMatch` | Implemented | `fileMatchPattern` accepts one pattern or an array |
| `inclusion: manual` | Implemented | Imported as on-demand with an invocation name |
| `inclusion: auto` | Implemented | Imported as on-demand with a trigger description; the mode is preserved and written back |
| `product.md`, `tech.md`, `structure.md` | Implemented | Documented foundation files, so their canonical kind is assigned by file name |
| `.kiro/skills/*/SKILL.md` | Implemented | Skills round-trip natively |
| `.kiro/agents/*.json` | Implemented | `name`, `description`, `prompt`/`instructions`, `tools`, `model`; other fields such as `resources` or `allowedTools` are preserved as extensions and written back for Kiro. Other targets report each by [classification](#provider-extension-classification): `resources` is context (`STEMMA3801`); `allowedTools`, `permissions`, `toolsSettings`, `hooks`, `mcpServers` and `includeMcpJson` are security (blocking `STEMMA3802`) |
| Duplicate JSON keys | Rejected | `STEMMA1502`; the file is preserved as an opaque block rather than guessed at |
| Exclude patterns | Lossy | `fileMatchPattern` has no negative syntax; `STEMMA3101` |
| Global `~/.kiro/steering/` | Unsupported | Outside the repository |
| Procedures | Adapted | Delivered as skills |

Source, last verified 2026-09-06:
[Kiro steering documents](https://kiro.dev/docs/steering/) — confirms
`.kiro/steering/`, the four inclusion modes, that `fileMatchPattern` accepts
single or multiple patterns, and the three foundation files.

Skill and agent metadata sources, last verified 2026-09-06:
[Kiro agent skills](https://kiro.dev/docs/skills/) and
[Custom agent configuration reference](https://kiro.dev/docs/custom-agents/configuration-reference/).

## Cursor

| Item | Status |
| --- | --- |
| `.cursor/rules/*.mdc` import | Planned |
| `.cursor/rules/*.mdc` export | Planned |
| Capability row | Declared, `available: false` |

Stemma declares the `cursor` target identifier so that profiles and reports can
reference it, and **refuses to compile for it**: `stemma plan --target cursor`
fails with `STEMMA3001` and exit code 3. Nothing about Cursor is simulated.

## Provider extension classification

Fields Stemma does not model are kept as provider extensions
(`extensions.<provider>.<key>`). A regenerated file of the same provider writes
them back; another provider cannot. What that loss costs depends on what the
field does, so every known key is classified here and in
`internal/capabilities`, against the official documentation cited in each row
(checked 2026-09-23):

- **presentation** — labels and display hints, or a copy of a canonical field
  that every target projects on its own. Dropped silently.
- **context** — changes what reaches the agent's context. The mapping becomes
  `lossy` with warning `STEMMA3801`.
- **behaviour** — changes how or when the agent acts. `lossy` with warning
  `STEMMA3801`.
- **security** — grants or restricts what the agent may do: permission
  policies, tool allowlists, hooks, MCP servers. `lossy` with the blocking
  error `STEMMA3802`; accept its fingerprint in the target profile once an
  equivalent policy has been configured by hand, or the loss has been reviewed.

A key that is not listed is treated as **behaviour**, so the loss of a field
Stemma does not know is never silent. Keys are matched per provider, whatever
entity carries them: Kiro's `allowedTools` classifies nothing about Claude.
Copilot prompt `mode` is deliberately unlisted: the current prompt-file
reference documents `agent` instead, so `mode` falls back to behaviour. Stemma's
own `stemma.*` keys are bookkeeping and are never reported. Front matter kept
at the project level (on `CLAUDE.md`, `.github/copilot-instructions.md` or
`AGENTS.md`) is not attached to an entity and is not yet classified.

| Provider | Key | Kind | Meaning | Basis |
| --- | --- | --- | --- | --- |
| `claude` | `agent` | behaviour | subagent type used by a forked skill | [Extend Claude with skills](https://code.claude.com/docs/en/skills) |
| `claude` | `argument-hint` | presentation | autocomplete hint for skill arguments | [Extend Claude with skills](https://code.claude.com/docs/en/skills) |
| `claude` | `arguments` | behaviour | named arguments substituted into the skill | [Extend Claude with skills](https://code.claude.com/docs/en/skills) |
| `claude` | `background` | behaviour | whether a skill or subagent runs in the background | [Extend Claude with skills](https://code.claude.com/docs/en/skills) |
| `claude` | `color` | presentation | display color of the subagent | [Create custom subagents](https://code.claude.com/docs/en/sub-agents) |
| `claude` | `compatibility` | presentation | informational environment requirements of the skill | [Agent Skills specification (frontmatter fields)](https://agentskills.io/specification) |
| `claude` | `context` | behaviour | runs the skill in a forked subagent context | [Extend Claude with skills](https://code.claude.com/docs/en/skills) |
| `claude` | `description` | presentation | descriptive label of a rule file | mirrors canonical `title` |
| `claude` | `disable-model-invocation` | behaviour | stops Claude from loading the skill automatically | [Extend Claude with skills](https://code.claude.com/docs/en/skills) |
| `claude` | `disallowed-tools` | security | tools removed while the skill is active | [Extend Claude with skills](https://code.claude.com/docs/en/skills) |
| `claude` | `disallowedTools` | security | tools denied to the subagent | [Create custom subagents](https://code.claude.com/docs/en/sub-agents) |
| `claude` | `effort` | behaviour | effort level while the skill or subagent is active | [Extend Claude with skills](https://code.claude.com/docs/en/skills) |
| `claude` | `experimental` | behaviour | experimental subagent options | [Create custom subagents](https://code.claude.com/docs/en/sub-agents) |
| `claude` | `hooks` | security | lifecycle hooks that run commands while the skill or subagent is active | [Extend Claude with skills](https://code.claude.com/docs/en/skills) |
| `claude` | `initialPrompt` | behaviour | first user turn submitted when the agent runs as the main session | [Create custom subagents](https://code.claude.com/docs/en/sub-agents) |
| `claude` | `isolation` | behaviour | runs the subagent in a temporary git worktree | [Create custom subagents](https://code.claude.com/docs/en/sub-agents) |
| `claude` | `license` | presentation | license covering the skill | [Agent Skills specification (frontmatter fields)](https://agentskills.io/specification) |
| `claude` | `maxTurns` | behaviour | maximum agentic turns before the subagent stops | [Create custom subagents](https://code.claude.com/docs/en/sub-agents) |
| `claude` | `mcpServers` | security | MCP servers available to the subagent | [Create custom subagents](https://code.claude.com/docs/en/sub-agents) |
| `claude` | `memory` | context | persistent memory scope of the subagent | [Create custom subagents](https://code.claude.com/docs/en/sub-agents) |
| `claude` | `metadata` | presentation | free-form key-value annotations | [Agent Skills specification (frontmatter fields)](https://agentskills.io/specification) |
| `claude` | `model` | behaviour | model used while the skill is active | [Extend Claude with skills](https://code.claude.com/docs/en/skills) |
| `claude` | `omitClaudeMd` | context | launches the subagent without CLAUDE.md files | [Create custom subagents](https://code.claude.com/docs/en/sub-agents) |
| `claude` | `paths` | context | glob patterns that limit when the skill is activated | [Extend Claude with skills](https://code.claude.com/docs/en/skills) |
| `claude` | `permissionMode` | security | permission mode of the subagent | [Create custom subagents](https://code.claude.com/docs/en/sub-agents) |
| `claude` | `shell` | behaviour | shell used for command blocks in the skill | [Extend Claude with skills](https://code.claude.com/docs/en/skills) |
| `claude` | `skills` | context | skills preloaded into the subagent's context | [Create custom subagents](https://code.claude.com/docs/en/sub-agents) |
| `claude` | `user-invocable` | behaviour | hides the skill from the slash-command menu | [Extend Claude with skills](https://code.claude.com/docs/en/skills) |
| `claude` | `when_to_use` | behaviour | additional guidance on when Claude invokes the skill | [Extend Claude with skills](https://code.claude.com/docs/en/skills) |
| `codex` | `compatibility` | presentation | informational environment requirements of the skill | [Agent Skills specification (frontmatter fields)](https://agentskills.io/specification) |
| `codex` | `license` | presentation | license covering the skill | [Agent Skills specification (frontmatter fields)](https://agentskills.io/specification) |
| `codex` | `metadata` | presentation | free-form key-value annotations | [Agent Skills specification (frontmatter fields)](https://agentskills.io/specification) |
| `github-copilot` | `agent` | behaviour | agent that runs a prompt (ask, agent, plan or a custom agent) | [Prompt files in VS Code (GitHub Copilot)](https://code.visualstudio.com/docs/copilot/customization/prompt-files) |
| `github-copilot` | `argument-hint` | presentation | hint text shown in the chat input | [Prompt files in VS Code (GitHub Copilot)](https://code.visualstudio.com/docs/copilot/customization/prompt-files) |
| `github-copilot` | `compatibility` | presentation | informational environment requirements of the skill | [Agent Skills specification (frontmatter fields)](https://agentskills.io/specification) |
| `github-copilot` | `description` | presentation | descriptive label of an instructions file | mirrors canonical `title` |
| `github-copilot` | `disable-model-invocation` | behaviour | stops the cloud agent from choosing the custom agent automatically | [Custom agents configuration](https://docs.github.com/en/copilot/reference/custom-agents-configuration) |
| `github-copilot` | `excludeAgent` | behaviour | stops Copilot code review or Copilot cloud agent from using the instructions | [Adding repository custom instructions for GitHub Copilot](https://docs.github.com/en/copilot/how-tos/copilot-on-github/customize-copilot/add-custom-instructions/add-repository-instructions) |
| `github-copilot` | `infer` | behaviour | retired switch for automatic selection of the custom agent | [Custom agents configuration](https://docs.github.com/en/copilot/reference/custom-agents-configuration) |
| `github-copilot` | `license` | presentation | license covering the skill | [Agent Skills specification (frontmatter fields)](https://agentskills.io/specification) |
| `github-copilot` | `mcp-servers` | security | additional MCP servers and tools granted to the custom agent | [Custom agents configuration](https://docs.github.com/en/copilot/reference/custom-agents-configuration) |
| `github-copilot` | `metadata` | presentation | free-form key-value annotations | [Agent Skills specification (frontmatter fields)](https://agentskills.io/specification) |
| `github-copilot` | `model` | behaviour | language model used to run the prompt | [Prompt files in VS Code (GitHub Copilot)](https://code.visualstudio.com/docs/copilot/customization/prompt-files) |
| `github-copilot` | `target` | behaviour | environment a custom agent is meant for | [Custom agents configuration](https://docs.github.com/en/copilot/reference/custom-agents-configuration) |
| `github-copilot` | `tools` | security | tools or tool sets available to the prompt | [Prompt files in VS Code (GitHub Copilot)](https://code.visualstudio.com/docs/copilot/customization/prompt-files) |
| `github-copilot` | `user-invocable` | behaviour | whether a user can select the custom agent | [Custom agents configuration](https://docs.github.com/en/copilot/reference/custom-agents-configuration) |
| `kiro` | `allowedTools` | security | tools the agent may use without prompting | [Kiro custom agent configuration reference](https://kiro.dev/docs/custom-agents/configuration-reference/) |
| `kiro` | `compatibility` | presentation | informational environment requirements of the skill | [Agent Skills specification (frontmatter fields)](https://agentskills.io/specification) |
| `kiro` | `description` | presentation | when a steering document applies | [Kiro steering documents](https://kiro.dev/docs/steering/); mirrors canonical `activation.trigger` |
| `kiro` | `hooks` | security | commands run at agent lifecycle trigger points | [Kiro custom agent configuration reference](https://kiro.dev/docs/custom-agents/configuration-reference/) |
| `kiro` | `includeMcpJson` | security | grants the MCP servers from workspace and global configuration | [Kiro custom agent configuration reference](https://kiro.dev/docs/custom-agents/configuration-reference/) |
| `kiro` | `inclusion` | presentation | steering inclusion mode | [Kiro steering documents](https://kiro.dev/docs/steering/); mirrors canonical `activation` |
| `kiro` | `keyboardShortcut` | presentation | shortcut for switching to the agent | [Kiro custom agent configuration reference](https://kiro.dev/docs/custom-agents/configuration-reference/) |
| `kiro` | `license` | presentation | license covering the skill | [Agent Skills specification (frontmatter fields)](https://agentskills.io/specification) |
| `kiro` | `mcpServers` | security | MCP servers the agent has access to | [Kiro custom agent configuration reference](https://kiro.dev/docs/custom-agents/configuration-reference/) |
| `kiro` | `metadata` | presentation | free-form key-value annotations | [Agent Skills specification (frontmatter fields)](https://agentskills.io/specification) |
| `kiro` | `name` | presentation | steering identifier | [Kiro steering documents](https://kiro.dev/docs/steering/); mirrors canonical `activation.invocationName` |
| `kiro` | `permissions` | security | capability-based access control rules | [Kiro custom agent configuration reference](https://kiro.dev/docs/custom-agents/configuration-reference/) |
| `kiro` | `resources` | context | files, skills and knowledge bases loaded for the agent | [Kiro custom agent configuration reference](https://kiro.dev/docs/custom-agents/configuration-reference/) |
| `kiro` | `toolAliases` | behaviour | remapped tool names | [Kiro custom agent configuration reference](https://kiro.dev/docs/custom-agents/configuration-reference/) |
| `kiro` | `toolsSettings` | security | per-tool configuration, including shell and filesystem rules | [Kiro custom agent configuration reference](https://kiro.dev/docs/custom-agents/configuration-reference/) |
| `kiro` | `welcomeMessage` | presentation | message shown when switching to the agent | [Kiro custom agent configuration reference](https://kiro.dev/docs/custom-agents/configuration-reference/) |

## What "lossy" means here

A mapping is `lossy` when canonical information cannot be represented by the
target. Stemma never reports such a mapping as `exact`, always attaches at least
one diagnostic, and lets you accept the loss explicitly in the target profile
once you have reviewed it.
