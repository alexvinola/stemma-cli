# The canonical model

The canonical project under `.stemma/` is the source of truth once a repository
has been imported. Provider files are projections of it.

Schema: `schema/canonical-v2.schema.json`. Go types: `internal/canonical`;
on-disk layout: `internal/store`.

## Layout

```
.stemma/
├── project.json      # metadata: id, name, targets, token budgets
├── context/<slug>.md # one file per entity, named after the entity id
├── rules/<slug>.md
├── procedures/<slug>.md
├── skills/<slug>.md
├── agents/<slug>.md
├── decisions/<slug>.md
├── provenance.json   # machine bookkeeping (see below)
├── profiles/<target>.json
└── manifest.json
```

Entities are Markdown because almost everything in them *is* Markdown. Holding
multi-line prose inside JSON strings made the file people are supposed to edit
the least pleasant one in the repository, and produced diffs where changing one
word rewrote a whole line. The file name is the entity's slug, so the
filesystem mirrors the entity ids: `rule.api-validation` is
`.stemma/rules/api-validation.md`.

Structured metadata goes in YAML front matter, in the same restricted subset
Stemma parses everywhere else — no tags, anchors or aliases. Prose fields that
are not the main body become recognised `## Heading` sections. Anything under an
unrecognised heading stays part of the body, so nothing a person writes is lost.

There are two serializations of a project, and they have different jobs:

| Form | Where | Job |
| --- | --- | --- |
| Markdown + `project.json` | `.stemma/` | what people read and edit |
| One canonical JSON document | in memory only | giving a project exactly one byte form to hash, which is what manifests and plans compare |

The second never touches disk except in compact test fixtures.

## Entity identifiers

Every entity has a stable id of the form `<entityType>.<slug>`:

```
context.api-layer-conventions
rule.controller-repository
skill.release-checklist
```

Slugs are lowercase ASCII letters, digits and single hyphens, derived
deterministically from the title or name. Text that reduces to nothing (for
example a title written entirely in a non-Latin script) falls back to a hash of
the source path, so ids stay stable and unique without depending on locale.

Collisions are resolved by appending `-2`, `-3`, … in allocation order. Profile
overrides are keyed by these ids, so renaming an entity is a deliberate act.

## Activation

Activation is an exhaustive tagged union:

| Type | Meaning | Extra fields |
| --- | --- | --- |
| `always` | Loaded into every request | — |
| `path-scoped` | Loaded when matching files are in scope | `include`, optional `exclude` |
| `on-demand` | Loaded only when explicitly invoked | `trigger`, `invocationName` |
| `documentation-only` | Never loaded into agent context | — |

The zero value is invalid on purpose: a forgotten assignment is a validation
error, not an accidental "always-on". Fields that do not belong to the tag must
be empty, and a `path-scoped` activation must carry at least one include
pattern.

`documentation-only` entities are never projected into agent-facing output. A
target profile can override the activation, which makes the decision explicit
and visible.

## Entities

### Context document

Durable prose guidance: `id`, `title`, `kind`, `content`, `audience`,
`activation`, provenance, extensions.

`audience` is `agent`, `human` or `both`. A document whose audience is `human`
is never projected into agent-facing output, whatever its activation says; it is
reported as `skipped-explicitly` with that reason.

Kinds are `product`, `technology`, `architecture`, `structure`, `domain`,
`conventions`, `security`, `testing`, `operations`, `other`.

A kind is only assigned when there is a **structural** reason: a documented
foundation filename (Kiro's `product.md`, `tech.md`, `structure.md`) or an exact
match in the known-headings table (`internal/adapters/sections.go`). Otherwise
the kind is `other`. Stemma never infers a kind from arbitrary prose.

### Rule

A single actionable instruction: `id`, `title`, `instruction`, `priority`
(`must` / `should` / `may`), `enabled`, `activation`, plus optional
`rationale`, `goodExamples` and `badExamples`.

Only `instruction` is necessarily agent-facing. On disk the instruction is the
body of the rule file, and rationale and examples are `## Rationale`,
`## Good examples` and `## Bad examples` sections after it — so the split
between "what the agent is told" and "why we decided this" is visible while you
edit. A test enforces that the human-only parts never leak into generated files.

### Procedure

An ordered, invocable workflow: `name`, `description`, optional `trigger`,
`content`. Copilot has a native prompt-file format for these; the other
supported providers deliver them as skills, which is reported as `adapted`.

### Skill

Reusable on-demand capability documentation: `name`, `description`, `content`,
optional `allowedTools` and `invocationPolicy`. All four implemented providers
support skills natively.

### Specialist agent

`name`, `description`, `instructions`, `tools`, and `modelPreference` as
**opaque provider metadata**. Stemma never translates tool or model names
between providers: when an agent crosses providers with a tool list, the
mapping is `lossy` and `STEMMA3301` asks a human to check the names.

### Architecture decision

`title`, `status`, `context`, `decision`, `consequences`, `agentConstraints`.
Only `agentConstraints` is normally projected into agent-facing context; the
rest is human documentation. A decision record without agent constraints is
`skipped-explicitly` with that explanation.

## Provenance

Every imported entity records where it came from: source format, source path,
source hash, byte and line span where known, importer version, and a
disposition (`parsed`, `adapted`, `preserved-opaque`).

This lives in `.stemma/provenance.json`, not in the entity files, deliberately:
it is bookkeeping the machine maintains, and it would be noise in a file a
person is editing. Deleting it degrades gracefully — Stemma regenerates files
instead of re-emitting original bytes — rather than losing information.

Provenance also records a `contentHash`: the digest of the entity exactly as the
importer produced it. Re-emitting a source file verbatim requires that hash to
still match, so editing an entity's Markdown file always regenerates rather than
silently keeping the old bytes.

Provenance is what makes `stemma explain` able to trace a generated line back to
the file it came from, and what makes byte-identical round trips provable.

## Opaque blocks

Content Stemma refuses to interpret is preserved verbatim as an opaque block
with its provider, source path, span, hash, a human-readable reason, and a flag
saying whether it must be re-emitted for same-format round trips.

Examples: a file whose front matter could not be parsed safely, a heading with
no body, an `AGENTS.override.md` whose override semantics are not modelled.

Opaque blocks receive projection outcomes like any other entity: `exact` when
re-emitted into the same format, `lossy` when they belong to this target but
their file is no longer generated, `skipped-explicitly` when they belong to a
different provider.

## Provider extensions

Unrecognised front matter keys and provider-specific values are preserved under
`extensions.<provider>.<key>` rather than dropped. When exporting back to the
same provider they are re-emitted.

Keys starting with `stemma.` are reserved. They are Stemma's own round-trip
hints — the original file name of a rule, the directory name of a skill, which
JSON key held an agent's instructions — and are never written into generated
provider files.

## Token budgets

Optional advisory limits (`alwaysOn`, `worstCaseRequest`). Exceeding one
produces `STEMMA5001`. With no budget set, an unusually large always-on context
produces the informational `STEMMA5002`.
