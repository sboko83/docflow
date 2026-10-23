#!/bin/bash
# Print the body of the `## [X.Y.Z]` section of CHANGELOG.md. The release
# workflow uses it as the GitHub Release notes; a missing or empty section
# fails the release before any asset is built.
# Usage: bash tools/release-notes.sh X.Y.Z
set -euo pipefail

fail() {
    printf 'release-notes: %s\n' "$1" >&2
    exit 1
}

[ "$#" -eq 1 ] || fail 'usage: release-notes.sh X.Y.Z'
VERSION=$1
printf '%s\n' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' \
    || fail "VERSION must be bare SemVer (X.Y.Z), got: $VERSION"

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd -P) || fail 'cannot locate tools'
LOG=$SCRIPT_DIR/../CHANGELOG.md
[ -f "$LOG" ] || fail "missing $LOG"

NOTES=$(awk -v head="## [$VERSION]" '
    index($0, head) == 1 { on = 1; next }
    on && (/^## / || /^\[[^]]+\]: /) { exit }
    on { print }
' "$LOG" | sed -e '/./,$!d')
[ -n "$NOTES" ] || fail "no CHANGELOG.md section for $VERSION"
printf '%s\n' "$NOTES"
