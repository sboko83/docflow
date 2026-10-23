#!/bin/bash
# Check that a release tag is v<version>, where <version> is var version in
# cmd/docflow/main.go. The release workflow runs it before building assets.
# Usage: bash tools/check-release-tag.sh vX.Y.Z
set -euo pipefail

fail() {
    printf 'check-release-tag: %s\n' "$1" >&2
    exit 1
}

[ "$#" -eq 1 ] || fail 'usage: check-release-tag.sh vX.Y.Z'
TAG=$1

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd -P) || fail 'cannot locate tools'
MAIN=$SCRIPT_DIR/../cmd/docflow/main.go
[ -f "$MAIN" ] || fail "missing $MAIN"

SRC=$(sed -n 's/^var version = "\(.*\)"$/\1/p' "$MAIN")
[ -n "$SRC" ] || fail 'var version not found in cmd/docflow/main.go'
[ "$TAG" = "v$SRC" ] || fail "tag $TAG does not match version $SRC"
