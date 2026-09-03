# Diagnostics and exit codes

Diagnostic codes are part of Stemma's public contract. Human wording may
improve; codes do not change after release.

## Structure

Every diagnostic carries a code, a severity (`info`, `warning`, `error`), a
one-line summary, and optionally a detailed explanation, a source path, a line
and column, a canonical entity id, a target format and a suggested resolution.
It also carries `blocking` and a stable `fingerprint`.

The fingerprint is derived from code, severity, path, entity id and target —
deliberately **not** from prose — so improving a message does not invalidate an
acceptance recorded in a target profile.

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
column, then entity, then target, then summary. Duplicates are removed. Both
human and JSON output use this order.

## Codes

### 1xxx — discovery and parsing

| Code | Severity | Meaning |
| --- | --- | --- |
| `STEMMA1001_UNRECOGNIZED_FORMAT` | warning | A registered path has no importer for its role |
| `STEMMA1002_LIMIT_REACHED` | warning/error | A scan or document limit stopped the work early |
| `STEMMA1003_FILE_UNREADABLE` | error | A configuration file could not be read |
| `STEMMA1004_INVALID_ENCODING` | error | The file is not valid UTF-8; it is preserved, not interpreted |
| `STEMMA1101_INVALID_FRONT_MATTER` | warning/error | Front matter could not be parsed in the supported subset |
| `STEMMA1102_FRONT_MATTER_TOO_LARGE` | error | Front matter exceeded a size, line or key limit |
| `STEMMA1103_UNSAFE_YAML_CONSTRUCT` | error | A tag, anchor, alias or merge key was refused |
| `STEMMA1201_UNKNOWN_SECTION_PRESERVED` | info/warning | A section was kept without being modelled |
| `STEMMA1202_UNKNOWN_KEYS_PRESERVED` | info | Unrecognised front matter kept as provider extensions |
| `STEMMA1203_OPAQUE_BLOCK_PRESERVED` | info | Content preserved verbatim without interpretation |
| `STEMMA1301_MULTIPLE_SOURCES` | error | Several providers detected; Stemma will not merge silently |
| `STEMMA1302_NO_SOURCES_DETECTED` | info | Nothing supported was found |
| `STEMMA1401_MIXED_LINE_ENDINGS` | info | The file mixes LF and CRLF; generated output uses LF |
| `STEMMA1501_INVALID_AGENT_JSON` | error | An agent definition is not valid JSON, or has wrong field types |
| `STEMMA1502_DUPLICATE_JSON_KEY` | error | A JSON object repeats a key; Stemma will not guess which wins |

### 2xxx — canonical validation

| Code | Severity | Meaning |
| --- | --- | --- |
| `STEMMA2001_DUPLICATE_ENTITY_ID` | error/info | Two entities share an id (info when reported by the deduplication pass) |
| `STEMMA2002_INVALID_ENTITY_ID` | error | Malformed id or a type/slug mismatch |
| `STEMMA2003_MISSING_REQUIRED_FIELD` | error | A required field is empty or has an unknown enum value |
| `STEMMA2004_UNSUPPORTED_SCHEMA_VERSION` | error | The project was written by another schema version |
| `STEMMA2005_INVALID_ACTIVATION` | error | The activation union invariants were violated |
| `STEMMA2101_INVALID_GLOB` | error/warning | A pattern is invalid or escapes the repository |
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
| `STEMMA3201_DIRECTORY_SCOPE_AMBIGUOUS` | warning | Patterns do not resolve to one directory; Stemma will not invent one |
| `STEMMA3202_DIRECTORY_SCOPE_BROADENED` | warning | Directory scoping matches more files than the canonical patterns |
| `STEMMA3301_AGENT_TOOLS_REQUIRE_REVIEW` | warning/error | Tool names crossed providers, or are unsafe |
| `STEMMA3302_AGENT_NOT_NATIVELY_SUPPORTED` | warning | The target has no specialist-agent format |
| `STEMMA3402_ON_DEMAND_ADAPTED` | info | On-demand content is delivered through another mechanism |
| `STEMMA3501_OPAQUE_BLOCK_NOT_REEMITTED` | warning | Preserved content could not be written back |
| `STEMMA3601_TARGET_CONTENT_OVERRIDDEN` | warning | A profile replaced the canonical wording for this target |
| `STEMMA3701_FILE_REGENERATED` | info | A file was regenerated rather than minimally patched |

### 4xxx — filesystem and transactions

| Code | Severity | Meaning |
| --- | --- | --- |
| `STEMMA4001_PATH_ESCAPE` | error | A path would leave the workspace |
| `STEMMA4002_SYMLINK_REJECTED` | error | A destination is a symbolic link; Stemma never writes through one |
| `STEMMA4101_STALE_PLAN` | error | The repository changed after the plan was built (exit code 4) |
| `STEMMA4201_WRITE_ROLLED_BACK` | error | A write failed; changes were rolled back (exit code 5) |
| `STEMMA4202_RECOVERY_DATA_WRITTEN` | error | Rollback was incomplete; see `.stemma/recovery/` |
| `STEMMA4301_UNTRACKED_DESTINATION` | error | The destination exists and Stemma did not write it |
| `STEMMA4401_DELETE_PROPOSED` | info | A previously generated file is no longer produced |
| `STEMMA4501_OUTPUT_STALE` | error | `check` found generated output that is out of date |

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
