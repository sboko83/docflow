#!/bin/bash
# E2E test for install.sh: build release layouts with tools/release.sh,
# serve them over a local HTTP server the way GitHub Releases does, then
# run install.sh, docflow update across versions, and docflow uninstall.
# Uses a temporary HOME with ~/.zcode and stub claude/codex/pi CLIs at the
# front of PATH; also checks the release tag guard and that a development
# build refuses update, and the skills mode: DOCFLOW_SKILLS=project, the
# no-terminal global fallback, docflow update --skills switching the links.
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
if [ "$WINDOWS" = 1 ]; then
    # The skills mode question reads the console (CONIN$), which stays openable
    # without a console and has no setsid here: pin the mode so a scripted
    # install never blocks on the question. The no-terminal fallback is a
    # Unix-only case, marked as skipped on Windows below.
    export DOCFLOW_SKILLS=global
fi
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

# The skills mode question is asked on the controlling terminal. CI runners
# have none; a human running this script does, and its log is redirected, so
# the question would be invisible and the read would block. Start the children
# in a new session: /dev/tty then fails to open and docflow install/update take
# the scripted fallback (global links, one hint line, nothing stored).
no_tty() {
    "$PYTHON" -c 'import subprocess, sys
raise SystemExit(subprocess.run(sys.argv[1:], start_new_session=True).returncode)' "$@"
}

docflow() {
    if [ "$WINDOWS" = 1 ]; then
        "$HOME/.docflow/current/docflow.exe" "$@"
    else
        no_tty "$HOME/.docflow/bin/docflow" "$@"
    fi
}

# Run install.sh detached from the controlling terminal, with the log captured
# and any extra VAR=VALUE assignments in its environment.
install_sh() {
    local log=$1
    shift
    if [ "$WINDOWS" = 1 ]; then
        env "$@" "$SH_BIN" "$REPO_DIR/install.sh" >"$log" 2>&1
    else
        no_tty env "$@" "$SH_BIN" "$REPO_DIR/install.sh" >"$log" 2>&1
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

release_skill_names() {
    local skill
    for skill in "$REPO_DIR"/skills/*/; do
        basename "$skill"
    done
}

skill_links_present() {
    local label=$1 dir name got want
    for dir in $SKILL_DIRS; do
        for name in $(release_skill_names); do
            want=$HOME/.docflow/current/skills/$name
            got=$(read_link "$HOME/$dir/$name") || fail "$label: no skill link in ~/$dir/$name"
            [ "$got" = "$want" ] \
                || fail "$label: readlink ~/$dir/$name: got '$got', want '$want'"
        done
    done
}

skill_links_absent() {
    local label=$1 dir name
    for dir in $SKILL_DIRS; do
        for name in $(release_skill_names); do
            [ ! -L "$HOME/$dir/$name" ] && [ ! -e "$HOME/$dir/$name" ] \
                || fail "$label: a docflow skill is in ~/$dir/$name"
        done
    done
}

skills_mode_is() {
    local label=$1 want=$2 got
    got=$(cat "$HOME/.docflow/skills-mode" 2>/dev/null) \
        || fail "$label: no ~/.docflow/skills-mode"
    [ "$got" = "$want" ] \
        || fail "$label: skills-mode: got '$got', want '$want'"
}

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
install_sh "$WORK/install.log" || { cat "$WORK/install.log" >&2; fail 'install.sh failed'; }
GOT=$(docflow --version)
[ "$GOT" = 0.0.1 ] || fail "docflow --version: got '$GOT', want '0.0.1'"
skill_links_present 'install 0.0.1'
if [ "$WINDOWS" = 1 ]; then
    echo 'skip: the no-terminal skills mode needs setsid, which Windows lacks'
else
    # No DOCFLOW_SKILLS and no controlling terminal: docflow install links
    # globally, hints, and stores nothing, so scripted installs keep working.
    [ ! -e "$HOME/.docflow/skills-mode" ] \
        || fail "skills-mode written without a terminal: $(cat "$HOME/.docflow/skills-mode")"
    grep -qF 'docflow update --skills project' "$WORK/install.log" \
        || fail "install.sh: no global fallback hint in '$(cat "$WORK/install.log")'"
fi
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
install_sh "$WORK/reinstall.log" || { cat "$WORK/reinstall.log" >&2; fail 'second install.sh failed'; }
GOT=$(docflow --version)
[ "$GOT" = 0.0.1 ] || fail "docflow --version after reinstall: got '$GOT', want '0.0.1'"
COUNT=$(path_line_count)
[ "$COUNT" = 1 ] || fail "PATH lines in ~/.zshrc after reinstall: got $COUNT, want 1"

echo '== update --skills global stores the mode at the current version'
# The scripted updates below then follow the stored mode and never read a
# terminal; --skills applies to the links even with no newer release.
OUT=$(docflow update --skills global)
printf '%s\n' "$OUT" | grep -qxF 'up to date: 0.0.1' \
    || fail "update --skills global: got '$OUT', want 'up to date: 0.0.1'"
skills_mode_is 'update --skills global' global
skill_links_present 'update --skills global'

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

echo '== install with DOCFLOW_SKILLS=project leaves the personal directories clean'
install_sh "$WORK/project-install.log" DOCFLOW_SKILLS=project \
    || { cat "$WORK/project-install.log" >&2; fail 'DOCFLOW_SKILLS=project install.sh failed'; }
GOT=$(docflow --version)
[ "$GOT" = 0.0.3 ] \
    || fail "docflow --version after the project install: got '$GOT', want '0.0.3'"
skills_mode_is 'DOCFLOW_SKILLS=project install' project
skill_links_absent 'DOCFLOW_SKILLS=project install'
grep -qF 'skills: project mode' "$WORK/project-install.log" \
    || fail "project install: no project-mode line in '$(cat "$WORK/project-install.log")'"

echo '== update in project mode keeps the directories clean'
OUT=$(docflow update)
printf '%s\n' "$OUT" | grep -qxF 'up to date: 0.0.3' \
    || fail "update in project mode: got '$OUT', want 'up to date: 0.0.3'"
if printf '%s\n' "$OUT" | grep -q 'skills'; then
    fail "update in project mode touched the links: '$OUT'"
fi
skills_mode_is 'update in project mode' project
skill_links_absent 'update in project mode'

echo '== update --skills global creates the personal links'
OUT=$(docflow update --skills global)
printf '%s\n' "$OUT" | grep -qxF 'up to date: 0.0.3' \
    || fail "update --skills global: got '$OUT', want 'up to date: 0.0.3'"
skills_mode_is 'update --skills global' global
skill_links_present 'update --skills global'

echo '== update --skills project keeps a foreign skill'
FOREIGN=$WORK/foreign-skill
mkdir "$FOREIGN"
printf 'foreign\n' > "$FOREIGN/SKILL.md"
if [ "$WINDOWS" = 1 ]; then
    # MSYS ln -s needs a privilege the runner does not grant. A plain directory
    # is the same case for the installer: not docflow's, must stay.
    mkdir "$HOME/.claude/skills/foreign-skill"
    cp "$FOREIGN/SKILL.md" "$HOME/.claude/skills/foreign-skill/SKILL.md"
else
    ln -s "$FOREIGN" "$HOME/.claude/skills/foreign-skill" \
        || fail 'cannot create the foreign skill link'
fi
OUT=$(docflow update --skills project)
printf '%s\n' "$OUT" | grep -qxF 'up to date: 0.0.3' \
    || fail "update --skills project: got '$OUT', want 'up to date: 0.0.3'"
printf '%s\n' "$OUT" | grep -qF 'skills: project mode' \
    || fail "update --skills project: no project-mode line in '$OUT'"
skills_mode_is 'update --skills project' project
skill_links_absent 'update --skills project'
if [ "$WINDOWS" = 0 ]; then
    read_link "$HOME/.claude/skills/foreign-skill" >/dev/null \
        || fail 'the foreign skill link is gone after the switch to project'
fi
[ -f "$HOME/.claude/skills/foreign-skill/SKILL.md" ] \
    || fail 'the foreign skill is gone after the switch to project'

echo '== update --skills global restores the personal links for the sections below'
# install-dev.sh below must find the release links to report that it redirects
# them to the checkout, so the mode returns to global here.
OUT=$(docflow update --skills global)
printf '%s\n' "$OUT" | grep -qxF 'up to date: 0.0.3' \
    || fail "update --skills global (restore): got '$OUT', want 'up to date: 0.0.3'"
skills_mode_is 'update --skills global (restore)' global
skill_links_present 'update --skills global (restore)'
[ -f "$HOME/.claude/skills/foreign-skill/SKILL.md" ] \
    || fail 'update --skills global removed the foreign skill'
# rm does not follow a link: this unlinks the link, the target stays.
rm -rf "$HOME/.claude/skills/foreign-skill" \
    || fail 'cannot remove the foreign skill'

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
mkdir -p "$WORK/repo"
# install-dev.sh links the physical checkout path (macOS TMPDIR is under /var).
DEV_REPO=$(cd "$WORK/repo" && pwd -P) || fail 'cannot resolve the development checkout'
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
