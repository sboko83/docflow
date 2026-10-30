#!/usr/bin/env bash
# Smoke test of `docflow run` against the real OpenCode CLI with the
# built-in agent profile on the OpenCode go subscription. Spends a small
# amount of subscription quota: two tiny units, one on the #low variant
# and one on #high of `opencode-go/deepseek-v4.1-flash`, assigned by
# `Executor:` in the plan. Runs in a throwaway repository, never in this
# one; reads no agents config.
# --dry-run checks the routes without agent calls and needs no opencode.
# --binary PATH runs another docflow executable than bin/docflow.
set -euo pipefail
# The script drives docflow by hand, like a person: drop the runner guard
# so it also works when launched from an agent session inside `docflow run`.
unset DOCFLOW_RUNNER_UNIT

model='opencode-go/deepseek-v4.1-flash'
dry_run=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        --dry-run) dry_run=1 ;;
        --binary)
            [ "$#" -ge 2 ] || { echo "usage: $0 [--dry-run] [--binary PATH]" >&2; exit 2; }
            docflow="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"
            shift ;;
        *) echo "usage: $0 [--dry-run] [--binary PATH]" >&2; exit 2 ;;
    esac
    shift
done

repo="$(cd "$(dirname "$0")/.." && pwd)"
if [ -z "${docflow:-}" ]; then
    docflow="$repo/bin/docflow"
    [ -x "$docflow" ] || docflow="$repo/bin/docflow$(cd "$repo" && go env GOEXE)"
fi
[ -x "$docflow" ] || {
    echo "smoke-opencode: no docflow binary at $docflow" >&2
    echo 'smoke-opencode: build it first: go build -o bin/docflow ./cmd/docflow (or pass --binary PATH)' >&2
    exit 1
}
export PATH="$repo/bin:$PATH"
work="$(mktemp -d)"

fail() {
    echo "smoke-opencode: FAIL: $*" >&2
    echo "smoke-opencode: kept $work" >&2
    exit 1
}
# A clean exit removes the scratch repository; a failure keeps it for
# inspection and names the path above.
trap 'status=$?; cd /; if [ "$status" -eq 0 ]; then rm -rf "$work"; fi' EXIT

cd "$work"
git init -q
git config core.autocrlf false
git config user.name smoke
git config user.email smoke@example.invalid
"$docflow" init --agent opencode >/dev/null
printf 'Use the absolute docflow executable path for verification: `%s`.\n' \
    "$docflow" > README.md

cat > docs/docflow.md <<'EOF'
---
docflow: 3
read_budget: 40000
---
## Queue
- P001

## Backlog
EOF

cat > docs/plans/P001-smoke.md <<EOF
---
id: P001
title: OpenCode smoke
type: feature
status: active
created: 2026-10-30
gate: true
---
## Goal
Two trivial files written by the runner's agent on the OpenCode go
subscription: unit 1 on the #low variant of $model,
unit 2 on #high.

## Context

## Constraints
Use the absolute docflow executable path from README.md for verification;
an agent's shell profile may replace the inherited PATH.

## Traps

## Decisions

## Phase 1: Files

### [ ] P001.1.1 Write hello
Executor: opencode | $model | low
Read:
- docs/docflow.md
- README.md
Do:
- Create \`hello.txt\` containing the single line \`hello\`.
Verify: \`grep -qx hello hello.txt\`
Commit: P001.1.1: write hello

### [ ] P001.1.2 Write bye
Executor: opencode | $model | high
Read:
- docs/docflow.md
- README.md
Do:
- Create \`bye.txt\` containing the single line \`bye\`.
Verify: \`grep -qx bye bye.txt\`
Commit: P001.1.2: write bye
EOF

git add -A
git commit -qm init

"$docflow" run --dry-run --all
grep -qF "P001.1.1 [ ] Write hello: opencode | $model | low (unit)" \
    <("$docflow" run --dry-run --all 2>&1) \
    || fail 'dry-run: no #low route line for P001.1.1'
grep -qF "P001.1.2 [ ] Write bye: opencode | $model | high (unit)" \
    <("$docflow" run --dry-run --all 2>&1) \
    || fail 'dry-run: no #high route line for P001.1.2'
if [ "$dry_run" -eq 1 ]; then
    echo 'smoke-opencode: dry-run ok'
    exit 0
fi

command -v opencode >/dev/null 2>&1 \
    || fail 'opencode CLI is not in PATH; the live smoke needs it (opencode --version)'

out="$work.out"
code=0
"$docflow" run --yes --no-notify >"$out" || code=$?
cat "$out"
[ "$code" -eq 0 ] || fail "docflow run exited with $code, want 0"

# The routes are in the unit lines: a silent fallback switch would show
# another executor there.
grep -qF "P001.1.1 Write hello -> opencode | $model | low" "$out" \
    || fail 'no #low route line for P001.1.1 in the run output'
grep -qF "P001.1.2 Write bye -> opencode | $model | high" "$out" \
    || fail 'no #high route line for P001.1.2 in the run output'

plan=docs/plans/P001-smoke.md
grep -q '^### \[x\] P001.1.1' "$plan" || fail 'P001.1.1 is not [x]'
grep -q '^### \[x\] P001.1.2' "$plan" || fail 'P001.1.2 is not [x]'
grep -qx hello hello.txt || fail 'hello.txt is wrong'
grep -qx bye bye.txt || fail 'bye.txt is wrong'

log="$(git log --format=%s)"
for subject in 'P001.1.1: write hello' 'P001.1.2: write bye'; do
    grep -qxF "$subject" <<<"$log" || fail "no commit '$subject'"
done
"$docflow" status | grep -q 'ready to close' || fail 'plan is not ready to close'

echo 'smoke-opencode: ok'
