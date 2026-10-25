#!/bin/bash
# Build release artifacts for one version into OUTDIR, mirroring the GitHub
# Releases URL layout so python3 -m http.server can serve it locally:
#   OUTDIR/download/v<VERSION>/docflow_<os>_<arch>.tar.gz
#   OUTDIR/download/v<VERSION>/checksums.txt
#   OUTDIR/download/v<VERSION>/VERSION
#   OUTDIR/latest/download/VERSION
set -e

fail() {
    printf 'error: %s\n' "$1" >&2
    exit 1
}

usage() {
    cat <<'EOF'
Usage: bash tools/release.sh VERSION OUTDIR

VERSION is bare SemVer (X.Y.Z). Builds docflow_<os>_<arch>.tar.gz for
darwin/linux/windows on amd64/arm64 from this checkout, plus checksums.txt and
VERSION, into OUTDIR with the release URL layout.
EOF
}

[ "$#" -eq 2 ] || { usage >&2; exit 1; }
VERSION=$1
OUTDIR=$2
printf '%s\n' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' \
    || fail "VERSION must be bare SemVer (X.Y.Z), got: $VERSION"

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd -P) \
    || fail 'cannot locate tools directory'
REPO_DIR=$(cd "$SCRIPT_DIR/.." && pwd -P) \
    || fail 'cannot locate checkout'
[ -f "$REPO_DIR/go.mod" ] || fail "incomplete checkout: missing $REPO_DIR/go.mod"
[ -d "$REPO_DIR/skills" ] || fail "incomplete checkout: missing $REPO_DIR/skills"
[ -f "$REPO_DIR/LICENSE" ] || fail "incomplete checkout: missing $REPO_DIR/LICENSE"

GO=$(command -v go 2>/dev/null) \
    || fail 'Go 1.24 or newer is required (go was not found)'
GO_VERSION=$("$GO" version 2>&1) \
    || fail "cannot run Go toolchain: $GO_VERSION"
if ! printf '%s\n' "$GO_VERSION" | grep -qE 'go1\.(2[4-9]|[3-9][0-9])'; then
    fail "Go 1.24 or newer is required (found $GO_VERSION)"
fi

DLDIR="$OUTDIR/download/v$VERSION"
LATESTDIR="$OUTDIR/latest/download"
mkdir -p "$DLDIR" "$LATESTDIR" || fail "cannot create $DLDIR and $LATESTDIR"

STAGE=$(mktemp -d) || fail 'cannot create staging directory'
trap 'rm -rf "$STAGE"' EXIT

for PLATFORM in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
    GOOS=${PLATFORM%/*}
    GOARCH=${PLATFORM#*/}
    BINARY=docflow
    [ "$GOOS" != windows ] || BINARY=docflow.exe
    STAGE_DIR="$STAGE/$GOOS-$GOARCH"
    mkdir -p "$STAGE_DIR"
    (cd "$REPO_DIR" \
        && CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" "$GO" build -trimpath \
            -ldflags "-X main.version=$VERSION" -o "$STAGE_DIR/$BINARY" \
            ./cmd/docflow) \
        || fail "go build failed for $GOOS/$GOARCH"
    cp -R "$REPO_DIR/skills" "$STAGE_DIR/skills"
    cp "$REPO_DIR/LICENSE" "$STAGE_DIR/LICENSE"
    # macOS Finder droppings must not land in the release archive.
    find "$STAGE_DIR" -name .DS_Store -delete
    # COPYFILE_DISABLE keeps macOS tar from adding ._ AppleDouble entries.
    COPYFILE_DISABLE=1 tar -C "$STAGE_DIR" -czf \
        "$DLDIR/docflow_${GOOS}_${GOARCH}.tar.gz" "$BINARY" LICENSE skills
done

if command -v sha256sum >/dev/null 2>&1; then
    (cd "$DLDIR" && sha256sum docflow_*.tar.gz)
else
    (cd "$DLDIR" && shasum -a 256 docflow_*.tar.gz)
fi >"$DLDIR/checksums.txt"

printf '%s\n' "$VERSION" >"$DLDIR/VERSION"
cp "$DLDIR/VERSION" "$LATESTDIR/VERSION"
printf 'release %s in %s\n' "$VERSION" "$OUTDIR"
