#!/bin/bash
# E2E test for install.ps1: build release layouts with tools/release.sh,
# serve them over a local HTTP server the way GitHub Releases does, then run
# install.ps1 in both entry forms (powershell -File and the README's
# `iex (irm URL)`), check the installed docflow, the user PATH entry, a
# corrupted-checksum abort, and docflow uninstall. Uses a temporary
# USERPROFILE with ~/.zcode and a DOCFLOW_TEST_USER_PATH override the way
# tools/test-install.sh does; run on Windows only.
set -euo pipefail

fail() {
    printf 'test-install-ps1: %s\n' "$1" >&2
    exit 1
}

case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) ;;
    *) fail 'Windows only: install.ps1 needs PowerShell' ;;
esac

command -v go >/dev/null 2>&1 || fail 'go is required'
command -v python >/dev/null 2>&1 || fail 'python is required'
command -v curl >/dev/null 2>&1 || fail 'curl is required'
command -v powershell >/dev/null 2>&1 || fail 'powershell is required'

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd -P) || fail 'cannot locate tools'
REPO_DIR=$(cd "$SCRIPT_DIR/.." && pwd -P) || fail 'cannot locate checkout'
[ -f "$REPO_DIR/install.ps1" ] || fail "missing $REPO_DIR/install.ps1"

WORK=$(mktemp -d) || fail 'cannot create a work directory'
SERVER_PID=""
cleanup() {
    if [ -n "$SERVER_PID" ]; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    rm -rf "$WORK"
}
trap cleanup EXIT

HOME_DIR=$WORK/home
DIST=$WORK/dist
mkdir "$HOME_DIR" "$DIST"
mkdir "$HOME_DIR/.zcode"

# Keep the Go build cache when HOME moves to the sandbox.
GO_CACHE=$(go env GOCACHE 2>/dev/null) || fail 'cannot read go env GOCACHE'
[ -n "$GO_CACHE" ] && export GOCACHE="$GO_CACHE"

export HOME="$HOME_DIR"
export USERPROFILE
USERPROFILE=$(cygpath -w "$HOME_DIR")
# The override must exist before the first install: an invalid override
# fails closed in Go rather than falling back to the real registry.
printf 'C:\\test-tools' > "$WORK/user-path"
export DOCFLOW_TEST_USER_PATH
DOCFLOW_TEST_USER_PATH=$(cygpath -w "$WORK/user-path")
# The skills mode question reads the console (CONIN$), which stays openable
# without a console: pin the mode so a scripted install never blocks on it.
export DOCFLOW_SKILLS=global
export DOCFLOW_NO_UPDATE_CHECK=1

docflow() {
    "$HOME_DIR/.docflow/current/docflow.exe" "$@"
}

path_entry_count() {
    python -c 'import ntpath,os,pathlib
entry = ntpath.normcase(ntpath.join(os.environ["USERPROFILE"], ".docflow", "current"))
value = pathlib.Path(os.environ["DOCFLOW_TEST_USER_PATH"]).read_text()
print(sum(ntpath.normcase(p.rstrip("\\")) == entry for p in value.split(";")))' | tr -d '\r'
}

build_release() {
    bash "$REPO_DIR/tools/release.sh" "$1" "$DIST" >/dev/null
}

install_ps1() {
    local log=$1 form=$2
    case $form in
        file)
            powershell -NoProfile -ExecutionPolicy Bypass \
                -File "$REPO_DIR/install.ps1" >"$log" 2>&1 ;;
        iex)
            powershell -NoProfile -ExecutionPolicy Bypass \
                -Command "iex (irm '$BASE_URL/install.ps1')" >"$log" 2>&1 ;;
        *) fail "unknown install form: $form" ;;
    esac
}

PORT=$(python -c 'import socket
sock = socket.socket()
sock.bind(("127.0.0.1", 0))
print(sock.getsockname()[1])
sock.close()' | tr -d '\r') || fail 'cannot pick a free port'

build_release 0.0.1
# The iex form downloads the script over HTTP like raw.githubusercontent does.
cp "$REPO_DIR/install.ps1" "$DIST/install.ps1"

python -m http.server --bind 127.0.0.1 "$PORT" --directory "$DIST" \
    >"$WORK/http.log" 2>&1 &
SERVER_PID=$!

BASE_URL=http://127.0.0.1:$PORT
export DOCFLOW_RELEASE_URL=$BASE_URL

for _ in $(seq 1 100); do
    curl -fsS -o /dev/null "$BASE_URL/latest/download/VERSION" 2>/dev/null && break
    kill -0 "$SERVER_PID" 2>/dev/null \
        || fail "http server died: $(cat "$WORK/http.log")"
    sleep 0.1
done
curl -fsS -o /dev/null "$BASE_URL/latest/download/VERSION" 2>/dev/null \
    || fail "http server did not start: $(cat "$WORK/http.log")"

echo '== install 0.0.1 (powershell -File)'
install_ps1 "$WORK/install.log" file \
    || { cat "$WORK/install.log" >&2; fail 'install.ps1 failed'; }
GOT=$(docflow --version | tr -d '\r')
[ "$GOT" = 0.0.1 ] || fail "docflow --version: got '$GOT', want '0.0.1'"
COUNT=$(path_entry_count)
[ "$COUNT" = 1 ] || fail "user PATH entries: got $COUNT, want 1"
[ -f "$HOME_DIR/.zcode/skills/bsv-docs/SKILL.md" ] \
    || fail 'no skill link in ~/.zcode/skills/bsv-docs'

echo '== corrupted checksum aborts the install'
build_release 0.0.2
case "$(uname -m)" in
    x86_64 | amd64) ARCH=amd64 ;;
    arm64 | aarch64) ARCH=arm64 ;;
    *) fail 'unsupported CPU architecture for the test' ;;
esac
CHECKSUMS=$DIST/download/v0.0.2/checksums.txt
# Corrupt the target line's hash to a valid-length-wrong hex value (insert
# two extra hex chars), so the comparison path runs rather than the
# "no checksum" parse failure.
sed -i.bak "/docflow_windows_${ARCH}\.tar\.gz\$/s/^[0-9a-f]\\{2\\}/&00/" \
    "$CHECKSUMS" && rm "$CHECKSUMS.bak"
if install_ps1 "$WORK/bad-install.log" file; then
    fail 'install.ps1 succeeded with a corrupted checksum'
fi
grep -q 'sha256 mismatch' "$WORK/bad-install.log" \
    || fail "corrupted checksum: no mismatch message in '$(cat "$WORK/bad-install.log")'"
GOT=$(docflow --version | tr -d '\r')
[ "$GOT" = 0.0.1 ] \
    || fail "version changed after a failed install: got '$GOT', want '0.0.1'"

echo '== install 0.0.2 (iex (irm URL))'
build_release 0.0.2
cp "$REPO_DIR/install.ps1" "$DIST/install.ps1"
install_ps1 "$WORK/iex-install.log" iex \
    || { cat "$WORK/iex-install.log" >&2; fail "iex install.ps1 failed"; }
GOT=$(docflow --version | tr -d '\r')
[ "$GOT" = 0.0.2 ] || fail "docflow --version: got '$GOT', want '0.0.2'"
COUNT=$(path_entry_count)
[ "$COUNT" = 1 ] || fail "user PATH entries after the iex install: got $COUNT, want 1"

echo '== uninstall'
docflow uninstall >/dev/null || fail 'docflow uninstall failed'
GOT=''
for _ in $(seq 1 100); do
    GOT=$(find "$HOME_DIR" -maxdepth 1 -name '.docflow' -print)
    [ -z "$GOT" ] && break
    sleep 0.1
done
[ -z "$GOT" ] || fail '~/.docflow still exists after uninstall'
COUNT=$(path_entry_count)
[ "$COUNT" = 0 ] || fail "user PATH entries after uninstall: got $COUNT, want 0"

echo OK
