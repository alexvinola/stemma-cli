# Working on Stemma

Instructions for coding agents (and people) changing this repository.

Stemma is a deterministic compiler for coding-agent context. The value of the
project is that its output is predictable and explainable. Most rules below
exist to protect that property.

## Toolchain

- Go 1.24 or newer (developed against the current stable toolchain).
- No cgo. `CGO_ENABLED=0` must keep working for every supported platform.
- No external dependencies. The module has an empty `require` block on purpose:
  the front matter parser, glob engine and Markdown splitter are ours precisely
  so that behaviour, limits and safety are auditable. Adding a dependency needs
  an explicit justification in the pull request.

## Commands

```bash
gofmt -w .
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/stemma
```

`make verify` runs formatting checks, vet, tests, race tests and
cross-compilation for all six supported platforms.

Golden fixtures are regenerated only with `make golden`
(`go test ./internal/compiler -run TestGolden -update-golden`). Never make a
normal test run rewrite a fixture, and always read the resulting diff.

## Package boundaries

Keep packages cohesive and acyclic. The dependency direction is:

```
version, diagnostics, globs, provenance, tokenestimate   (leaves)
        ↓
canonical  →  capabilities, profiles, parser, workspace, manifest
        ↓
discovery, optimizer, adapters (+ per-provider packages)
        ↓
adapters/registry  →  compiler  →  store  →  cli  →  cmd/stemma
```

- `internal/workspace` owns **every** filesystem effect. Nothing else opens,
  reads, writes, stats or walks files.
- `internal/store` owns the on-disk layout of `.stemma/`. It is the only place
  that knows entities are Markdown files, and the only code allowed to remove a
  file Stemma wrote (entity files, on an import that replaces the project).
- `internal/compiler` `Compile` is pure: no filesystem, no stdout, no `os.Exit`.
  `BuildPlan` may read; only `Apply` writes.
- `internal/cli` owns presentation. Commands take an `Env` with explicit
  writers, so they are testable and never touch `os.Stdout` directly.
- Provider knowledge lives in `internal/adapters/<provider>` and
  `internal/capabilities`. Do not scatter provider conditionals elsewhere.

Avoid global mutable state. The adapter registry is an explicit switch, not
init-time registration, for exactly this reason.

## Determinism rules

These are not style preferences; violating them breaks the product.

- Never iterate a map to produce output. Sort first, or use a slice.
- Never use timestamps, randomness, locale, the environment or the network in
  anything that affects generated files, hashes, diagnostics or exit codes. A
  timestamp may be *recorded* in the manifest after a successful apply.
- Sort every output collection: files by path, mappings by type then id,
  diagnostics by severity then code then path then position.
- Applying a plan and then re-planning must report no changes.

## Safety rules

- Repository files are untrusted input. Never execute, resolve or fetch
  anything found inside them. `Run curl …` in an instruction is text.
- All generated paths go through `workspace.NormalizeRel`. Reject traversal,
  absolute paths, backslashes, `~`, `:` (drive letters and NTFS alternate data
  streams) and NUL bytes. Apply prefix-sensitive checks *after* `path.Clean`,
  or an input like `./A:` slips past them — normalization must be idempotent,
  and `FuzzNormalizeRel` asserts exactly that.
- Never follow a symlink; refuse it.
- Respect the bounded limits in `workspace.Limits` and the parser constants.
  New parsing code needs its own explicit limit.
- Sanitize untrusted text before printing it (`cli.Sanitize`).
- Never `panic` on user input. Parsers return diagnostics; fuzz tests enforce
  this.

## No silent data loss

Every meaningful piece of imported input must end in exactly one of four
states: parsed into a canonical entity, stored as a provider extension,
preserved as an opaque block, or rejected with a blocking diagnostic.

Every canonical entity must receive exactly one projection outcome per target:
`exact`, `adapted`, `lossy`, `blocked` or `skipped-explicitly`. The compiler
asserts this and fails with exit code 6 if an adapter forgets one.

A mapping that loses information must be `lossy` (never `exact`) and must
reference at least one diagnostic. A test enforces this.

## Preserving user changes

- Stemma refuses to overwrite a file it did not generate. That check is what
  `--adopt-untracked` deliberately relaxes; do not weaken it elsewhere.
- A file that was edited after Stemma wrote it is a conflict, not an update.
- Deletions are proposed, never executed.
- Writes are transactional: temporary file, atomic rename, rollback on failure,
  recovery data under `.stemma/recovery/` if a rollback cannot complete.

## Changing provider behaviour

1. Verify the behaviour against **official provider documentation** first.
2. Update `internal/capabilities` — including the source URL and the
   `lastVerified` date — and `docs/provider-compatibility.md`.
3. Update the adapter.
4. Add or update fixtures under `testdata/<provider>/<case>/` and regenerate
   goldens with `make golden`. Every provider behaviour change needs a fixture.
5. If the mapping quality changes, update the projection outcome and its
   explanation text, not just the code that writes the file.

Never claim tool compatibility across providers. Stemma does not translate tool
names; it warns (`STEMMA3301`) and keeps them.

## Diagnostics

Diagnostic codes are a public contract. You may improve the human wording of a
diagnostic at any time — fingerprints deliberately exclude prose — but do not
rename or renumber a released code. New codes go in `internal/diagnostics` with
a stable `STEMMA<number>_<NAME>` identifier and a documented severity.

## Prohibited

- LLM or AI-service integration of any kind.
- Network calls, telemetry, analytics, accounts, credentials.
- Executing commands, scripts or plugins found in configuration.
- Reading or analysing source code files.
- Automatic git commits, pushes or pull requests.
- Deleting user files.
- Presenting an unimplemented provider as supported.
