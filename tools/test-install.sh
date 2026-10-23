#!/bin/bash
# E2E test for install.sh: build release layouts with tools/release.sh,
# serve them over a local HTTP server the way GitHub Releases does, then
# run install.sh, docflow update across versions, and docflow uninstall.
# Uses a temporary HOME with ~/.zcode and stub claude/codex/pi CLIs at the
# front of PATH; also checks the release tag guard and that a development
# build refuses update.
# DOCFLOW_TEST_SH=dash runs install.sh under dash instead of sh.
set -euo pipefail

SH_BIN=${DOCFLOW_TEST_SH:-sh}

fail() {
    printf 'test-install: %s\n' "$1" >&2
    exit 1
}

command -v go >/dev/null 2>&1 || fail 'go is required'
command -v python3 >/dev/null 2>&1 || fail 'python3 is required'
command -v curl >/dev/null 2>&1 || fail 'curl is required'

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd -P) || fail 'cannot locate tools'
REPO_DIR=$(cd "$SCRIPT_DIR/.." && pwd -P) || fail 'cannot locate checkout'
[ -f "$REPO_DIR/install.sh" ] || fail "missing $REPO_DIR/install.sh"
command -v "$SH_BIN" >/dev/null 2>&1 || fail "shell not found: $SH_BIN"

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
STUB_BIN=$WORK/stub-bin
TOOL_BIN=$WORK/tool-bin
DIST=$WORK/dist
mkdir "$HOME_DIR" "$STUB_BIN" "$TOOL_BIN" "$DIST"

for agent in claude codex pi; do
    printf '#!/bin/sh\nexit 0\n' > "$STUB_BIN/$agent"
    chmod +x "$STUB_BIN/$agent"
done

# Keep the Go build cache when HOME moves to the sandbox.
GO_CACHE=$(go env GOCACHE 2>/dev/null) || fail 'cannot read go env GOCACHE'
[ -n "$GO_CACHE" ] && export GOCACHE

export HOME="$HOME_DIR"
mkdir "$HOME/.zcode"
export SHELL=/bin/zsh
# Real agent CLIs and a development docflow on the inherited PATH would mask
# the stubs: keep only the tools the test needs plus the system directories.
for tool in go python3 curl "$SH_BIN" zsh; do
    found=$(command -v "$tool" 2>/dev/null) || continue
    ln -sf "$found" "$TOOL_BIN/$(basename "$tool")"
done
export PATH="$STUB_BIN:$TOOL_BIN:/usr/bin:/bin:/usr/sbin:/sbin"

docflow() {
    "$HOME/.docflow/bin/docflow" "$@"
}

PATH_LINE='export PATH="$HOME/.docflow/bin:$PATH"'
SKILL_DIRS='.claude/skills .agents/skills .pi/agent/skills .zcode/skills'

path_line_count() {
    # grep -c prints 0 and exits 1 when nothing matches.
    grep -cF "$PATH_LINE" "$HOME/.zshrc" || true
}

build_release() {
    bash "$REPO_DIR/tools/release.sh" "$1" "$DIST" >/dev/null
}

PORT=$(python3 -c 'import socket
sock = socket.socket()
sock.bind(("127.0.0.1", 0))
print(sock.getsockname()[1])
sock.close()') || fail 'cannot pick a free port'

build_release 0.0.1

python3 -m http.server --bind 127.0.0.1 "$PORT" --directory "$DIST" \
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

echo '== install 0.0.1'
"$SH_BIN" "$REPO_DIR/install.sh" >"$WORK/install.log" 2>&1 \
    || { cat "$WORK/install.log" >&2; fail 'install.sh failed'; }
GOT=$(docflow --version)
[ "$GOT" = 0.0.1 ] || fail "docflow --version: got '$GOT', want '0.0.1'"
WANT_LINK=$HOME/.docflow/current/skills/bsv-docs
for dir in $SKILL_DIRS; do
    GOT=$(readlink "$HOME/$dir/bsv-docs") || fail "no skill link in ~/$dir"
    [ "$GOT" = "$WANT_LINK" ] \
        || fail "readlink ~/$dir/bsv-docs: got '$GOT', want '$WANT_LINK'"
done
COUNT=$(path_line_count)
[ "$COUNT" = 1 ] || fail "PATH lines in ~/.zshrc: got $COUNT, want 1"
if [ -e "$TOOL_BIN/zsh" ]; then
    # Only the PATH line in ~/.zshrc puts docflow on the hermetic PATH.
    # +m: without job control zsh does not take over the caller's terminal.
    GOT=$(env -u ZDOTDIR zsh -i +m -c 'command -v docflow' 2>/dev/null) \
        || fail 'interactive zsh does not find docflow'
    WANT=$HOME/.docflow/bin/docflow
    [ "$GOT" = "$WANT" ] || fail "zsh command -v docflow: got '$GOT', want '$WANT'"
else
    echo 'skip: zsh not found'
fi

echo '== reinstall 0.0.1'
"$SH_BIN" "$REPO_DIR/install.sh" >"$WORK/reinstall.log" 2>&1 \
    || { cat "$WORK/reinstall.log" >&2; fail 'second install.sh failed'; }
GOT=$(docflow --version)
[ "$GOT" = 0.0.1 ] || fail "docflow --version after reinstall: got '$GOT', want '0.0.1'"
COUNT=$(path_line_count)
[ "$COUNT" = 1 ] || fail "PATH lines in ~/.zshrc after reinstall: got $COUNT, want 1"

echo '== update to 0.0.2'
build_release 0.0.2
OUT=$(docflow update --check)
printf '%s\n' "$OUT" | grep -q '0.0.1 -> 0.0.2' \
    || fail "update --check: got '$OUT', want '0.0.1 -> 0.0.2'"
docflow update >"$WORK/update.log" 2>&1 \
    || { cat "$WORK/update.log" >&2; fail 'docflow update failed'; }
GOT=$(docflow --version)
[ "$GOT" = 0.0.2 ] || fail "docflow --version after update: got '$GOT', want '0.0.2'"
[ -f "$HOME/.claude/skills/bsv-docs/SKILL.md" ] \
    || fail 'skill link broken after update'

echo '== update to 0.0.3 keeps the previous version'
build_release 0.0.3
docflow update >/dev/null 2>&1 || fail 'docflow update to 0.0.3 failed'
GOT=$(docflow --version)
[ "$GOT" = 0.0.3 ] || fail "docflow --version after update: got '$GOT', want '0.0.3'"
GOT=$(ls "$HOME/.docflow/versions" | sort | tr '\n' ' ')
[ "$GOT" = '0.0.2 0.0.3 ' ] || fail "versions/: got '$GOT', want '0.0.2 0.0.3 '"

echo '== corrupted checksum aborts the update'
build_release 0.0.4
CHECKSUMS=$DIST/download/v0.0.4/checksums.txt
sed -i.bak 's/^../zz/' "$CHECKSUMS" && rm "$CHECKSUMS.bak"
if docflow update >"$WORK/bad-update.log" 2>&1; then
    fail 'docflow update succeeded with a corrupted checksum'
fi
GOT=$(docflow --version)
[ "$GOT" = 0.0.3 ] \
    || fail "version changed after a failed update: got '$GOT', want '0.0.3'"
GOT=$(readlink "$HOME/.docflow/current")
[ "$GOT" = versions/0.0.3 ] \
    || fail "current after a failed update: got '$GOT', want 'versions/0.0.3'"

echo '== development build refuses update'
DEV_REPO=$WORK/repo
mkdir -p "$DEV_REPO/.git"
(cd "$REPO_DIR" && go build -o "$DEV_REPO/bin/docflow" ./cmd/docflow) \
    || fail 'cannot build the development binary'
RC=0
"$DEV_REPO/bin/docflow" update >"$WORK/dev-update.log" 2>&1 || RC=$?
[ "$RC" = 1 ] || fail "development update: got exit $RC, want 1"
grep -qF 'bash tools/install-dev.sh' "$WORK/dev-update.log" \
    || fail "development update: no install-dev.sh hint in '$(cat "$WORK/dev-update.log")'"
GOT=$(docflow --version)
[ "$GOT" = 0.0.3 ] \
    || fail "version changed after a development update: got '$GOT', want '0.0.3'"

echo '== uninstall'
docflow uninstall >/dev/null || fail 'docflow uninstall failed'
[ ! -e "$HOME/.docflow" ] || fail '~/.docflow still exists after uninstall'
for dir in $SKILL_DIRS; do
    [ ! -L "$HOME/$dir/bsv-docs" ] || fail "skill link still exists: ~/$dir/bsv-docs"
done
COUNT=$(path_line_count)
[ "$COUNT" = 0 ] || fail "PATH lines in ~/.zshrc after uninstall: got $COUNT, want 0"
# grep -l exits 1 when no file matches, which is the expected outcome.
GOT=$(find "$HOME" -maxdepth 1 -type f -exec grep -lF '# docflow' {} + || true)
[ -z "$GOT" ] || fail "docflow marker left after uninstall in: $GOT"

echo '== release tag must match the source version'
SRC_VERSION=$(cd "$REPO_DIR" && go run ./cmd/docflow --version) \
    || fail 'cannot read the source version'
bash "$REPO_DIR/tools/check-release-tag.sh" "v$SRC_VERSION" >/dev/null 2>&1 \
    || fail "check-release-tag.sh rejects v$SRC_VERSION"
for tag in "$SRC_VERSION" v999.0.0; do
    if bash "$REPO_DIR/tools/check-release-tag.sh" "$tag" >/dev/null 2>&1; then
        fail "check-release-tag.sh accepts $tag for version $SRC_VERSION"
    fi
done

echo OK
