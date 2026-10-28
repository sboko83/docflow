#!/bin/bash
# Exercise smoke-run's assertions without spending live-agent quota.
set -euo pipefail

source_root="$(cd "$(dirname "$0")/.." && pwd -P)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/checkout/tools" "$work/checkout/bin" "$work/stubs" "$work/tmp"
cp "$source_root/tools/smoke-run.sh" "$work/checkout/tools/"

# An empty GOEXE lets the fixture use a shell stub on Windows too.
cat > "$work/stubs/go" <<'EOF'
#!/bin/bash
[ "$*" = 'env GOEXE' ] || exit 99
EOF
cat > "$work/checkout/bin/docflow" <<'EOF'
#!/bin/bash
set -euo pipefail
case "$*" in
    'init --agent pi') mkdir -p docs/plans docs/archive ;;
    'run --dry-run --all') ;;
    'status') echo 'P001 ready to close' ;;
    'run --yes --max-units 1')
        sed 's/\[ \]/[x]/' docs/plans/P001-close.md > plan.tmp
        mv plan.tmp docs/plans/P001-close.md
        echo hello > hello.txt
        git add -A
        git commit -qm 'P001.1.1: write hello'
        mkdir -p .git/docflow/run
        echo unit > .git/docflow/run/P001.1.1.out
        ;;
    'run --dry-run --close') echo 'close: pi | zai/glm-5.3-flash | high' ;;
    'run --yes --close')
        for section in Outcome Decisions Gotchas Follow-ups; do
            printf '## %s\n\nNone.\n\n' "$section"
        done > docs/archive/P001-close.md
        rm docs/plans/P001-close.md
        if [ "$TEST_SMOKE_MODE" != queue-retained ]; then
            sed '/^- P001$/d' docs/docflow.md > queue.tmp
            mv queue.tmp docs/docflow.md
        fi
        git add -A
        git commit -qm 'P001: close plan'
        printf '%s\n' \
            '{"event":"close","plan":"P001","result":"done","exit_code":0,"route":{"model":"zai/glm-5.3-flash"}}' \
            '{"close":true,"event":"attempt"}' > .git/docflow/run/test.events.jsonl
        echo closed > .git/docflow/run/P001.close1.out
        ;;
    'run')
        echo hello > hello.txt
        git add hello.txt
        git commit -qm 'P001.1.1: write hello'
        echo bye > bye.txt
        git add bye.txt
        git commit -qm 'P001.1.2: write bye'
        ;;
    *) printf 'unexpected stub command: %s\n' "$*" >&2; exit 99 ;;
esac
EOF
chmod +x "$work/stubs/go" "$work/checkout/bin/docflow"
export PATH="$work/stubs:$PATH" TMPDIR="$work/tmp"

check() {
    local mode="$1" want_code="$2" want_text="$3" code=0
    shift 3
    TEST_SMOKE_MODE="$mode" bash "$work/checkout/tools/smoke-run.sh" "$@" \
        > "$work/output" 2>&1 || code=$?
    if [ "$code" -ne "$want_code" ] || ! grep -qF "$want_text" "$work/output"; then
        cat "$work/output" >&2
        printf 'test-smoke-run: %s: exit %s (want %s), expected %s\n' \
            "$mode" "$code" "$want_code" "$want_text" >&2
        exit 1
    fi
}

check success 0 'smoke-run: ok close' --close
check queue-retained 1 'P001 is still in the Queue' --close
check default 0 'smoke-run: ok'
check dry-run 0 'smoke-run: dry-run ok' --dry-run
echo 'test-smoke-run: ok (close success, retained queue, default, dry-run)'
