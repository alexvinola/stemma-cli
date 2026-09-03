# The compiler pipeline

Stages, in order, with the guarantees each one provides.

## 1. Discover

`internal/discovery` walks the workspace and classifies **paths only**. It never
opens a file. A path that is not in the registry, or that is not a safe
normalized repository path, is ignored.

The registry is the complete list of files Stemma will ever open:

```
.github/copilot-instructions.md
.github/instructions/**/*.instructions.md
.github/prompts/**/*.prompt.md
.github/skills/*/SKILL.md
.github/agents/*.md
CLAUDE.md
.claude/CLAUDE.md
.claude/rules/**/*.md
.claude/skills/*/SKILL.md
.claude/agents/*.md
AGENTS.md
AGENTS.override.md
**/AGENTS.md
**/AGENTS.override.md
.agents/skills/*/SKILL.md
.kiro/steering/**/*.md
.kiro/skills/*/SKILL.md
.kiro/agents/*.json
```

Heavy directories (`.git`, `node_modules`, `dist`, `vendor`, …) are skipped.
Symlinked files and directories are never followed or reported. Walking is
bounded by depth, file count, per-file size and total size; hitting a limit is
reported, not silently ignored.

Detection reports `high` confidence when a primary entry point is present
(`copilot-instructions.md`, an instructions file, `CLAUDE.md`, `.claude/rules`,
`AGENTS.md`, `.kiro/steering`) and `medium` when only secondary files are found.

## 2. Read

Only registered paths are read, through `workspace.ReadFile`, which refuses
symlinks, enforces the size limits and records a hash.

## 3. Parse

`internal/parser` produces a `Document` with:

- BOM detection and line-ending detection (LF, CRLF or mixed).
- UTF-8 validation — invalid encoding is an error diagnostic, and the file is
  preserved as an opaque block rather than interpreted.
- Front matter only at the very start of the document, in a restricted YAML
  subset: scalars, block and flow sequences, nested mappings, block scalars.
  Tags, anchors, aliases, merge keys and multi-document streams are refused
  (`STEMMA1103`). Size, line count, nesting depth and key count are bounded.
- Fence-aware section splitting: `#`, `---` and bullets inside a fenced or
  indented code block are content, never structure.
- Byte and line spans for every section.

Parsing never panics and never fails outright; problems become diagnostics.

## 4. Import

Each provider adapter turns its parsed representation into canonical entities.
Aggregate instruction files are split into units by `SplitDocument`, which picks
the split level structurally: a document that opens with a single top-level
heading is split one level deeper, so the title's own prose becomes the
preamble and subheadings stay inside their parent unit.

Everything that is not modelled is preserved: unknown front matter keys become
provider extensions, unparseable or unmodelled files become opaque blocks.

## 5. Validate

`canonical.Validate` checks schema version, required fields, id shape and
uniqueness, activation union invariants, glob syntax, provenance consistency,
opaque block hashes and unsafe agent tool names. Everything caused by user input
is a diagnostic, never a Go error.

## 6. Load the target profile

`.stemma/profiles/<target>.json`, or a default empty profile. The profile is
validated against the project: overrides for unknown entities are warnings,
unsafe directories or filenames are errors.

## 7. Resolve

For each entity, `adapters.Resolve` produces:

- whether it is included for this target (canonical `enabled`, profile
  `include`, and `documentation-only` all feed into this),
- the activation to use for this target,
- the destination directory and filename if pinned,
- the agent-facing text, and whether a content override replaced it.

A profile can never re-enable an entity that is disabled in the canonical
project: canonical truth wins over delivery.

## 8. Optimize

`internal/optimizer` runs only passes that can be explained without a language
model:

- remove byte-identical duplicates,
- remove duplicates that are identical after conservative whitespace
  normalization (used for comparison only — stored content is never rewritten),
- keep the lexicographically smallest id so the result never depends on input
  order,
- report a removed duplicate as `skipped-explicitly` with an informational
  diagnostic.

Not allowed, ever: paraphrasing, summarising, merging things that "seem
similar", inventing triggers, directories or capabilities.

## 9. Lower and render

The exporter consults `internal/capabilities` and writes provider files, and
records exactly one projection outcome per entity. Rendering is deterministic:
sorted maps, sorted lists, fixed section order.

## 10. Reuse unchanged sources

Before rendering a file, the builder checks whether the destination is also the
file its content came from and nothing relevant changed. If so, the original
bytes are re-emitted verbatim. See `docs/round-trip.md`.

## 11. Diff and classify

`BuildPlan` hashes each destination and compares it with the generated content
and with the manifest:

| Situation | Kind |
| --- | --- |
| Destination does not exist | `create` |
| Content already identical | `unchanged` |
| Generated by Stemma, still matching the manifest | `update` |
| Generated by Stemma but edited since | `conflict` |
| Exists but never generated by Stemma | `conflict` (or `update` with `--adopt-untracked`) |
| Generated before, no longer produced | `delete-proposed` |

## 12. Apply

`Apply` refuses to run when blocking diagnostics are present, re-checks every
destination hash recorded in the plan (a mismatch is `STEMMA4101` and exit code
4), then writes everything — including the updated manifest — in a single
transaction.

Deletions are never executed.

## Invariants the compiler asserts

- Every canonical entity and every opaque block receives exactly one projection
  outcome. A missing or duplicated outcome is an internal invariant failure
  (exit code 6), not silent output.
- Every generated path is a safe, normalized, workspace-relative path.
- Generated content is valid UTF-8.
- The same input, configuration and version produce identical files,
  diagnostics, ordering, hashes and exit code.
