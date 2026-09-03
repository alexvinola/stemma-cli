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

## GitHub Copilot

| Item | Status | Notes |
| --- | --- | --- |
| `.github/copilot-instructions.md` | Implemented | Split into always-on context documents by heading |
| `.github/instructions/**/*.instructions.md` | Implemented | `applyTo` is a comma-separated glob list |
| `.github/prompts/**/*.prompt.md` | Implemented | Imported as procedures; `mode`, `model` and other keys preserved as extensions |
| `.github/skills/*/SKILL.md` | Implemented | Skills round-trip natively |
| `.github/agents/*.md` | Implemented | Markdown with `name`, `description`, `tools` front matter |
| `applyTo` exclude patterns | Lossy | The documented front matter has no negative syntax. Canonical excludes produce `STEMMA3101` and are written only as a scope note in the file body |
| `excludeAgent` and other unknown keys | Partial | Preserved as provider extensions; not interpreted |
| `AGENTS.md` / `CLAUDE.md` fallbacks | Unsupported by design | Copilot also reads these, but Stemma never writes them *for the Copilot target*, so two targets never own one file |

Sources, last verified 2026-09-02:

- [Adding repository custom instructions for GitHub Copilot](https://docs.github.com/en/copilot/how-tos/configure-custom-instructions/add-repository-instructions)
  — confirms the three instruction file locations, the `applyTo` front matter,
  comma-separated multiple patterns, and that Copilot also reads `AGENTS.md`
  and `CLAUDE.md`.
- [About agent skills](https://docs.github.com/en/copilot/concepts/agents/about-agent-skills)
  — confirms `.github/skills`, `.claude/skills` and `.agents/skills` with
  `SKILL.md`.
- [Creating custom agents for Copilot cloud agent](https://docs.github.com/en/copilot/how-tos/copilot-on-github/customize-copilot/customize-cloud-agent/create-custom-agents)
  — confirms Markdown agent profiles with YAML front matter.

## Claude Code

| Item | Status | Notes |
| --- | --- | --- |
| `CLAUDE.md`, `.claude/CLAUDE.md` | Implemented | Always-on project instructions; the file that was imported is written back |
| `.claude/rules/**/*.md` | Implemented | Discovered recursively; directory name makes "rule" structurally explicit |
| `paths:` front matter | Implemented | Maps to path-scoped activation; a rule without `paths` is always-on |
| `.claude/skills/*/SKILL.md` | Implemented | Skills round-trip natively |
| `.claude/agents/*.md` | Implemented | `name`, `description`, `tools`, `model` |
| Exclude patterns | Lossy | `paths` has no documented negative syntax; `STEMMA3101` |
| Procedures | Lossy → Adapted | No native procedure format; exported as skills, reported as `adapted` |
| `@path` imports | Partial | Preserved verbatim as text, never resolved. Stemma warns that imported files still enter the context window, so imports are **not** presented as a context reduction |
| Brace expansion (`{ts,tsx}`) | Partial | Passed through verbatim; Stemma's own matcher treats braces literally |
| `CLAUDE.local.md`, user- and policy-scope files | Unsupported | Personal or machine-level files are out of scope for a repository compiler |
| Auto memory (`~/.claude/projects/**`) | Unsupported | Machine-local, written by the agent, not repository configuration |

Source, last verified 2026-09-02:
[How Claude remembers your project](https://code.claude.com/docs/en/memory)
— confirms `CLAUDE.md` and `.claude/CLAUDE.md`, `.claude/rules/` with recursive
discovery, `paths:` front matter with multiple glob patterns and brace
expansion, and that `@`-imports still load into context at launch.

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

## Kiro

| Item | Status | Notes |
| --- | --- | --- |
| `.kiro/steering/*.md` | Implemented | `inclusion: always` (documented default when absent) |
| `inclusion: fileMatch` | Implemented | `fileMatchPattern` accepts one pattern or an array |
| `inclusion: manual` | Implemented | Imported as on-demand with an invocation name |
| `inclusion: auto` | Implemented | Imported as on-demand with a trigger description; the mode is preserved and written back |
| `product.md`, `tech.md`, `structure.md` | Implemented | Documented foundation files, so their canonical kind is assigned by file name |
| `.kiro/skills/*/SKILL.md` | Implemented | Skills round-trip natively |
| `.kiro/agents/*.json` | Implemented | `name`, `description`, `prompt`/`instructions`, `tools`, `model`; unknown fields such as `resources` are preserved as extensions |
| Duplicate JSON keys | Rejected | `STEMMA1502`; the file is preserved as an opaque block rather than guessed at |
| Exclude patterns | Lossy | `fileMatchPattern` has no negative syntax; `STEMMA3101` |
| Global `~/.kiro/steering/` | Unsupported | Outside the repository |
| Procedures | Adapted | Delivered as skills |

Source, last verified 2026-09-02:
[Kiro steering documents](https://kiro.dev/docs/steering/) — confirms
`.kiro/steering/`, the four inclusion modes, that `fileMatchPattern` accepts
single or multiple patterns, and the three foundation files.

## Cursor

| Item | Status |
| --- | --- |
| `.cursor/rules/*.mdc` import | Planned |
| `.cursor/rules/*.mdc` export | Planned |
| Capability row | Declared, `available: false` |

Stemma declares the `cursor` target identifier so that profiles and reports can
reference it, and **refuses to compile for it**: `stemma plan --target cursor`
fails with `STEMMA3001` and exit code 3. Nothing about Cursor is simulated.

## What "lossy" means here

A mapping is `lossy` when canonical information cannot be represented by the
target. Stemma never reports such a mapping as `exact`, always attaches at least
one diagnostic, and lets you accept the loss explicitly in the target profile
once you have reviewed it.
