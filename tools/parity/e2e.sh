#!/bin/bash
# Shared offline fixtures for the six migrated acceptance suites.
e2e_dir="$(cd "$(dirname "$parity_scenario")/.." && pwd -P)"
e2e_source="$(cd "$e2e_dir/../.." && pwd -P)"
e2e_root="$PWD"
plan=docs/plans/P001-plan.md
mkdir -p "$HOME/bin" "$HOME/steps" "$XDG_CONFIG_HOME/docflow"
export FAKE_AGENT_SCENARIO="$HOME/steps" PROBE_LOG="$HOME/probes" STUB_LOG="$HOME/notifications"
export E2E_BINARY="$parity_binary"
export E2E_FAKE="$e2e_source/internal/agentio/testdata/fake_agent.sh"
e2e_path="$PATH"
cat >"$HOME/bin/claude" <<'EOF'
#!/bin/bash
case "$*" in
    *'/usage'*|*app-server*) printf '%s %s\n' "${0##*/}" "$*" >>"$PROBE_LOG"; exit 0 ;;
esac
exec /bin/bash "$E2E_FAKE" "$@"
EOF
cp "$HOME/bin/claude" "$HOME/bin/codex"
cat >"$HOME/bin/osascript" <<'EOF'
#!/bin/bash
printf '%s\n' "$*" >>"$STUB_LOG"
EOF
cp "$HOME/bin/osascript" "$HOME/bin/notify-send"
cat >"$HOME/bin/e2e-cli" <<'EOF'
#!/bin/bash
code=0
"$E2E_BINARY" "$@" >"$HOME/raw.stdout" 2>"$HOME/raw.stderr" || code=$?
# Only the start time, wall clock interval and elapsed duration vary.
# Retain the unit count, cost, unverified count and replan count.
sed -E \
    -e 's/^(run: P[0-9]+ .*, start )[0-9:]+/\1<clock>/' \
    -e 's/^(run: [0-9]+ units?, )[^,]*–[^,]* \([^)]*\)/\1<clock> (<duration>)/' \
    -e 's/^(P[0-9.]+ time: ).*/\1<duration>/' \
    -e 's/^(P[0-9.]+ done: [0-9a-f]+ \([0-9]+ attempts?, )[^,)]*/\1<duration>/' \
    "$HOME/raw.stdout"
cat "$HOME/raw.stderr" >&2
exit "$code"
EOF
chmod +x "$HOME/bin/claude" "$HOME/bin/codex" "$HOME/bin/osascript" "$HOME/bin/notify-send" "$HOME/bin/e2e-cli"
e2e_windows=0
e2e_exe=
e2e_bash=/bin/bash
e2e_fake_cmd="$E2E_FAKE"
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*)
        e2e_windows=1
        e2e_exe=.exe
        # Native programs cannot start /bin/bash or open POSIX paths.
        e2e_bash="$(cygpath -m "$(command -v bash)")"
        e2e_fake_cmd="$(cygpath -m "$E2E_FAKE")"
        parity_shim claude codex
        ;;
esac
export PATH="$HOME/bin:$PATH"

# Check success and refusals explicitly; run_docflow owns output comparison.
e2e_cli() {
    local expected="$1" code=0
    shift
    run_docflow "$HOME/bin/e2e-cli" "$@" || code=$?
    if [ "$code" -ne "$expected" ]; then
        cat "$HOME/raw.stdout"; cat "$HOME/raw.stderr" >&2
        echo "e2e: expected exit $expected, got $code" >&2
        exit 1
    fi
}

crash_commit() {
    cat >"$HOME/bin/crash-cli" <<'EOF'
#!/bin/bash
code=0
# Exec preserves this PID so the post-commit hook can kill the CLI itself.
/bin/bash -c 'printf "%s\n" "$$" >"$HOME/crash.pid"; exec "$E2E_BINARY" "$@"' -- "$@" \
    >"$HOME/crash.out" 2>"$HOME/crash.err" &
cli_pid=$!
wait "$cli_pid" 2>/dev/null || code=$?
cat "$HOME/crash.out"
cat "$HOME/crash.err" >&2
exit "$code"
EOF
    chmod +x "$HOME/bin/crash-cli"
    local code=0
    run_docflow "$HOME/bin/crash-cli" commit P001.1.1 || code=$?
    [ "$code" -eq 137 ]
}

observe() { run_docflow /bin/bash -c "$1"; }

edit() {
    sed "$2" "$1" >"$HOME/edited"
    mv "$HOME/edited" "$1"
}

save_fixture() {
    git add -A
    git commit -q --allow-empty -m "$1"
}

nested() {
    mkdir -p "$1"
    git -C "$1" -c init.templateDir= init -q -b main
    git -C "$1" config user.name 'docflow parity'
    git -C "$1" config user.email parity@example.invalid
    printf 'let a = 1\n' >"$1/main.swift"
    git -C "$1" add -A
    git -C "$1" commit -q -m init
}

repos_fixture() {
    parity_repo
    mkdir -p docs/plans docs/archive
    cp "$e2e_dir/fixtures/repos.md" "$plan"
    cp "$e2e_dir/fixtures/repos-docflow.md" docs/docflow.md
    printf 'print(1)\n' >a.py
    printf '/projects/\n/modules/\n' >.gitignore
    save_fixture repos
    nested projects/a
}

accept_fixture() {
    parity_repo
    e2e_cli 0 init
    printf '# Project\n' >README.md
    printf 'a\n' >a.txt
    printf 'c\n' >c.txt
    cp "$e2e_dir/fixtures/$1.md" "$plan"
}

activate_fixture() {
    save_fixture init
    e2e_cli 0 activate P001
    git commit -qam 'P001: activate plan'
}

head_route() { edit "$plan" "/created:/a\\
$1
"; }

unit_route() { edit "$plan" "/Commit: P001.1.$1/i\\
$2
"; }

state() {
    observe 'git log --format=%B; git status --porcelain; cat docs/plans/*.md'
}

agent_logs() {
    # Providers can be probed concurrently; the source mixed-executor case
    # also compares their sorted log. Agent calls and notifications stay ordered.
    observe 'if [ -f "$HOME/steps.log" ]; then cat "$HOME/steps.log"; fi; if [ -f "$PROBE_LOG" ]; then sort "$PROBE_LOG"; fi; if [ -f "$STUB_LOG" ]; then cat "$STUB_LOG"; fi'
}

loop_fixture() {
    parity_repo
    mkdir -p docs/plans docs/archive
    cp "$e2e_dir/fixtures/loop-docflow.md" docs/docflow.md
    cp "$e2e_dir/fixtures/loop.md" "$plan"
    cp "$e2e_dir/fixtures/other.md" docs/plans/P002-other.md
    # Use the P024.4.3 shell fake through a legacy custom adapter, as P016 did.
    cat >"$HOME/agents.toml" <<EOF
[tiers]
fast = {agent = "fake", model = "fast"}
default = {agent = "fake", model = ""}
strong = {agent = "fake", model = "strong"}
[agents.fake]
cmd = ["$e2e_bash", "$e2e_fake_cmd"]
model_args = ["--model", "{model}"]
resume_args = ["--resume", "{session}"]
result = "json:structured_output"
text = "json:result"
session = "json:session_id"
EOF
}

# Copy Go sources and resources into a checkout with spaces so the installer
# builds the CLI from that checkout.
checkout_fixture() {
    export HOME="$HOME/home with spaces"
    checkout="$HOME/checkout with spaces"
    bin_dir="$HOME/.local/bin"
    outside="$HOME/unrelated working directory"
    mkdir -p "$checkout/bin" "$checkout/tools" "$outside" "$HOME/isolated path"
    cp "$e2e_source/tools/install-dev.sh" "$checkout/tools/install-dev.sh"
    cp -R "$e2e_source/cmd" "$e2e_source/internal" \
        "$e2e_source/skills" "$e2e_source/templates" "$checkout/"
    cp "$e2e_source/go.mod" "$checkout/go.mod"
    # A shared Git-dir gives both copies the same version SHA, without
    # committing executable bytes (which necessarily differ by language).
    if [ "$e2e_windows" -eq 1 ]; then
        printf 'gitdir: %s/.git\n' "$(cygpath -m "$e2e_source")" >"$checkout/.git"
    else
        printf 'gitdir: %s/.git\n' "$e2e_source" >"$checkout/.git"
    fi
    if [ "$e2e_windows" -eq 1 ]; then
        # MSYS tools cannot be linked: copy them with their runtime DLLs.
        # go and git keep their own trees, so their directories join PATH.
        for tool in grep dirname ln mkdir cat readlink cmp basename diff; do
            cp "$(command -v "$tool")" "$HOME/isolated path/"
        done
        cp "$(dirname "$(command -v ln)")"/*.dll "$HOME/isolated path/"
        export PATH="$HOME/isolated path:$(dirname "$(command -v go)"):$(dirname "$(command -v git)")"
    else
        for tool in go grep git dirname ln mkdir cat readlink cmp basename diff; do
            ln -s "$(command -v "$tool")" "$HOME/isolated path/$tool"
        done
        export PATH="$HOME/isolated path"
    fi
    cd "$outside"
}

installer() { run_docflow /bin/bash "$checkout/tools/install-dev.sh" --no-skills "$@"; }

installed_cli() { run_docflow "$bin_dir/docflow$e2e_exe" "$@"; }
export plan e2e_exe
