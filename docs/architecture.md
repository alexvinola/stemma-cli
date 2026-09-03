# Architecture

Stemma is a compiler. It has a front end (importers), a middle (canonical model,
validation, optimization), and a back end (target profiles, projection,
exporters), plus a transactional writer.

```
 provider files
      │  discovery: classify paths (never opens source code)
      ▼
 registered configuration files
      │  parser: fence-aware Markdown + restricted front matter
      ▼
 provider-specific parsed representation
      │  importer: normalize into canonical entities,
      │            preserve unknown content
      ▼
 canonical project  (.stemma/: Markdown entities + project.json)
      │  validation, deterministic optimization passes
      ▼
 canonical project + target profile (.stemma/profiles/<target>.json)
      │  resolution: activation, inclusion, destination
      │  capabilities: what can this provider express?
      ▼
 projection mappings + rendered files (in memory)
      │  diff against the working tree and the manifest
      ▼
 plan (create / update / unchanged / delete-proposed / conflict)
      │  apply: re-check hashes, temp files, atomic rename, rollback
      ▼
 provider files + updated manifest (.stemma/manifest.json)
```

## Packages

| Package | Responsibility |
| --- | --- |
| `internal/version` | Compile-time versions and the compatibility baseline |
| `internal/diagnostics` | Stable codes, severities, fingerprints, ordering |
| `internal/globs` | The glob dialect: matching, validation, directory derivation |
| `internal/provenance` | Where content came from; hashing |
| `internal/canonical` | The provider-neutral model, IDs, JSON codec, validation |
| `internal/tokenestimate` | Replaceable local estimator and the cost report |
| `internal/parser` | Markdown splitting and the restricted front matter subset |
| `internal/workspace` | Every filesystem effect: paths, limits, walking, transactions |
| `internal/manifest` | What was imported and generated, with hashes |
| `internal/profiles` | Target projection profiles |
| `internal/capabilities` | What each provider can express, with sources and dates |
| `internal/discovery` | Path-based detection of provider configuration |
| `internal/optimizer` | Safe, explainable optimization passes |
| `internal/adapters` | Adapter contracts and shared import/export machinery |
| `internal/adapters/<provider>` | One importer and one exporter per provider |
| `internal/adapters/registry` | Explicit target → adapter mapping |
| `internal/compiler` | Import driver, pure `Compile`, `BuildPlan`, `Apply` |
| `internal/store` | The on-disk layout of `.stemma/`: entity Markdown files, metadata, provenance |
| `internal/cli` | Flags, human and JSON output, exit codes |

The dependency graph is acyclic and flows downwards through that table.

## Purity boundary

`compiler.Compile` is a pure function of `(project, options)`. It does not read
files, does not print and does not exit. Everything it needs from disk —
existing file bytes for byte-identical reuse — is passed in through
`CompileOptions.Originals`.

`BuildPlan` adds reading (hashing destinations, loading originals). `Apply` is
the only function that writes, and it writes through `workspace.Transaction`.

This split is what makes the compiler testable without a filesystem and what
guarantees that `plan`, `scan`, `check` and `explain` cannot modify anything.

## Canonical model

See `docs/canonical-model.md`. The short version: rules, context documents,
procedures, skills, specialist agents and architecture decisions, each with a
stable id, an activation, provenance, provider extensions — plus opaque blocks
for content Stemma deliberately did not interpret.

## Projection profiles

A profile answers "how should this target receive this entity?" It can include
or exclude an entity, change its activation for that target only, pin a
directory or filename, accept a known lossy mapping, or (explicitly, and always
with a diagnostic) replace the wording.

A profile never changes canonical meaning. Two targets can disagree about
delivery while agreeing about truth.

## Diagnostic model

See `docs/diagnostics.md`. Diagnostics are structured values with stable codes
and stable fingerprints, sorted deterministically, present in both human and
JSON output. A fingerprint covers code, severity, path, entity and target — not
prose — so improving a message does not invalidate an acceptance recorded in a
profile.

## Round-trip strategy

See `docs/round-trip.md`. Same format with no semantic change re-emits the
original bytes; same format with changes regenerates while preserving opaque
blocks; a different format produces optimized semantic equivalence, not textual
identity.

## Transactional writing

`workspace.Transaction` snapshots each destination, writes to a temporary
sibling file, fsyncs, chmods and renames into place, in sorted path order. On
any failure it restores every file it already replaced and removes every file it
created. If restoring fails, it writes the original content and a recovery
report under `.stemma/recovery/` (mode 0600) and returns a `RollbackError`.

Existing file permissions are preserved; new files get 0644.

## Manifest semantics

`.stemma/manifest.json` records the imported sources and, per target, the
generated files with their hashes and contributing entities. This is what lets
Stemma distinguish:

- a file it generated and that is unchanged → `unchanged`
- a file it generated that needs new content → `update`
- a file it generated that someone edited → `conflict`
- a file it never generated → `conflict` unless `--adopt-untracked`
- a file it used to generate and no longer produces → `delete-proposed`

A timestamp may be recorded after a successful apply. It never participates in
hashing, planning or generated output.

## Token approximation

The default estimator is:

```
tokens = ceil( max( characters / 4 , words × 1.3 ) )
```

It is deliberately conservative and always labelled approximate. `Estimator` is
an interface, so a different local estimator can be substituted without
touching adapters. Stemma does not report monetary costs: the product metric is
context reduction, not a speculative price.
