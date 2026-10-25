#!/bin/bash
# Internal scenario process: each invocation starts with an empty environment.
set -euo pipefail
umask 022

parity_binary="$1"
parity_scenario="$2"
parity_output="$3"
parity_baseline="$4"
parity_peer="${5:-}"
parity_call=0

compare_call() {
    local stem="$1" field
    for field in args stdout stderr status; do
        if ! cmp -s "$parity_baseline/$stem.$field" "$parity_output/$stem.$field"; then
            echo "parity: $(basename "$parity_scenario" .sh) call $parity_call: $field differs" >&2
            # diff is diagnostic only; cmp above also detects binary differences.
            diff -u "$parity_baseline/$stem.$field" "$parity_output/$stem.$field" >&2 || :
            exit 1
        fi
    done
}

run_docflow() {
    local binary="$1"
    shift
    local stem code=0
    parity_call=$((parity_call + 1))
    printf -v stem '%04d' "$parity_call"
    printf '%s\0' "$@" >"$parity_output/$stem.args"
    "$binary" "$@" </dev/null \
        >"$parity_output/$stem.stdout" 2>"$parity_output/$stem.stderr" || code=$?
    printf '%s\n' "$code" >"$parity_output/$stem.status"
    if [ -n "$parity_baseline" ]; then compare_call "$stem"; fi
    return "$code"
}

docflow() { run_docflow "$parity_binary" "$@"; }

peer_docflow() {
    if [ -z "$parity_peer" ]; then
        echo "parity: scenario needs a peer binary" >&2
        exit 2
    fi
    run_docflow "$parity_peer" "$@"
}

# Native Windows programs find only .exe/.cmd on PATH: front a fake agent script
# "$HOME/bin/<name>" with a .cmd that runs it through Git Bash. No-op elsewhere.
parity_shim() {
    case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) ;; *) return 0 ;; esac
    local name bash_path
    bash_path="$(cygpath -m "$(command -v bash)")"
    for name in "$@"; do
        printf '@"%s" "%%~dp0%s" %%*\r\n' "$bash_path" "$name" >"$HOME/bin/$name.cmd"
    done
}

# Call once at the beginning of every scenario; add fixtures with shell/git.
parity_repo() {
    git -c init.templateDir= init -q -b main
    git config user.name 'docflow parity'
    git config user.email parity@example.invalid
    git config commit.gpgsign false
    git config core.autocrlf false
    printf 'docflow parity fixture\n' >fixture.txt
    git add fixture.txt
    git commit -q -m 'Create parity fixture'
}

# Source without a conditional: setup failures must retain errexit semantics.
source "$parity_scenario"
printf '%s\n' "$parity_call" >"$parity_output/count"
if [ "$parity_call" -eq 0 ]; then
    echo "parity: scenario made no docflow calls: $parity_scenario" >&2
    exit 1
fi
if [ -n "$parity_baseline" ] && ! cmp -s "$parity_baseline/count" "$parity_output/count"; then
    echo "parity: $(basename "$parity_scenario" .sh): call count differs" >&2
    diff -u "$parity_baseline/count" "$parity_output/count" >&2 || :
    exit 1
fi
