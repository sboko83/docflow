#!/bin/bash
parity_repo
mkdir -p docs/plans "$HOME/bin"
cat >docs/docflow.md <<'EOF'
---
docflow: 3
read_budget: 40000
---
## Queue
- P001

## Backlog
EOF
cat >"$HOME/plan" <<'EOF'
---
id: P001
title: Replan rules
type: feature
status: active
created: 2026-10-23
executor: claude | opus | high
replan: claude | haiku | low
---
## Goal
Check replan rejection.

## Context

## Constraints

## Traps

## Decisions

## Phase 1: First

### [x] P001.1.1 Done
Read:
- fixture.txt
Do:
- previous work
Verify: `true`
Commit: P001.1.1: done
Handoff:
- done history

### [ ] P001.1.2 Blocked
Read:
- fixture.txt
Do:
- new work
Verify: `true`
Commit: P001.1.2: new work
EOF

# Fake provider only: no real CLI, credentials or provider requests.
cat >"$HOME/bin/claude" <<'EOF'
#!/bin/bash
set -eu
case "$*" in *'/usage'*) exit 0 ;; esac
cat >/dev/null
plan=docs/plans/P001-plan.md
if [ ! -f "$HOME/replan-called" ]; then
    touch "$HOME/replan-called"
    printf '%s\n' '{"type":"result","is_error":false,"structured_output":{"status":"blocked","handoff":[],"decisions":[],"unverified":[],"reason":"fix the plan","blocker":"plan"}}'
    exit 0
fi
cp "$plan" "$HOME/before-replan"
sed 's/### \[!\] P001.1.2/### [~] P001.1.2/' "$plan" >"$HOME/edited"
case "$REPLAN_CASE" in
    header) sed 's/title: Replan rules/title: changed/' "$HOME/edited" >"$plan" ;;
    done) sed 's/done history/changed history/' "$HOME/edited" >"$plan" ;;
    other) cp "$HOME/edited" "$plan"; printf 'foreign\n' >foreign.txt ;;
esac
printf '%s\n' '{"type":"result","is_error":false,"structured_output":{"status":"done","handoff":["replanned"],"decisions":[],"unverified":[],"reason":"","blocker":""}}'
EOF

# Compare all deterministic output and the original exit code. Only the two
# run summary lines contain wall-clock time/duration; keep raw logs for checks.
cat >"$HOME/bin/replan-cli" <<'EOF'
#!/bin/bash
set -eu
rc=0
"$PARITY_REPLAN_BINARY" "$@" >"$HOME/raw.stdout" 2>"$HOME/raw.stderr" || rc=$?
sed '/^run:/d' "$HOME/raw.stdout"
cat "$HOME/raw.stderr" >&2
exit "$rc"
EOF
chmod +x "$HOME/bin/claude" "$HOME/bin/replan-cli"
parity_shim claude
export PATH="$HOME/bin:$PATH"
export PARITY_REPLAN_BINARY="$parity_binary"

for REPLAN_CASE in header done other; do
    export REPLAN_CASE
    cp "$HOME/plan" docs/plans/P001-plan.md
    rm -f "$HOME/replan-called" foreign.txt
    git add -A
    git commit -q --allow-empty -m "Prepare $REPLAN_CASE rejection"
    rc=0
    run_docflow "$HOME/bin/replan-cli" run --no-wait || rc=$?
    [ "$rc" -eq 3 ]
    cmp "$HOME/before-replan" docs/plans/P001-plan.md
    case "$REPLAN_CASE" in
        header) grep -q 'plan header changed' "$HOME/raw.stderr" ;;
        done) grep -q 'unit P001.1.1 \[x\] changed' "$HOME/raw.stderr" ;;
        other) grep -q 'the tree changed outside the plan file' "$HOME/raw.stderr"; test -f foreign.txt ;;
    esac
done
