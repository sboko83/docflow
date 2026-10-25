#!/bin/sh
# Standalone docflow installer. Downloads a release archive, verifies its
# sha256 checksum, unpacks it, and runs `docflow install` from the unpacked
# binary. macOS, Linux and Windows (Git Bash), amd64 and arm64; no Go needed.
#
#   curl -fsSL https://raw.githubusercontent.com/sboko83/docflow/main/install.sh | sh
#
# Environment overrides:
#   DOCFLOW_VERSION=X.Y.Z      install this version instead of the latest
#   DOCFLOW_RELEASE_URL=BASE   release base URL (default: GitHub Releases)
set -eu

BASE=${DOCFLOW_RELEASE_URL:-https://github.com/sboko83/docflow/releases}
BASE=${BASE%/}

fail() {
    printf 'install.sh: %s\n' "$1" >&2
    exit 1
}

OS=$(uname -s) || fail 'cannot detect the operating system'
case $OS in
    Darwin) OS=darwin ;;
    Linux) OS=linux ;;
    MINGW* | MSYS* | CYGWIN*) OS=windows ;;
    *) fail "unsupported operating system: $OS (supported: macOS, Linux, Windows with Git Bash)" ;;
esac
BINARY=docflow
[ "$OS" != windows ] || BINARY=docflow.exe

ARCH=$(uname -m) || fail 'cannot detect the CPU architecture'
case $ARCH in
    x86_64 | amd64) ARCH=amd64 ;;
    arm64 | aarch64) ARCH=arm64 ;;
    *) fail "unsupported CPU architecture: $ARCH (supported: amd64, arm64)" ;;
esac

if command -v curl >/dev/null 2>&1; then
    DOWNLOADER=curl
elif command -v wget >/dev/null 2>&1; then
    DOWNLOADER=wget
else
    fail 'curl or wget is required'
fi

fetch() {
    if [ "$DOWNLOADER" = curl ]; then
        curl -fsSL "$1" -o "$2"
    else
        wget -q -O "$2" "$1"
    fi
}

TMP=$(mktemp -d 2>/dev/null || mktemp -d -t docflow) \
    || fail 'cannot create a temporary directory'
trap 'rm -rf "$TMP"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

VERSION=${DOCFLOW_VERSION-}
if [ -z "$VERSION" ]; then
    fetch "$BASE/latest/download/VERSION" "$TMP/VERSION" \
        || fail "cannot read the latest version from $BASE"
    VERSION=$(tr -d ' \t\r\n' < "$TMP/VERSION")
fi
VERSION=${VERSION#v}
printf '%s\n' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' \
    || fail "invalid release version: '$VERSION' (want X.Y.Z)"

NAME=docflow_${OS}_${ARCH}.tar.gz
URL=$BASE/download/v$VERSION
fetch "$URL/$NAME" "$TMP/$NAME" || fail "cannot download $URL/$NAME"
fetch "$URL/checksums.txt" "$TMP/checksums.txt" \
    || fail "cannot download $URL/checksums.txt"

if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL=$(sha256sum "$TMP/$NAME" | cut -d' ' -f1)
elif command -v shasum >/dev/null 2>&1; then
    ACTUAL=$(shasum -a 256 "$TMP/$NAME" | cut -d' ' -f1)
else
    fail 'sha256sum or shasum is required to verify the download'
fi
NAME_RE=$(printf '%s\n' "$NAME" | sed 's/[.]/\\./g')
WANT=$(sed -n 's/^\([0-9a-fA-F]*\) [ *]'"$NAME_RE"'$/\1/p' \
    "$TMP/checksums.txt" | tr 'A-F' 'a-f')
[ -n "$WANT" ] || fail "no checksum for $NAME in checksums.txt"
[ "$WANT" = "$ACTUAL" ] \
    || fail "sha256 mismatch for $NAME (expected $WANT, got $ACTUAL)"

mkdir "$TMP/release" || fail 'cannot create the unpack directory'
tar -xzf "$TMP/$NAME" -C "$TMP/release" || fail "cannot unpack $NAME"
[ -x "$TMP/release/$BINARY" ] || fail "archive has no $BINARY binary: $NAME"

"$TMP/release/$BINARY" install
