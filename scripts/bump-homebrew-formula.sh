#!/usr/bin/env bash
#
# Updates a Homebrew formula to a published release of stemma.
#
#   scripts/bump-homebrew-formula.sh <version-without-v> <path-to-stemma.rb>
#
# The checksums are read from the release's own checksums.txt and written into
# the formula as literals. They are deliberately not fetched at install time:
# a hash that travels with the artefact it verifies proves nothing, because
# whoever could tamper with one could tamper with the other. Committing the
# hash into the tap keeps the two on separate trust paths.
#
# Safe to re-run: it is a no-op when the formula already pins that version.

set -euo pipefail

if [ $# -ne 2 ]; then
    echo "usage: $0 <version-without-v> <path-to-formula.rb>" >&2
    exit 2
fi

VERSION="$1"
FORMULA="$2"
REPO="${STEMMA_REPO:-alexvinola/stemma-cli}"

if [ ! -f "$FORMULA" ]; then
    echo "error: no such formula: $FORMULA" >&2
    exit 1
fi

CHECKSUMS_URL="https://github.com/${REPO}/releases/download/v${VERSION}/checksums.txt"

echo "Fetching ${CHECKSUMS_URL}"
if ! SUMS=$(curl -sfL "$CHECKSUMS_URL"); then
    echo "error: no checksums.txt published for v${VERSION}" >&2
    exit 1
fi

# Pull one asset's hash out of the checksums file, failing loudly if the asset
# is missing rather than writing an empty hash into the formula.
#
# The value is required to be 64 hex characters, not merely 64 characters long:
# it is used as a regex replacement further down, where a backslash sequence
# would be interpreted rather than inserted literally.
sum_for() {
    local name="$1" hash
    hash=$(echo "$SUMS" | awk -v n="$name" '$2 == n { print $1 }')
    if ! [[ "$hash" =~ ^[0-9a-f]{64}$ ]]; then
        echo "error: no valid sha256 for ${name} in checksums.txt" >&2
        exit 1
    fi
    echo "$hash"
}

DARWIN_ARM=$(sum_for stemma-darwin-arm64)
DARWIN_AMD=$(sum_for stemma-darwin-amd64)
LINUX_ARM=$(sum_for stemma-linux-arm64)
LINUX_AMD=$(sum_for stemma-linux-amd64)

python3 - "$FORMULA" "$VERSION" \
    "$DARWIN_ARM" "$DARWIN_AMD" "$LINUX_ARM" "$LINUX_AMD" <<'PY'
import re
import sys

path, version, darwin_arm, darwin_amd, linux_arm, linux_amd = sys.argv[1:7]
original = open(path).read()

updated, n = re.subn(r'version "[^"]+"', f'version "{version}"', original, count=1)
if n != 1:
    sys.exit("error: no version field found in the formula")

for asset, digest in (
    ("darwin-arm64", darwin_arm),
    ("darwin-amd64", darwin_amd),
    ("linux-arm64", linux_arm),
    ("linux-amd64", linux_amd),
):
    # Anchor on the asset name in the url so each sha256 is matched to the
    # binary immediately above it, never to a neighbouring block.
    pattern = r'(stemma-' + asset + r'"\s*\n\s*sha256 ")[0-9a-f]{64}'
    updated, n = re.subn(pattern, r'\g<1>' + digest, updated)
    if n != 1:
        sys.exit(f"error: expected exactly one sha256 for {asset}, found {n}")

if updated == original:
    print(f"Formula already pins {version}; nothing to do.")
else:
    open(path, "w").write(updated)
    print(f"Formula updated to {version}.")
PY
