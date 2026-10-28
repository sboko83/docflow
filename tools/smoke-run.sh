#!/usr/bin/env bash
# Smoke test of `docflow run` against the real Pi CLI with the
# built-in agent profile. Spends subscription quota: two tiny units on
# GLM Flash, assigned by `executor:` in the plan. Runs in a throwaway
# repository, never in this one; reads no agents config.
# --mixed: the second unit on Codex CLI by its own `Executor:`; spends
# Z.ai and Codex quota. --dry-run checks the plan without agent calls.
# --unblock: the live re-check rule after `docflow unblock` — three runs
# of one guarded unit on the local strata model, no subscription quota.
set -euo pipefail
# The script drives docflow by hand, like a person: drop the runner guard
# so it also works when launched from an agent session inside `docflow run`.
unset DOCFLOW_RUNNER_UNIT

mixed=0
dry_run=0
unblock=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        --mixed) mixed=1 ;;
        --dry-run) dry_run=1 ;;
        --unblock) unblock=1 ;;
        *) echo "usage: $0 [--mixed] [--unblock] [--dry-run]" >&2; exit 2 ;;
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

if [ "$unblock" -eq 1 ]; then
    cat > docs/plans/P001-unblock.md <<'EOF'
---
id: P001
title: Unblock re-check
type: feature
status: active
created: 2026-10-12
executor: pi | strata/swift-1.5-iq3_s | -
---
## Goal
A unit guarded by a condition only a person can satisfy, plus a trailing
unit that must stay untouched: the runner must re-check the unblock note
with a command instead of trusting it.

## Constraints
Use the absolute docflow executable path from README.md for verification;
an agent's shell profile may replace the inherited PATH.

## Context

## Traps

## Decisions

## Phase 1: Flag

### [ ] P001.1.1 Write hello behind the flag
Read:
- docs/docflow.md
- README.md
Do:
- Check the condition first with a command: `test -f unblock.flag`.
  While it is missing, this unit needs a person: reply blocked with
  `blocker: "human"` naming `unblock.flag is missing`, create nothing
  and skip the rest of `Do:` (the runner records the block for you).
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
Commit: P001.1.2: write bye
EOF
else
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
fi

git add -A
git commit -qm init
"$docflow" run --dry-run --all
if [ "$dry_run" -eq 1 ]; then
    cd /
    rm -rf "$work"
    echo 'smoke-run: dry-run ok'
    exit 0
fi

if [ "$unblock" -eq 1 ]; then
    plan=docs/plans/P001-unblock.md
    out="$work.out"

    # Branch 1: the flag is missing — the agent names the missing human
    # step, the runner records the block and the run stops with code 3.
    code=0
    "$docflow" run --yes --max-units 1 >"$out" || code=$?
    cat "$out"
    [ "$code" -eq 3 ] || fail "branch 1: docflow run exited with $code, want 3 (blocked)"
    grep -q 'blocked (needs human)' "$out" || fail 'branch 1: no human block in the run output'
    grep -q '^### \[!\] P001.1.1' "$plan" || fail 'branch 1: unit is not [!]'
    grep -q '^- Blocked (human): ' "$plan" || fail 'branch 1: no Blocked (human) item'

    # Branch 2: unblock on a false claim — the agent must re-check the
    # condition with a command, find it unmet and block the unit again.
    "$docflow" unblock P001.1.1 --note "flag" >/dev/null || fail 'branch 2: unblock failed'
    grep -q '^### \[~\] P001.1.1' "$plan" || fail 'branch 2: unit is not [~]'
    grep -q '^- Unblocked: flag' "$plan" || fail 'branch 2: no Unblocked item'
    code=0
    "$docflow" run --yes --max-units 1 >"$out" || code=$?
    cat "$out"
    [ "$code" -eq 3 ] || fail "branch 2: docflow run exited with $code, want 3 (blocked again)"
    [ "$(grep -c '^- Blocked (human): ' "$plan")" -ge 2 ] || fail 'branch 2: unit was not blocked again'
    grep -q '^### \[!\] P001.1.1' "$plan" || fail 'branch 2: unit is not [!] again'

    # Branch 3: the flag exists, the claim is true — the agent re-checks
    # with a command, does the unit and the run finishes with code 0.
    cd "$work"
    touch unblock.flag
    [ -f unblock.flag ] || fail 'branch 3: unblock.flag is not in the repository root'
    "$docflow" unblock P001.1.1 --note "flag" >/dev/null || fail 'branch 3: unblock failed'
    code=0
    "$docflow" run --yes --max-units 1 >"$out" || code=$?
    cat "$out"
    [ "$code" -eq 0 ] || fail "branch 3: docflow run exited with $code, want 0"
    grep -q '^### \[x\] P001.1.1' "$plan" || fail 'branch 3: unit is not [x]'
    grep -q '^### \[ \] P001.1.2' "$plan" || fail 'branch 3: the second unit was touched'
    grep -qxF 'P001.1.1: write hello' <<<"$(git log --format=%s)" \
        || fail 'branch 3: no commit for the unit'

    cd /
    rm -rf "$work" "$out"
    echo 'smoke-run: ok unblock'
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
