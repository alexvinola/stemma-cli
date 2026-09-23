# The canonical intermediate representation

Stemma's canonical project is its provider-neutral intermediate representation
(IR). Provider configuration files are inputs to importers and outputs from
exporters; after import, neither a provider file nor a provider's schema is the
semantic source of truth.

The editable files under `.stemma/` are a durable serialization of the IR, not
the IR itself. Loading them reconstructs the same in-memory `canonical.Project`;
saving a project renders that value back to Markdown entity files and JSON
bookkeeping. This names the existing schema boundary and does not migrate or
change schema version 2.

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

IR entities are serialized as Markdown because almost everything in them *is*
Markdown. Holding
multi-line prose inside JSON strings made the file people are supposed to edit
the least pleasant one in the repository, and produced diffs where changing one
word rewrote a whole line. The file name is the entity's slug, so the
filesystem mirrors the entity ids: `rule.api-validation` is
`.stemma/rules/api-validation.md`.

Structured metadata goes in YAML front matter, in the same restricted subset
Stemma parses everywhere else — no tags, anchors or aliases. Prose fields that
are not the main body become recognised `## Heading` sections. Anything under an
unrecognised heading stays part of the body, so nothing a person writes is lost.

Every canonical entity file requires a front matter block delimited by `---`.
Missing or unterminated front matter blocks loading with a diagnostic naming the
file. Present fields must have their expected types: strings for textual metadata,
booleans for `enabled`, and strings or lists containing only strings for tool lists
and activation patterns. The single-string list shorthand remains supported.
Optional fields may be omitted; a malformed value is not treated as omission.
`extensions` and each provider entry beneath it must be mappings; the provider's
extension values remain opaque and may have any supported YAML value type.

These checks belong to the canonical file reader, not the general Markdown
parser: a provider's ordinary Markdown file may legitimately have no front matter.

There are two serializations of the IR, and they have different jobs:

| Form | Where | Job |
| --- | --- | --- |
| Markdown + `project.json` | `.stemma/` | the durable, human-editable serialization |
| One canonical JSON document | in memory only | giving the IR exactly one byte form to hash, which is what manifests and plans compare |

The second never touches disk except in compact test fixtures.

## IR invariants

The six semantic entity types are context documents, rules, procedures, skills,
specialist agents and architecture decisions. Their shared contracts are:

- **Provider neutrality.** Modelled field names and meanings do not belong to a
  provider. Unrecognised provider-specific source keys are retained only under
  `extensions.<provider>.<key>`; explicitly modelled opaque values such as tool
  and model names are preserved without translation.
- **ActivationClosedUnion.** Activation has exactly the four tags documented
  below; the zero value, unknown tags and fields belonging to another tag are
  invalid. Only context documents and rules store activation in schema version
  2. Procedures, skills, agents and decisions receive an explicit activation
  when each target projects them; that activation may differ by target.
- **ProjectionActivationTotality.** Every projection mapping has one valid
  activation, including skipped and blocked mappings. This records the delivery
  decision even for entity types that do not store activation in the IR.
- **Stable semantic fingerprints.** `EntityFingerprint` covers every field of
  one of the six entities, including provider extensions, but excludes
  provenance. It therefore survives both canonical JSON and `.stemma/` storage
  round trips, and bookkeeping changes cannot make edited semantics look
  unchanged.
- **Complete imported provenance.** An imported entity records source format,
  source path and hash, importer version and disposition; its canonical content
  hash is stamped after import. A span is recorded where known. A hand-authored
  entity may instead have an entirely zero provenance value.

`OpaqueBlock` is deliberately outside the six-type semantic entity union. It is
an auxiliary loss-preservation record with its own provider, source, byte span,
content hash, reason and re-emission flag. It participates in projection
accounting so that preserved input also receives exactly one outcome, but it
does not acquire entity fields, activation or provider extensions.

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

Include and exclude patterns are brace-expanded when imported and before
projection, including patterns written directly in `.stemma/` or a profile.
For example, `src/**/*.{ts,tsx}` becomes `src/**/*.ts` and `src/**/*.tsx`.
Literal braces inside character classes such as `[{]` are preserved. Literal
commas remain valid canonical input but require a lossy diagnostic for Copilot.
Expansion is bounded at 1000 alternatives per pattern and 32 nested groups.
Validation rejects patterns beyond either bound because it cannot check every
alternative. Importers report a blocking `STEMMA2102`; no partial expansion or
unvalidated pattern is imported. Split a rejected pattern into smaller groups.

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
The IR does not store an activation for a procedure; exporters assign its
on-demand activation when projecting it.

### Skill

Reusable on-demand capability documentation: `name`, `description`, `content`,
optional `allowedTools` and `invocationPolicy`. All four implemented providers
support skills natively. The on-demand activation is assigned during
projection, not stored on the skill.

### Specialist agent

`name`, `description`, `instructions`, `tools`, and `modelPreference` as
**opaque provider metadata**. Stemma never translates tool or model names
between providers: when an agent crosses providers with a tool list, the
mapping is `lossy` and `STEMMA3301` asks a human to check the names.
Agent activation is also a projection decision: targets with native specialist
agents use on-demand delivery, while a target that must flatten an agent into
root instructions records always-on delivery.

### Architecture decision

`title`, `status`, `context`, `decision`, `consequences`, `agentConstraints`.
Only `agentConstraints` is normally projected into agent-facing context; the
rest is human documentation. A decision record without agent constraints is
`skipped-explicitly` with that explanation.
Decisions do not store activation; a projection that emits their agent
constraints records an explicit always-on activation.

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

Opaque blocks receive auxiliary projection outcomes: `exact` when
re-emitted into the same format, `lossy` when they belong to this target but
their file is no longer generated, `skipped-explicitly` when they belong to a
different provider.

## Provider extensions

Unrecognised front matter keys and provider-specific values are preserved under
`extensions.<provider>.<key>` rather than dropped. This is the only place in a
semantic entity where provider-specific schema keys belong. When exporting back
to the same provider they are re-emitted, including when the file is
regenerated rather than reused byte for byte.

Another provider cannot write them, and what that costs depends on what the key
does. Every key is classified per provider in `internal/capabilities`, with the
official documentation it was checked against:

| Kind | Examples | Losing it |
| --- | --- | --- |
| `presentation` | Kiro `welcomeMessage`, Claude `color`, skill `license`; keys that mirror a canonical field, such as Kiro steering `inclusion: always` or `inclusion: fileMatch` | Silent; the mapping outcome is unchanged |
| `context` | Kiro agent `resources`, Claude subagent `skills` | `lossy`, warning `STEMMA3801` naming the field |
| `behaviour` | Copilot `excludeAgent`, prompt `model`, Kiro `toolAliases`, Kiro steering `inclusion: manual` or `inclusion: auto` | `lossy`, warning `STEMMA3801` naming the field |
| `security` | Kiro `allowedTools`/`permissions`/`hooks`/`mcpServers`, Claude `permissionMode`/`disallowedTools` | `lossy`, blocking error `STEMMA3802` until its fingerprint is accepted in the target profile |

A key the table does not list is treated as `behaviour`: Stemma cannot tell
whether an unknown field changes what the agent does, so its loss is never
silent. The explanation of the mapping lists every reported field.

Some keys mean different things depending on their value, and are classified
per value. Kiro's steering `inclusion` is the case today: `always` and
`fileMatch` become their own canonical activations, but `manual` (loaded only
when a person references the file) and `auto` (loaded when a request matches
the description) both become `on-demand`, so the difference survives only in
the extension. Such a value is not lost where the target's on-demand delivery
has the same invocation mode (`onDemandInvocation` in the capability row):
Copilot's prompt files keep `manual`, Claude's and Codex's skills keep `auto`,
and Kiro writes either back. Elsewhere, or when a profile makes the entity
always-on, the mapping is `lossy` with `STEMMA3801` for
`extensions.kiro.inclusion`.

A field is promoted into the canonical model only when it is genuinely
interoperable across providers; tool lists and model preferences already are
(`tools`, `allowedTools`, `modelPreference`). The classified keys above stay
extensions because no other provider has an equivalent with the same meaning.

Provider-specific values at the project level — front matter on `CLAUDE.md`,
`.github/copilot-instructions.md` or `AGENTS.md` — are not attached to an entity
and are not yet part of this classification.

Keys starting with `stemma.` are reserved. They are Stemma's own round-trip
hints — the original file name of a rule, the directory name of a skill, which
JSON key held an agent's instructions — and are never written into generated
provider files.

## Token budgets

Optional advisory limits (`alwaysOn`, `worstCaseRequest`). Exceeding one
produces `STEMMA5001`. With no budget set, an unusually large always-on context
produces the informational `STEMMA5002`.
