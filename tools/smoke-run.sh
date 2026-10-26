#!/usr/bin/env bash
# Smoke test of `docflow run` against the real Pi CLI with the
# built-in agent profile. Spends subscription quota: two tiny units on
# GLM Flash, assigned by `executor:` in the plan. Runs in a throwaway
# repository, never in this one; reads no agents config.
# --mixed: the second unit on Codex CLI by its own `Executor:`; spends
# Z.ai and Codex quota. --dry-run checks the plan without agent calls.
set -euo pipefail

mixed=0
dry_run=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        --mixed) mixed=1 ;;
        --dry-run) dry_run=1 ;;
        *) echo "usage: $0 [--mixed] [--dry-run]" >&2; exit 2 ;;
    esac
    shift
done

repo="$(cd "$(dirname "$0")/.." && pwd)"
docflow="$repo/bin/docflow$(cd "$repo" && go env GOEXE)"
export PATH="$repo/bin:$PATH"
work="$(mktemp -d)"

fail() {
    echo "smoke-run: FAIL: $*" >&2
    echo "smoke-run: kept $work" >&2
    exit 1
}

cd "$work"
git init -q
git config core.autocrlf false
git config user.name smoke
git config user.email smoke@example.invalid
"$docflow" init --agent pi >/dev/null
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

cat > docs/plans/P001-smoke.md <<'EOF'
---
id: P001
title: Smoke
type: feature
status: active
created: 2026-10-12
executor: pi | zai/glm-5.3-flash | high
---
## Goal
Two trivial files written by the runner's agent.

## Context

## Constraints
Use the absolute docflow executable path from README.md for verification;
an agent's shell profile may replace the inherited PATH.

## Traps

## Decisions

## Phase 1: Files

### [ ] P001.1.1 Write hello
Read:
- docs/docflow.md
- README.md
Do:
- Create `hello.txt` containing the single line `hello`.
Verify: `grep -qx hello hello.txt`
Commit: P001.1.1: write hello

### [ ] P001.1.2 Write bye
Read:
- docs/docflow.md
- README.md
Do:
- Create `bye.txt` containing the single line `bye`.
Verify: `grep -qx bye bye.txt`
EXECUTOR
Commit: P001.1.2: write bye
EOF

if [ "$mixed" -eq 1 ]; then
    sed 's/^EXECUTOR$/Executor: codex | gpt-6-luna | low/' \
        docs/plans/P001-smoke.md > plan.tmp
else
    sed '/^EXECUTOR$/d' docs/plans/P001-smoke.md > plan.tmp
fi
mv plan.tmp docs/plans/P001-smoke.md

git add -A
git commit -qm init
"$docflow" run --dry-run --all
if [ "$dry_run" -eq 1 ]; then
    cd /
    rm -rf "$work"
    echo 'smoke-run: dry-run ok'
    exit 0
fi

out="$work.out"
code=0
"$docflow" run >"$out" || code=$?
cat "$out"
[ "$code" -eq 0 ] || fail "docflow run exited with $code"

log="$(git log --format=%s)"
for subject in 'P001.1.1: write hello' 'P001.1.2: write bye'; do
    grep -qxF "$subject" <<<"$log" || fail "no commit '$subject'"
done
"$docflow" status | grep -q 'ready to close' || fail 'plan is not ready to close'

if [ "$mixed" -eq 1 ]; then
    for agent in zai codex; do
        grep -Eq "^quota: (.*; )?$agent " "$out" || fail "no quota line for $agent"
    done
fi

cd /
rm -rf "$work" "$out"
echo 'smoke-run: ok'
