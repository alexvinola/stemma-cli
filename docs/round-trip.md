# Round trips and provenance

## Same format, no semantic change

**Guarantee: byte-identical, including line endings and any byte order mark.**

When the destination is also the file the content came from, Stemma re-emits the
original bytes instead of regenerating them. Reuse is only allowed when all of
the following hold:

- the destination path is one of the files that were imported,
- every entity written to that file records that file as its source path,
- the file's hash still matches the hash recorded at import,
- every one of those entities is still byte-for-byte what the importer produced
  (each records a `contentHash` in `.stemma/provenance.json`, so editing an
  entity's Markdown file disables reuse rather than being silently discarded),
- the set of entities written to the file is exactly the set imported from it,
- no profile override changes inclusion, activation, directory, filename or
  content for any of those entities.

If any condition fails, the file is regenerated. This check is implemented once,
in `adapters.ReuseOriginal`, and applies to every provider.

`TestSameFormatRoundTripIsByteIdentical` imports each provider fixture and
compiles straight back to the same provider: every file must be classified
`unchanged`, and the plan must propose nothing at all.

## Same format, with changes

Stemma regenerates the affected file and preserves every opaque block that
belongs to it, re-emitting them in source order. Unrelated files are still
reused byte-for-byte, so a change to one rule does not rewrite the whole
configuration.

Stemma does not attempt a minimal textual patch of a file it regenerates: it
cannot prove such a patch is safe in general, so it regenerates and keeps the
preserved blocks. Regeneration of a file that Stemma also imported is reported
with `STEMMA3701`. That diagnostic is informational rather than a warning,
because regenerating after a real change is the normal path and CI running
`check --warnings-as-errors` should not fail on it — but it is always present in
`--json` output and in `plan --explain`.

## Different format

The goal is optimized semantic equivalence, not textual identity. Content is
re-packaged into the target's native mechanisms, which is exactly the point of
the tool: the always-on set, the scoping mechanism and the file layout are all
allowed to differ.

`TestReimportPreservesEntities` compiles a project to another provider, writes
the result to a clean workspace, imports it back, and requires that every piece
of agent-facing text from the first project is still present.

## What does not survive a cross-format round trip

Being explicit about this matters more than pretending it is lossless:

- **Provider extensions** of the original provider are kept in the canonical
  model but are not written into a different provider's files.
- **Entity ids and file names** are re-derived from titles in the target
  layout, so a Copilot instructions file called `api.instructions.md` may come
  back as a Claude rule named after its description.
- **Specialist agents** flattened into `AGENTS.md` come back as ordinary
  context, not as agents. This is reported as `lossy` at compile time.
- **Exclude patterns** are written only as a human-readable scope note in
  providers that cannot express them, so re-importing yields the note as text,
  not as an exclusion.
- **Rule priorities** survive in providers where rules are rendered as
  `**MUST** …` bullets, but a re-import treats the whole file as one unit unless
  the target format makes rules structurally explicit.

## Change classification

| Kind | Meaning |
| --- | --- |
| `create` | The destination does not exist |
| `update` | Stemma generated the file and it needs new content |
| `unchanged` | The file already has exactly the generated content |
| `delete-proposed` | Stemma generated it before and no longer produces it — reported only, never executed |
| `conflict` | The file exists with content Stemma did not write, or was edited after Stemma wrote it |

## Stale plan protection

A plan records the hash of every destination it inspected. `apply` re-hashes all
of them before writing anything. Any difference — including a file that appeared
or disappeared — aborts with `STEMMA4101` and exit code 4, and the repository is
left untouched.

## Saved plans are input, never authority

A plan file exists to be committed, reviewed and replayed, which is exactly the
workflow in which a pull request can rewrite it. Stemma therefore never writes
the bytes a saved plan carries. `apply --plan` does three things before touching
the repository:

1. **Structural checks.** The plan must come from this build (schema version,
   Stemma version, provider baseline), target an implemented provider, and every
   change must name a unique, normalized repository path with a known kind. For
   anything that would be written, the declared `newHash` must be the hash of
   the content the plan carries. The file must also hold exactly one JSON
   document.
2. **Rebuild.** The plan is recompiled from the current canonical project,
   profile and manifest.
3. **Comparison.** The saved plan must describe exactly what that rebuild
   produces — same paths, kinds, content, hashes and diagnostics. Any drift in
   the project, the profile, the ownership of a destination or the flags in use
   is refused with exit code 4.

Only then does the apply proceed, and it proceeds with the **rebuilt** plan. The
saved document is an assertion about what compiling would do, so editing it can
change whether the apply is allowed, never what gets written. In particular a
saved plan cannot adopt an untracked file that `--adopt-untracked` was not given
for, because the rebuild classifies that destination as a conflict and the two
plans no longer agree.

## Provenance

Every imported entity records its source format, source path, source hash, byte
and line span, importer version and disposition. `stemma explain` prints it:

```
context.api-layer-conventions  (context)
  title:        API layer conventions
  target:       codex
  activation:   paths src/api/**, src/handlers/**
  imported from: .github/instructions/api.instructions.md (github-copilot, parsed)
                lines 1-11
  profile:      no override
  outcome:      lossy
  destination:  AGENTS.md
  reason:       No single directory could be derived from src/api/**, src/handlers/**,
                so the content stays in the root AGENTS.md and is no longer scoped.
  est. tokens:  ~24 (approximate)
```

The same entity compiled for Claude Code is `exact`, because `paths:` front
matter can carry both patterns. That difference between targets, made visible
per entity, is the point of the command.
