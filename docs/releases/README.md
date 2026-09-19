# Release routine

The workflow in `.github/workflows/ci.yml` has two distinct release jobs:

- `release-version` publishes a versioned release for a pushed `v*` tag after
  tests, fuzz smoke tests and cross-compilation pass.
- `release-latest`, the rolling build from the tip of `master`, is currently
  disabled with `if: false`. A normal push to `master` does not publish or
  replace binaries.

Enabling the rolling release is a separate product and operations decision. Do
not remove its guard as part of a routine versioned release.

## Maintainer checklist

1. Inspect the current conditions of both release jobs in
   `.github/workflows/ci.yml`. Confirm that README installation text describes
   what the workflow actually publishes.
2. Add or update `docs/releases/vX.Y.Z.md` with the reviewed changes, known
   limitations and any upgrade notes.
3. Run `make verify` from a clean worktree and review the complete diff,
   including generated or release metadata.
4. Create the intended version tag only after the version and release notes
   agree. The `release-version` job accepts stable `vX.Y.Z` tags and optional
   prerelease or build suffixes, and builds all six supported platform binaries.
5. After the workflow succeeds, verify that the GitHub release contains all
   expected binaries plus `checksums.txt`. For a stable release, also verify
   that the README's `releases/latest` links resolve to it. For a prerelease,
   verify the direct tag URL and confirm that `releases/latest` still resolves
   to the previous stable release.
6. Re-check the rolling-release statement in this file and the README. If the
   `release-latest` condition changes deliberately, update both documents in
   the same change so release claims cannot drift from automation again.
