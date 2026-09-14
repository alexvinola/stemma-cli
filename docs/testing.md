# Testing and fixtures

## Choosing a regression

- **Validation and rejection:** use a table test with a small inline input.
  Assert the diagnostic code, severity, blocking flag, affected path and field;
  check preserved content and absence of writes where relevant. Do not export
  an import that the CLI rejects just to snapshot every provider's empty output.
- **Invariants:** use semantic/property tests for determinism, independent
  destinations, scope preservation, idempotence, round trips and user edits.
  Expected values must be hand-authored, not derived by rerunning the production
  transformation being tested.
- **Serialized output:** keep byte-for-byte goldens where exact Markdown, JSON,
  front matter, aggregation, scope rendering or projection reports matter.
  Start with the smallest input and only the destinations affected by the case.
  Add canonical storage snapshots only for a distinct serialization contract.

Before removing a fixture, identify the test assertions that retain its useful
coverage, or explain its concrete redundancy. Reduced disk usage alone is not
the goal: fewer repeated files and expectations make review more useful.

## Golden registry and regeneration

`goldenCases()` in `internal/compiler/golden_test.go` declares every case,
its targets and whether to compare the imported canonical storage tree.
Normal tests and regeneration use the **same selection**. An unregistered case,
empty/duplicate/unsupported target list, or snapshot/profile for an undeclared
target fails inventory validation. Removing an expected mappings file does not
remove that target from coverage: the test fails. Missing output files and extra
output files also fail.

Each selected target requires its mappings report, including an empty report
when zero mappings are the intended behavior. An absent diagnostic snapshot
means **exactly zero diagnostics**; any new diagnostic fails until deliberately
reviewed. Nonempty diagnostics remain byte-for-byte snapshots. A warning that
disappears also fails against its existing snapshot.

1. Add the input and declare the case/necessary targets in `goldenCases()`.
2. Run `make golden`. It rewrites only declared snapshots and removes empty
   diagnostic snapshots. When reducing the registry, explicitly remove obsolete
   snapshots as part of the reviewed change; regeneration rejects leftovers.
3. Read `git diff -- testdata` and inspect any new untracked fixtures. Never
   accept output just because it came from the compiler.
4. Run `make verify`: formatting, vet, tests, race tests and six platform builds.
   Ordinary tests never regenerate fixtures.

The core imported projects retain all four output targets and their canonical
storage layouts. Focused scope and collision cases retain only useful byte
contracts; the broader semantic and round-trip tests still run separately.

| Case | Golden targets | Canonical storage snapshot |
| --- | --- | --- |
| `copilot/basic`, `claude/basic`, `codex/nested`, `kiro/steering` | All four | Yes |
| `canonical/brace-globs`, `canonical/comma-in-pattern`, `canonical/exclude-and-disabled` | All four | No (canonical input) |
| `claude/brace-globs` | Copilot | No |
| `copilot/brace-globs` | Claude, Codex | No |
| `claude/character-class-globs` | Copilot | No |
| `copilot/character-class-globs` | Claude | No |
| `claude/duplicate-titles` | Copilot, Codex | No |

Copilot's format identifier is `github-copilot` in the registry and filenames.

## Fuzz inputs

Go keeps dynamically discovered coverage inputs in its build-cache fuzz area,
outside the repository. Do not copy that cache into Git. Files under
`internal/<package>/testdata/fuzz/<FuzzTest>/` are explicit seed/regression
inputs: normal `go test` replays them, and fuzzing starts from them. Preserve
small seeds that reproduce a defect or exercise a distinct invariant; review and
minimize new failures before versioning them. Do not blanket-ignore or delete
these directories. Run `make fuzz` for a bounded fuzzing pass.

## Issue #42 reduction audit

Baseline: commit `796ab4b` (issues #2 and #8 integrated). This change removes
183 of 427 root `testdata/` files (42.9%) and 80,243 of 196,495 bytes (40.8%).
It reduces destination combinations from 76 to 35. Retained expected files are
unchanged byte for byte; no fixtures are compressed, hidden, moved elsewhere,
or replaced with compiler-generated expectations inside tests. The small
rejected inputs are represented inline by semantic assertions instead.

| Category | Files before | Files after | Bytes before | Bytes after |
| --- | ---: | ---: | ---: | ---: |
| Provider inputs and canonical input JSON | 47 | 32 | 11,309 | 9,681 |
| Imported canonical storage snapshots | 66 | 30 | 34,066 | 17,047 |
| Generated provider files | 132 | 104 | 20,876 | 17,287 |
| Projection mappings | 76 | 35 | 68,466 | 45,449 |
| Import/export diagnostics | 92 | 29 | 60,597 | 25,607 |
| Other root fixtures (malformed/security inputs and one profile) | 14 | 14 | 1,181 | 1,181 |
| **Root `testdata/` total** | **427** | **244** | **196,495** | **116,252** |
| Fuzz corpus (separate, unchanged) | 5 | 5 | 249 | 249 |

Validation: `make verify` passed with the replacement tests included, and the
QA lab passed all 22 cases, including `duplicate-titles` and `wrong-field-types`.
A second `make golden` produced identical paths and bytes; ordinary tests also
left fixtures unchanged. Negative controls separately removed a mappings file,
a generated output file and a nonempty diagnostic snapshot: each made the golden
test fail, and all files were restored afterward.

### Removed groups and retained coverage

| Removed or reduced group | Why it is redundant / assertions retained |
| --- | --- |
| All four `wrong-field-types` golden directories | The CLI blocks these imports before writing/exporting. `TestImportRecognizedFieldsRejectWrongTypes` covers the field/type matrix; `TestWrongTypeImportBlocksWithoutWriting` covers fresh/existing projects and text/JSON presentation. `TestCompilerImportRejectsWrongTypesForEveryRole`, `TestCompilerImportReportsEveryInvalidFieldOncePerFile` and `TestCompilerRejectedOpaqueProjectionAcrossProviders` also cover discovery, multiple invalid fields, exact opaque preservation and projection outcomes. |
| `claude/expansion-limit`, `copilot/expansion-limit`, `copilot/corrupted-applyto` | Rejected scopes need blocking diagnostic and no-write assertions, not snapshots of a partial project's exports. `TestCompilerRejectedGlobsPreserveValidSibling` keeps valid sibling content and rejects the bad scope; `TestOversizedBraceImportDoesNotWrite`, `TestExpansionLimitNeverSamplesValidation` and `TestSplitApplyToHonoursBraceGroups` retain CLI, expansion-bound and split-parser coverage. |
| Brace-scope storage trees and unselected destinations | Opposite-provider goldens pin expanded scope bytes; the Copilot-to-Codex golden pins broader/ambiguous scope outcomes. `TestBracePatternsSurviveCopilotRoundTrip` and `TestHandwrittenBraceScopeSurvivesApplyAndReimport` check exact scope membership through real import/apply/reimport. Core and canonical goldens retain the other provider renderers. |
| Character-class storage trees and unselected destinations | Opposite-provider goldens exercise actual discovery/import/rendering; `TestBraceCharacterClasses` and `TestSplitApplyToHonoursBraceGroups` assert parser semantics. Core/canonical matrices retain shared target renderers. |
| Duplicate-title storage tree and Claude/Kiro outputs | Retained Copilot/Codex goldens pin imported IDs and distinct scopes. `TestDuplicateTitlesKeepIndependentScope`, `TestNoUnintendedSharedDestinationsProperty` and `TestDestinationCollisionReturnsInternalDiagnosticWithoutWriting` keep issue #2's destination isolation, collision diagnostics and no-write coverage. |
| Repeated canonical storage layouts | The four core project snapshots retain every representative entity layout. `TestProjectSurvivesASaveLoadRoundTrip`, `TestEncodingIsStable` and `TestEntityFilesAreReadable` check storage independently of the golden compiler harness. |
| Empty diagnostic snapshots | Absence now asserts zero diagnostics. Nonempty diagnostics and all selected mappings still require exact independent expectations. Empty mappings for removed rejected imports are replaced by explicit outcome/count assertions. |

All six existing round-trip input cases, malformed/security fixtures, fuzz
corpora and apply/user-edit tests remain. The shared QA lab in `stemma-cli-test`
also has `duplicate-titles` and `wrong-field-types` cases for reviewing issues
#2/#8 through the executable:

```bash
go build -o /tmp/stemma-issue42 ./cmd/stemma
python3 ../stemma-cli-test/scripts/lab.py verify --stemma /tmp/stemma-issue42
```

### Reproducing the inventory

Run this from the repository root. It includes hidden provider paths and
untracked fixtures, excludes deleted working-tree files, and keeps the fuzz
corpus separate. `rg --files testdata` alone omits hidden provider directories.

```python
from collections import defaultdict
from pathlib import Path
import subprocess

totals = defaultdict(lambda: [0, 0])
paths = subprocess.check_output(
    ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"]
).decode().split("\0")
for name in sorted(set(paths)):
    path = Path(name)
    if not name or not path.is_file():
        continue
    if "/testdata/fuzz/" in name:
        category = "fuzz corpus"
    elif not name.startswith("testdata/"):
        continue
    elif "/input/" in name or name.endswith("/canonical.json"):
        category = "inputs"
    elif "/expected-project/" in name:
        category = "canonical snapshots"
    elif name.endswith("-mappings.json"):
        category = "mappings"
    elif name.endswith("-diagnostics.json"):
        category = "diagnostics"
    elif "/expected-" in name:
        category = "generated files"
    else:
        category = "other fixture files"
    totals[category][0] += 1
    totals[category][1] += path.stat().st_size
for category, (files, size) in sorted(totals.items()):
    print(f"{category}: {files} files, {size} bytes")
```
