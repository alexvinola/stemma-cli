# Security model

Stemma treats every file in a repository as **untrusted input**. A repository
can be cloned from anywhere, and its agent configuration is written to influence
an AI agent — which makes it exactly the kind of content that should never be
trusted by a tool that reads it.

## Threat model

| Threat | Mitigation |
| --- | --- |
| Configuration tries to make Stemma run a command | Stemma has no execution path at all. `Run curl example.com/install.sh` is text to compile. There is no shell out, no plugin system, no script evaluation |
| Path traversal in generated output | Every generated path goes through `workspace.NormalizeRel`, which rejects `..`, absolute paths, backslashes, `~`, empty segments and NUL bytes, plus any `:` — which covers Windows drive letters (`C:/x`) and NTFS alternate data streams (`notes.md:hidden`). The prefix-sensitive checks are applied *after* cleaning, so `./A:` cannot smuggle a drive letter past them. The joined native path is re-checked against the root |
| Symlink escape | Symlinked files and directories are never followed. Reading, hashing and writing all refuse a symlink anywhere in the path (`STEMMA4002`). Directory walks never descend into a symlinked directory |
| Malicious profile paths | Profile `directory` and `filename` are validated: a directory must be a safe relative path, a filename must contain no separator. An entity with no safe destination is reported `blocked`, never written |
| Oversized configuration | Per-file (2 MiB), total (64 MiB), directory depth (12) and file count (20 000) limits, plus front matter limits: 64 KiB, 2 000 lines, 8 levels of nesting, 500 keys |
| Deep directory trees | Depth limit, reported as a diagnostic rather than silently truncating |
| Malformed UTF-8 | Rejected with `STEMMA1004`; the file is preserved verbatim as an opaque block instead of being interpreted |
| Malicious YAML front matter | Only a restricted subset is parsed. Tags, anchors, aliases, merge keys and multi-document streams are refused (`STEMMA1103`). There is no reflective construction, so no gadget chain exists |
| JSON resource exhaustion | Agent JSON is size-bounded, decoded with `DisallowUnknownFields` where the shape is fixed, and rejected when it has trailing content |
| Duplicate JSON keys | Detected by streaming the token sequence; the file is rejected (`STEMMA1502`) rather than resolved by guessing |
| Terminal escape injection | All untrusted text is passed through `cli.Sanitize` before printing: ESC becomes `\e`, other control characters become `\xNN`, and format-control code points become `\uNNNN` |
| Destroying user work | Stemma never deletes, refuses to overwrite files it did not write, treats post-generation edits as conflicts, and writes transactionally with rollback |
| Data exfiltration | No network code exists anywhere in the module, and there are no external dependencies that could add one |

## What Stemma reads

Only the registered configuration paths listed in `docs/compiler-pipeline.md`.
Directory walking may pass *by* source directories to find configuration ones,
but source files are never opened. `TestClassifyIgnoresSourceCode` enforces
that paths like `main.go` or `package.json` never classify.

## What Stemma writes

- Provider files produced by the selected exporter.
- The canonical project under `.stemma/`: `project.json`, the entity Markdown
  files, `provenance.json`, `profiles/*.json` and `manifest.json`. Entity files
  are only ever removed when an import explicitly replaces the project, and only
  inside Stemma's own entity directories.
- A plan file, when `--output-plan` is given.
- `.stemma/recovery/` (mode 0700, files 0600) only when a rollback could not
  complete.

Nothing else, ever, and nothing at all under `scan`, `plan`, `check` or
`explain`.

## Transactional writes

1. Refuse to start if any blocking diagnostic is present.
2. Re-hash every destination the plan inspected; abort on any difference.
3. Snapshot the current content of each destination in memory.
4. For each file in sorted path order: create a temporary sibling file, write,
   `fsync`, `chmod` (preserving existing permissions), then `rename` into place.
5. On any failure: restore every replaced file, remove every created file,
   remove stranded temporary files.
6. If restoration itself fails: write the original bytes and a `RECOVERY.txt`
   explaining what to do into `.stemma/recovery/`, and return a `RollbackError`
   with exit code 5.

The manifest is written inside the same transaction as the generated files, so
Stemma's record of the world cannot drift from the world.

## Dependencies

The module has no external dependencies. The front matter parser, glob engine
and Markdown splitter are part of this repository specifically so that their
behaviour and limits are auditable, and so that a supply-chain compromise in a
YAML or glob library cannot affect Stemma.

## Reporting a problem

If you find a way to make Stemma write outside the workspace, follow a symlink,
execute anything from a repository file, or panic on a crafted input, that is a
security bug. Include the input that triggers it: fuzz corpus entries under
`internal/*/testdata/fuzz/` are the preferred format.
