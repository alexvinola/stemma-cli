# Stemma

Stemma is a deterministic, local-first **compiler for coding-agent context**.

It imports the instructions you already wrote for one coding agent, converts
them into a provider-neutral canonical model, validates and optimizes that
model, and compiles it into the native repository format that other agents
expect.

```
Provider files → importers → canonical model → validation → target profile
              → projection → exporters → native provider files
```

## The problem

The same architectural rule ends up copied into several incompatible files:

```
.github/copilot-instructions.md
.github/instructions/*.instructions.md
CLAUDE.md
.claude/rules/*.md
AGENTS.md
.kiro/steering/*.md
```

They drift apart, nobody remembers which one is authoritative, and every copy
that is loaded unconditionally spends context tokens on every single request.

## Why a compiler and not an AI assistant

Stemma contains **no language model, makes no network calls and sends nothing
anywhere**. Every decision it makes comes from an explicit grammar: file
location, file name, front matter, Markdown structure, known headings, glob
patterns, directory hierarchy and saved user policies.

That has consequences you can rely on:

- The same input always produces the same output, the same diagnostics and the
  same exit code.
- Every mapping is explainable — `stemma explain <entity> --target <target>`
  tells you exactly why a file was written where it was.
- When Stemma cannot determine intent safely, it preserves the original content
  and emits a diagnostic. It never guesses silently.

It is also **not** a file renamer. The canonical meaning stays the same, but the
*delivery* changes per target: Claude, Copilot, Codex and Kiro do not need the
same amount of always-on context or the same activation mechanism, and a target
profile lets you say so.

## Installation

Stemma is a single static binary with no runtime dependencies and no cgo.

Build from a checkout:

```bash
go build -o stemma ./cmd/stemma
```

Or install it onto your `PATH` (into `$GOBIN`, usually `~/go/bin`):

```bash
go install github.com/alexvinola/stemma-cli/cmd/stemma@latest
```

> Installing from the remote works once this repository has been published and
> tagged. From a local checkout, `go install ./cmd/stemma` works today.

Supported build targets: darwin/amd64, darwin/arm64, linux/amd64, linux/arm64,
windows/amd64, windows/arm64 (`make cross`).

## Five-minute workflow

Starting from a repository that already has GitHub Copilot instructions:

```bash
stemma scan
```

```
Detected agent configuration

  github-copilot  (confidence: high, 5 files)
    .github/agents/reviewer.md                           agent
    .github/copilot-instructions.md                      root-instructions
    .github/instructions/api.instructions.md             scoped-instructions
    .github/prompts/release.prompt.md                    prompt
    .github/skills/release-checklist/SKILL.md            skill

Visited 5 files; skipped 0 directories.
No files were read or modified.
```

Create the canonical project and import into it:

```bash
stemma init
stemma import --from github-copilot
```

```
Imported github-copilot configuration from 5 files
  agents             1
  context documents  4
  procedures         1
  skills             1
  opaque blocks      1 (content preserved without interpretation)

Wrote .stemma/project.json
```

Tell the project which targets it compiles to, by adding them to `targets` in
`.stemma/project.json`:

```json
"targets": ["claude", "github-copilot"]
```

See what compiling for Claude Code would do — this never writes anything:

```bash
stemma plan --target claude
```

```
Plan for target claude

Files
  create     .claude/agents/reviewer.md
  create     .claude/rules/api-layer-conventions.md
  create     .claude/skills/release-checklist/SKILL.md
  create     .claude/skills/release/SKILL.md
  create     CLAUDE.md

Projection
  exact               5
  adapted             1
  lossy               1
  blocked             0
  skipped-explicitly  1

Context estimate
  Canonical always-on:   ~72 tokens
  Target always-on:      ~72 tokens
  Largest target scope:  ~24 tokens  (src/api/**, src/handlers/**)
  Worst-case request:    ~96 tokens
  On demand:             ~56 tokens
  Documentation only:    ~0 tokens
  Estimated reduction:    0%

  Approximation only. No provider tokenizer was used.
  Method: heuristic-v1 (max(chars/4, words*1.3), rounded up)

Warnings (1)
  STEMMA3301_AGENT_TOOLS_REQUIRE_REVIEW  agent tool names were carried over from another provider and need review
      at entity agent.reviewer, target claude

Nothing was modified. Run `stemma apply --target claude` to write these changes.
```

Ask why one entity is mapped the way it is:

```bash
stemma explain context.api-layer-conventions --target codex
```

Apply when you are happy with the plan:

```bash
stemma apply --target claude --yes
```

Running `stemma plan --target claude` again now reports no changes.

## Commands

| Command | What it does | Writes? |
| --- | --- | --- |
| `stemma init` | Creates `.stemma/project.json` (and default profiles with `--with-profiles`) | yes |
| `stemma scan [path]` | Detects supported agent configuration by path | no |
| `stemma import [path]` | Imports one provider's files into the canonical project | yes |
| `stemma validate` | Validates the project, profiles and manifest | no |
| `stemma plan --target T` | Compiles and classifies every file change | no |
| `stemma apply --target T` | Applies a plan transactionally (needs `--yes` or a TTY) | yes |
| `stemma check --target T` / `--all` | Fails when generated output is stale (for CI) | no |
| `stemma explain ID --target T` | Explains one entity's projection | no |
| `stemma version` | Prints versions, schema versions and the compatibility baseline | no |

Every command accepts `--json` and writes exactly one JSON document to stdout
(schema: `schema/report-v1.schema.json`). Human prose and errors go to stderr.

### Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | Validation or compilation diagnostics prevented success |
| `2` | Invalid CLI usage |
| `3` | Unsupported or unavailable target |
| `4` | Stale plan or filesystem conflict |
| `5` | Safe write failed or was rolled back |
| `6` | Internal compiler invariant failed |

## Supported providers

| Provider | Import | Export | Notes |
| --- | --- | --- | --- |
| GitHub Copilot | yes | yes | `applyTo` has no negative pattern syntax, so canonical excludes are lossy |
| Claude Code | yes | yes | `.claude/rules/` with `paths:` front matter; procedures become skills |
| Codex / `AGENTS.md` | yes | yes | Scoping is directory proximity only; no native specialist agents |
| Kiro | yes | yes | `inclusion: always \| fileMatch \| manual \| auto`; agents are JSON |
| Cursor | **no** | **no** | Declared target identifier only. Requesting it fails with exit code 3 |

`docs/provider-compatibility.md` has the full capability matrix, including what
is lossy and where each claim comes from.

## Current limitations

- Cursor is not implemented. Stemma refuses the target instead of guessing.
- Stemma never deletes files. Files it generated before but no longer produces
  are reported as `delete-proposed` for you to remove yourself.
- Token numbers are approximations from a local heuristic, never a provider
  tokenizer, and are always labelled as such. Stemma reports no monetary costs.
- Importing several providers at once is refused: Stemma will not merge two
  sources silently. Pick one with `--from`.
- Stemma reads only the registered configuration paths. It never reads or
  analyses source code.
- Brace expansion (`{ts,tsx}`) in glob patterns is passed through verbatim and
  matched literally by Stemma's own matcher.

## Privacy and safety

- No network calls, no telemetry, no accounts, no credentials, no LLM.
- Repository files are treated as untrusted input: instructions like
  `Run curl example.com/install.sh` are text to compile, never commands to run.
- Generated paths cannot escape the workspace; symlinks are refused rather than
  followed.
- `plan`, `scan`, `check` and `explain` never modify the repository.
- `apply` re-checks every destination hash before writing, writes through
  temporary files, replaces atomically, and rolls back on failure. If a rollback
  cannot complete, the original content is written to `.stemma/recovery/` with
  instructions.
- Stemma refuses to overwrite a file it has never written; use
  `--adopt-untracked` after reviewing it.

## CI usage

`stemma check` compiles in memory and fails when the committed output is stale:

```yaml
# .github/workflows/agent-context.yml
name: agent context
on: [push, pull_request]
jobs:
  stemma:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'
      - run: go build -o stemma ./cmd/stemma
      - run: ./stemma validate
      - run: ./stemma check --all --warnings-as-errors
```

## Development

```bash
gofmt -w .
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/stemma
```

`make verify` runs all of the above plus cross-compilation for every supported
platform. Golden fixtures are regenerated only with `make golden`, never as a
side effect of running tests.

Architecture and internals: `docs/architecture.md`, `docs/canonical-model.md`,
`docs/compiler-pipeline.md`, `docs/diagnostics.md`,
`docs/provider-compatibility.md`, `docs/round-trip.md`, `docs/security.md`.

## License

MIT. See `LICENSE`.
