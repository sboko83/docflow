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
WINDOWS=0
EXE=
PYTHON=python3
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) WINDOWS=1; EXE=.exe; PYTHON=python ;;
esac

fail() {
    printf 'test-install: %s\n' "$1" >&2
    exit 1
}

command -v go >/dev/null 2>&1 || fail 'go is required'
command -v "$PYTHON" >/dev/null 2>&1 || fail "$PYTHON is required"
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
    if [ "$WINDOWS" = 1 ]; then
        printf '@exit /b 0\r\n' > "$STUB_BIN/$agent.cmd"
    fi
done

# Keep the Go build cache when HOME moves to the sandbox.
GO_CACHE=$(go env GOCACHE 2>/dev/null) || fail 'cannot read go env GOCACHE'
[ -n "$GO_CACHE" ] && export GOCACHE="$GO_CACHE"

export HOME="$HOME_DIR"
if [ "$WINDOWS" = 1 ]; then
    export USERPROFILE
    USERPROFILE=$(cygpath -w "$HOME_DIR")
    # The override must exist before the first install: an invalid override
    # fails closed in Go rather than falling back to the real registry.
    printf 'C:\\test-tools' > "$WORK/user-path"
    export DOCFLOW_TEST_USER_PATH
    DOCFLOW_TEST_USER_PATH=$(cygpath -w "$WORK/user-path")
    export MSYS="${MSYS:+$MSYS }winsymlinks:nativestrict"
fi
mkdir "$HOME/.zcode"
export SHELL=/bin/zsh
# Real agent CLIs and a development docflow on the inherited PATH would mask
# the stubs: keep only the tools the test needs plus the system directories.
TOOL_PATH=
for tool in go "$PYTHON" curl "$SH_BIN" zsh git; do
    found=$(command -v "$tool" 2>/dev/null) || continue
    if [ "$WINDOWS" = 1 ]; then
        TOOL_PATH="$TOOL_PATH:$(dirname "$found")"
    else
        ln -sf "$found" "$TOOL_BIN/$(basename "$tool")"
    fi
done
export PATH="$STUB_BIN:$TOOL_BIN:/usr/bin:/bin:/usr/sbin:/sbin$TOOL_PATH"

docflow() {
    if [ "$WINDOWS" = 1 ]; then
        "$HOME/.docflow/current/docflow.exe" "$@"
    else
        "$HOME/.docflow/bin/docflow" "$@"
    fi
}

read_link() {
    if [ "$WINDOWS" = 1 ]; then
        local target
        target=$("$PYTHON" -c 'import os,sys; sys.stdout.write(os.readlink(sys.argv[1]))' "$1") || return 1
        cygpath -u "$target"
    else
        readlink "$1"
    fi
}

remove_skill_link() {
    if [ "$WINDOWS" = 1 ]; then
        "$PYTHON" -c 'import os,sys; os.rmdir(sys.argv[1])' "$1"
    else
        rm "$1"
    fi
}

PATH_LINE='export PATH="$HOME/.docflow/bin:$PATH"'
SKILL_DIRS='.claude/skills .agents/skills .pi/agent/skills .zcode/skills'

path_line_count() {
    if [ "$WINDOWS" = 1 ]; then
        "$PYTHON" -c 'import ntpath,os,pathlib
entry = ntpath.normcase(ntpath.join(os.environ["USERPROFILE"], ".docflow", "current"))
value = pathlib.Path(os.environ["DOCFLOW_TEST_USER_PATH"]).read_text()
print(sum(ntpath.normcase(p.rstrip("\\")) == entry for p in value.split(";")))' | tr -d '\r'
        return
    fi
    # grep -c prints 0 and exits 1 when nothing matches.
    grep -cF "$PATH_LINE" "$HOME/.zshrc" || true
}

build_release() {
    bash "$REPO_DIR/tools/release.sh" "$1" "$DIST" >/dev/null
}

PORT=$("$PYTHON" -c 'import socket
sock = socket.socket()
sock.bind(("127.0.0.1", 0))
print(sock.getsockname()[1])
sock.close()' | tr -d '\r') || fail 'cannot pick a free port'

build_release 0.0.1

"$PYTHON" -m http.server --bind 127.0.0.1 "$PORT" --directory "$DIST" \
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
    GOT=$(read_link "$HOME/$dir/bsv-docs") || fail "no skill link in ~/$dir"
    [ "$GOT" = "$WANT_LINK" ] \
        || fail "readlink ~/$dir/bsv-docs: got '$GOT', want '$WANT_LINK'"
done
COUNT=$(path_line_count)
[ "$COUNT" = 1 ] || fail "PATH lines in ~/.zshrc: got $COUNT, want 1"
if [ "$WINDOWS" = 1 ]; then
    GOT=$(PATH="$HOME/.docflow/current:$PATH" bash -c 'docflow --version') \
        || fail 'Git Bash does not find docflow on the configured Path'
    [ "$GOT" = 0.0.1 ] || fail "Git Bash docflow --version: $GOT"
elif [ -e "$TOOL_BIN/zsh" ]; then
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
GOT=$(read_link "$HOME/.docflow/current")
WANT=versions/0.0.3
[ "$WINDOWS" = 0 ] || WANT=$HOME/.docflow/versions/0.0.3
[ "$GOT" = "$WANT" ] \
    || fail "current after a failed update: got '$GOT', want '$WANT'"

echo '== development build refuses update'
DEV_REPO=$WORK/repo
mkdir -p "$DEV_REPO"
git init -q "$DEV_REPO"
# install-dev must not rebuild the binary currently running docflow verify.
for item in cmd internal templates skills tools go.mod LICENSE; do
    cp -R "$REPO_DIR/$item" "$DEV_REPO/"
done
(cd "$DEV_REPO" && go build -o "bin/docflow$EXE" ./cmd/docflow) \
    || fail 'cannot build the development binary'
RC=0
"$DEV_REPO/bin/docflow$EXE" update >"$WORK/dev-update.log" 2>&1 || RC=$?
[ "$RC" = 1 ] || fail "development update: got exit $RC, want 1"
grep -qF 'bash tools/install-dev.sh' "$WORK/dev-update.log" \
    || fail "development update: no install-dev.sh hint in '$(cat "$WORK/dev-update.log")'"
GOT=$(docflow --version)
[ "$GOT" = 0.0.3 ] \
    || fail "version changed after a development update: got '$GOT', want '0.0.3'"

echo '== install-dev.sh relinks release skills to the checkout'
bash "$DEV_REPO/tools/install-dev.sh" >"$WORK/install-dev.log" 2>&1 \
    || { cat "$WORK/install-dev.log" >&2; fail 'install-dev.sh failed'; }
grep -qF 'relinked' "$WORK/install-dev.log" \
    || fail "install-dev.sh: no relink message in '$(cat "$WORK/install-dev.log")'"
for dir in $SKILL_DIRS; do
    for skill in "$DEV_REPO"/skills/*/; do
        name=$(basename "$skill")
        GOT=$(read_link "$HOME/$dir/$name") || fail "no skill link ~/$dir/$name"
        WANT=$DEV_REPO/skills/$name
        [ "$GOT" = "$WANT" ] \
            || fail "readlink ~/$dir/$name after install-dev.sh: got '$GOT', want '$WANT'"
    done
done
# Put the release links back so uninstall below checks its own links.
for dir in $SKILL_DIRS; do
    for skill in "$REPO_DIR"/skills/*/; do
        remove_skill_link "$HOME/$dir/$(basename "$skill")"
    done
done
rm "$HOME/.local/bin/docflow$EXE"
for agent in claude codex pi; do
    docflow install-skills --agent "$agent" >/dev/null \
        || fail "release install-skills --agent $agent failed"
done
docflow install-skills --target "$HOME/.zcode/skills" >/dev/null \
    || fail 'release install-skills --target ~/.zcode/skills failed'

echo '== uninstall'
docflow uninstall >/dev/null || fail 'docflow uninstall failed'
[ ! -e "$HOME/.docflow" ] || fail '~/.docflow still exists after uninstall'
if [ "$WINDOWS" = 1 ]; then
    for _ in $(seq 1 100); do
        GOT=$(find "$HOME" -maxdepth 1 -name '.docflow-uninstall-*' -print)
        [ -z "$GOT" ] && break
        sleep 0.1
    done
    [ -z "$GOT" ] || fail "uninstall did not clean up its moved executable: $GOT"
fi
for dir in $SKILL_DIRS; do
    [ ! -L "$HOME/$dir/bsv-docs" ] || fail "skill link still exists: ~/$dir/bsv-docs"
    [ ! -e "$HOME/$dir/bsv-docs" ] || fail "skill directory still exists: ~/$dir/bsv-docs"
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
