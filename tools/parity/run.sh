#!/bin/bash
# Compare CLI calls in fresh fixtures at the same absolute path.
set -euo pipefail

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
    echo "usage: $0 <reference> [candidate]" >&2
    exit 2
fi

source_root="$(cd "$(dirname "$0")/../.." && pwd -P)"

binary_path() {
    if [ ! -f "$1" ] || [ ! -x "$1" ]; then
        echo "parity: not an executable file: $1" >&2
        return 2
    fi
    # Keep symlinks: the CLI itself resolves its executable for --version.
    printf '%s/%s\n' "$(cd "$(dirname "$1")" && pwd -P)" "$(basename "$1")"
}

reference="$(binary_path "$1")"
if [ "$#" -eq 2 ]; then
    candidate="$(binary_path "$2")"
else
    (cd "$source_root" && go build -o bin/docflow-go ./cmd/docflow)
    candidate="$source_root/bin/docflow-go"
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/docflow-parity.XXXXXX")"
work="$(cd "$work" && pwd -P)"
cleanup() { rm -rf "$work"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

run_scenario() {
    local binary="$1" scenario="$2" output="$3" baseline="$4" peer="$5"
    local code=0
    # Recreate HOME, CODEX_HOME and the fixture at identical paths, including
    # between scenarios. Logs live outside the disposable sandbox.
    rm -rf "$work/sandbox"
    mkdir -p "$work/sandbox/home" "$work/sandbox/codex" \
        "$work/sandbox/tmp" "$work/sandbox/repo" "$output"
    (
        cd "$work/sandbox/repo"
        env -i PATH="$PATH" HOME="$work/sandbox/home" \
            CODEX_HOME="$work/sandbox/codex" TMPDIR="$work/sandbox/tmp" \
            XDG_CONFIG_HOME="$work/sandbox/home/.config" \
            LC_ALL=C TZ=UTC TERM=dumb DOCFLOW_NOTIFY=0 DOCFLOW_SKILLS_CHECK=0 \
            GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null \
            GIT_AUTHOR_NAME='docflow parity' GIT_AUTHOR_EMAIL=parity@example.invalid \
            GIT_COMMITTER_NAME='docflow parity' GIT_COMMITTER_EMAIL=parity@example.invalid \
            GIT_AUTHOR_DATE=2000-01-01T00:00:00+0000 \
            GIT_COMMITTER_DATE=2000-01-01T00:00:00+0000 \
            /bin/bash "$source_root/tools/parity/driver.sh" \
            "$binary" "$scenario" "$output" "$baseline" "$peer" </dev/null
    ) >"$output/scenario.stdout" 2>"$output/scenario.stderr" || code=$?
    if [ "$code" -ne 0 ]; then
        cat "$output/scenario.stdout"
        cat "$output/scenario.stderr" >&2
    fi
    return "$code"
}

passed=0
skipped=0
for scenario in "$source_root"/tools/parity/scenarios/*.sh; do
    [ -f "$scenario" ] || continue
    name="$(basename "$scenario" .sh)"
    skip=0
    # Split only on whitespace; do not expand wildcard characters in names.
    set -f
    for excluded in ${PARITY_SKIP:-}; do
        if [ "$excluded" = "$name" ]; then skip=1; fi
    done
    set +f
    if [ "$skip" -eq 1 ]; then
        echo "parity: SKIP $name"
        skipped=$((skipped + 1))
        continue
    fi
    baseline="$work/reference/$name"
    actual="$work/candidate/$name"
    if ! run_scenario "$reference" "$scenario" "$baseline" '' "$candidate"; then
        echo "parity: FAIL $name (reference scenario)" >&2
        exit 1
    fi
    if ! run_scenario "$candidate" "$scenario" "$actual" "$baseline" "$reference"; then
        echo "parity: FAIL $name (candidate scenario)" >&2
        exit 1
    fi
    echo "parity: OK $name ($(cat "$baseline/count") calls)"
    passed=$((passed + 1))
done

if [ "$passed" -eq 0 ]; then
    echo "parity: no scenarios ran ($skipped skipped)" >&2
    exit 2
fi
echo "parity: $passed scenarios passed, $skipped skipped"
