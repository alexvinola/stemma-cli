# Diagnostics and exit codes

Diagnostic codes are part of Stemma's public contract. Human wording may
improve; codes do not change after release.

## Structure

Every diagnostic carries a code, a severity (`info`, `warning`, `error`), a
one-line summary, and optionally a detailed explanation, a source path, a line
and column, a canonical entity id, a target format, a `field` and a suggested
resolution. It also carries `blocking` and a stable `fingerprint`.

`field` names the specific field a diagnostic is about when one entity can
produce several diagnostics with the same code for one target — for example
`extensions.kiro.allowedTools` on `STEMMA3802`.

The fingerprint is derived from code, severity, path, entity id, target and,
when present, field — deliberately **not** from prose — so improving a message
does not invalidate an acceptance recorded in a target profile. A diagnostic
without a field has exactly the fingerprint it had before `field` existed, and
two fields of one entity never share a fingerprint, so accepting one loss
never accepts another.

## Acceptance

A reviewed diagnostic can be listed in the target profile:

```json
{
  "schemaVersion": 1,
  "target": "codex",
  "overrides": {},
  "acceptedDiagnostics": ["dg_184cf4cbcf883cfa"]
}
```

Accepted diagnostics are downgraded to `info` and stop blocking apply. They stay
visible: acceptance is not suppression. Get fingerprints from
`stemma plan --target <target> --json`.

## Ordering

Diagnostics are sorted by severity, then code, then path, then line, then
column, then entity, then target, then field, then summary. Duplicates are removed. Both
human and JSON output use this order.

## Codes

### 1xxx — discovery and parsing

| Code | Severity | Meaning |
| --- | --- | --- |
| `STEMMA1001_UNRECOGNIZED_FORMAT` | warning | A registered path has no importer for its role |
| `STEMMA1002_LIMIT_REACHED` | warning/error | A scan or document limit stopped the work early; for a scan the detail names the limit and what was not inspected |
| `STEMMA1003_FILE_UNREADABLE` | error | A configuration file could not be read |
| `STEMMA1004_INVALID_ENCODING` | error | The file is not valid UTF-8; it is preserved, not interpreted |
| `STEMMA1101_INVALID_FRONT_MATTER` | warning/error | Front matter could not be parsed in the supported subset, or a recognized provider/canonical field has the wrong type (error; names the key and found type) |
| `STEMMA1102_FRONT_MATTER_TOO_LARGE` | error | Front matter exceeded a size, line or key limit |
| `STEMMA1103_UNSAFE_YAML_CONSTRUCT` | error | A tag, anchor, alias or merge key was refused |
| `STEMMA1201_UNKNOWN_SECTION_PRESERVED` | info/warning | A section was kept without being modelled |
| `STEMMA1202_UNKNOWN_KEYS_PRESERVED` | info | Unrecognised front matter kept as provider extensions |
| `STEMMA1203_OPAQUE_BLOCK_PRESERVED` | info | Content preserved verbatim without interpretation |
| `STEMMA1301_MULTIPLE_SOURCES` | error | Several providers detected; Stemma will not merge silently |
| `STEMMA1302_NO_SOURCES_DETECTED` | info | Nothing supported was found |
| `STEMMA1303_DISCOVERY_INCOMPLETE` | error/warning | Import refused: a scan limit or an unreadable directory truncated discovery, so configuration may be missing. A warning when `--allow-incomplete-scan` accepts the subset |
| `STEMMA1304_SHARED_FILE_NOT_IMPORTED` | warning | The selected provider also reads this file, but another adapter owns it and the selected adapter does not import it (for example `AGENTS.md` under `--from kiro`); the file is left out of the import and untouched |
| `STEMMA1305_DIRECTORY_UNREADABLE` | warning | A directory's entries could not be read (typically permissions), so nothing below it was inspected; the scan is incomplete |
| `STEMMA1401_MIXED_LINE_ENDINGS` | info | The file mixes LF and CRLF; generated output uses LF |
| `STEMMA1501_INVALID_AGENT_JSON` | error | An agent definition is not valid JSON, or has wrong field types |
| `STEMMA1502_DUPLICATE_JSON_KEY` | error | A JSON object repeats a key; Stemma will not guess which wins |

A scan is **incomplete** when a walk limit truncated it — `max-depth` (a
directory deeper than 32 levels), `max-files` (more than 20 000 registered
configuration files) or `max-entries` (more than 1 000 000 directory entries
inspected) — or when a directory it entered could not be read. Only registered
configuration paths count against `max-files`; source code only counts against
`max-entries`. `stemma scan` reports an incomplete scan with `STEMMA1002`
warnings for limits and `STEMMA1305` warnings for unreadable directories (the
first 100 by path, then a count), `"complete": false` in JSON and a line in its
human output, and still exits 0. `stemma import` refuses it with a
blocking `STEMMA1303` error and exit code 1 before reading any file, because
auto-detection and the imported project could both be based on a subset.
`--allow-incomplete-scan` imports what was discovered; `STEMMA1303` then remains
visible as a warning. `"complete": true` means that every directory the walk
was meant to enter was read in full. Directories in the fixed skip list
(`node_modules`, `vendor`, …) and symbolic links are excluded by design and
never make a scan incomplete.

### 2xxx — canonical validation

| Code | Severity | Meaning |
| --- | --- | --- |
| `STEMMA2001_DUPLICATE_ENTITY_ID` | error/info | Two entities share an id (info when reported by the deduplication pass) |
| `STEMMA2002_INVALID_ENTITY_ID` | error | Malformed id or a type/slug mismatch |
| `STEMMA2003_MISSING_REQUIRED_FIELD` | error | A required field is empty, has an unknown enum value, or a canonical entity is missing its front matter block |
| `STEMMA2004_UNSUPPORTED_SCHEMA_VERSION` | error | The project was written by another schema version |
| `STEMMA2005_INVALID_ACTIVATION` | error | The activation union invariants were violated |
| `STEMMA2101_INVALID_GLOB` | error/warning | A pattern is invalid or escapes the repository |
| `STEMMA2102_GLOB_EXPANSION_LIMIT` | error | Import rejected: brace expansion exceeds 1000 alternatives or 32 nested groups; split the pattern into smaller groups |
| `STEMMA2201_DANGLING_PROVENANCE` | warning/error | Provenance is incomplete or inconsistent |
| `STEMMA2301_INVALID_PROFILE` | error/warning | A profile is malformed or unsafe |
| `STEMMA2302_PROFILE_OVERRIDES_UNKNOWN_ENTITY` | warning | A profile overrides an entity that does not exist |
| `STEMMA2401_MANIFEST_INCONSISTENT` | info/warning/error | The manifest no longer matches the repository |

### 3xxx — projection

| Code | Severity | Meaning |
| --- | --- | --- |
| `STEMMA3001_TARGET_UNAVAILABLE` | error | The target is declared but not implemented (exit code 3) |
| `STEMMA3002_TARGET_NOT_ENABLED` | warning | The target is not listed in the canonical project |
| `STEMMA3101_EXCLUDE_NOT_REPRESENTABLE` | warning | The provider has no negative pattern syntax |
| `STEMMA3102_PATTERN_NOT_REPRESENTABLE` | warning | A pattern contains a comma, which a comma-separated pattern list cannot represent |
| `STEMMA3201_DIRECTORY_SCOPE_AMBIGUOUS` | warning | Patterns do not resolve to one directory; Stemma will not invent one |
| `STEMMA3202_DIRECTORY_SCOPE_BROADENED` | warning | Directory scoping matches more files than the canonical patterns |
| `STEMMA3301_AGENT_TOOLS_REQUIRE_REVIEW` | warning/error | Tool names crossed providers, or are unsafe |
| `STEMMA3302_AGENT_NOT_NATIVELY_SUPPORTED` | warning | The target has no specialist-agent format |
| `STEMMA3402_ON_DEMAND_ADAPTED` | info | On-demand content is delivered through another mechanism |
| `STEMMA3501_OPAQUE_BLOCK_NOT_REEMITTED` | warning | Preserved content could not be written back |
| `STEMMA3601_TARGET_CONTENT_OVERRIDDEN` | warning | A profile replaced the canonical wording for this target |
| `STEMMA3701_FILE_REGENERATED` | info | A file was regenerated rather than minimally patched |
| `STEMMA3801_EXTENSION_NOT_PROJECTED` | warning | A context, behaviour or unclassified provider extension field was not written for the target |
| `STEMMA3802_SECURITY_EXTENSION_NOT_PROJECTED` | error | A security provider extension field (permissions, tool allowlists, hooks, MCP servers) was not written for the target; blocks apply until its fingerprint is accepted |

`STEMMA3801` and `STEMMA3802` are reported once per entity, target and
extension field, and make the mapping `lossy`. Provider extensions are
classified in `internal/capabilities` (see
[provider extension classification](provider-compatibility.md#provider-extension-classification)):
losing a `presentation` field is silent, losing a `context` or `behaviour`
field — or any field Stemma has not classified — is `STEMMA3801`, and losing a
`security` field is `STEMMA3802`. A dropped permission policy or tool allowlist
must not disappear unnoticed, so `STEMMA3802` is an error: configure an
equivalent policy in the target by hand where one exists, then add the
diagnostic's fingerprint to `acceptedDiagnostics` in that target's profile.
Setting `acceptLossy` on the entity only annotates the mapping; it does not
accept the security loss. Stemma's own `stemma.*` bookkeeping keys are never
reported.

### 4xxx — filesystem and transactions

| Code | Severity | Meaning |
| --- | --- | --- |
| `STEMMA4001_PATH_ESCAPE` | error | A path would leave the workspace |
| `STEMMA4002_SYMLINK_REJECTED` | error | A destination is a symbolic link; Stemma never writes through one |
| `STEMMA4101_STALE_PLAN` | error | The repository changed after the plan was built (exit code 4) |
| `STEMMA4201_WRITE_ROLLED_BACK` | error | A write failed; changes were rolled back (exit code 5) |
| `STEMMA4202_RECOVERY_DATA_WRITTEN` | error | Rollback was incomplete; see `.stemma/recovery/` |
| `STEMMA4301_UNTRACKED_DESTINATION` | error | The destination is unowned or changed since ownership was recorded |
| `STEMMA4302_IMPORT_ROUND_TRIP_UNVERIFIED` | warning | Import could not reproduce a source byte-identically at the same path; no ownership was recorded |
| `STEMMA4303_IMPORT_OWNERSHIP_REVOKED` | warning | Import replaced the canonical project; a previous destination lost ownership and was left untouched |
| `STEMMA4401_DELETE_PROPOSED` | info | A previously generated file is no longer produced |
| `STEMMA4501_OUTPUT_STALE` | error | `check` found generated output that is out of date |

`STEMMA4302` does not prevent saving the canonical import, but warns at import
time that overwriting this source cannot be authorized automatically. Compare
the source and canonical content, preserve anything missing and review the
same-provider plan. A separate output path allows review without overwriting
the source. `--adopt-untracked` does not bypass this conflict; it is intended for
foreign files. See [ownership at import](round-trip.md#ownership-at-import).

### 5xxx — budgets

| Code | Severity | Meaning |
| --- | --- | --- |
| `STEMMA5001_TOKEN_BUDGET_EXCEEDED` | warning | An approximate estimate exceeds a configured budget |
| `STEMMA5002_ALWAYS_ON_CONTEXT_LARGE` | info | Always-on context is large and no budget is set |

### 6xxx — internal

| Code | Severity | Meaning |
| --- | --- | --- |
| `STEMMA6001_INTERNAL_INVARIANT` | error | A compiler invariant failed; this is a bug (exit code 6) |

Numbers are never reused: `STEMMA3401` was retired before release because every
implemented provider can express several include patterns, so no adapter needs
to report adapting them.

## Exit codes

| Code | Name | When |
| --- | --- | --- |
| 0 | ok | Success |
| 1 | diagnostics | Validation or compilation diagnostics prevented success |
| 2 | usage | Invalid CLI usage |
| 3 | unsupported-target | Unknown or unimplemented target |
| 4 | stale-plan | Stale plan or filesystem conflict |
| 5 | write-failed | A safe write failed or was rolled back |
| 6 | internal | An internal compiler invariant failed |

Detailed causes always remain available through diagnostics; the exit code set
is deliberately small.

`STEMMA6001_INTERNAL_INVARIANT` also blocks colliding generated destinations,
including conflicting imported hints and profile pins. All affected mappings
are blocked; the CLI returns 6 and writes no files. Aggregates must be assembled
by the adapter and emitted exactly once.
