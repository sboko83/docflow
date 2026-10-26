#!/bin/bash
# P044 acceptance: an unverified item copied from a Depends unit is dropped,
# the prompt tells the agent not to copy it, and init writes the plan trigger.
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
printf 'a\n' >fixture.txt
cat >docs/plans/P001-plan.md <<'EOF'
---
id: P001
title: Unverified discipline
type: feature
status: active
created: 2026-10-23
executor: claude | opus | high
---
## Goal
Check the unverified discipline.

## Context

## Constraints

## Traps

## Decisions

## Phase 1: First

### [ ] P001.1.1 Base
Read:
- fixture.txt
Do:
- base work
Verify: `true`
Commit: P001.1.1: base

### [ ] P001.1.2 Dependent
Read:
- fixture.txt
Do:
- dependent work
Verify: `true`
Depends: P001.1.1
Commit: P001.1.2: dependent
EOF
git add -A
git commit -q -m 'Create unverified plan'

# Fake provider only: the reply depends on the unit in the prompt, and the
# prompt is kept for the checks below.
cat >"$HOME/bin/claude" <<'EOF'
#!/bin/bash
set -eu
case "$*" in *'/usage'*) exit 0 ;; esac
cat >"$HOME/prompt"
if grep -q '^# P001.1.2 ' "$HOME/prompt"; then
    cp "$HOME/prompt" "$HOME/prompt-2"
    "$PARITY_UNVERIFIED_BINARY" prompt P001.1.2 >"$HOME/manual-prompt-2"
    printf 'b\n' >>fixture.txt
    printf '%s\n' '{"type":"result","is_error":false,"structured_output":{"status":"done","handoff":["dependent done"],"decisions":[],"unverified":["X (inherited from P001.1.1)","Y"],"reason":"","blocker":""}}'
else
    printf 'c\n' >>fixture.txt
    printf '%s\n' '{"type":"result","is_error":false,"structured_output":{"status":"done","handoff":["base done"],"decisions":[],"unverified":["X"],"reason":"","blocker":""}}'
fi
EOF

# The run summary carries wall-clock time; compare the rest, keep raw output.
cat >"$HOME/bin/unverified-cli" <<'EOF'
#!/bin/bash
set -eu
rc=0
"$PARITY_UNVERIFIED_BINARY" "$@" >"$HOME/raw.stdout" 2>"$HOME/raw.stderr" || rc=$?
sed -E -e '/^run:/d' -e '/ (time|done): /d' "$HOME/raw.stdout"
cat "$HOME/raw.stderr" >&2
exit "$rc"
EOF
chmod +x "$HOME/bin/claude" "$HOME/bin/unverified-cli"
parity_shim claude
export PATH="$HOME/bin:$PATH"
export PARITY_UNVERIFIED_BINARY="$parity_binary"

# 1. The copy of the Depends item is dropped; only the unit's own risk stays.
run_docflow "$HOME/bin/unverified-cli" run --no-wait
grep -q '^run: 2 units, .*, 2 unverified' "$HOME/raw.stdout"
awk '/^### \[x\] P001.1.2 /{on=1; next} /^### /{on=0} on' docs/plans/P001-plan.md >"$HOME/handoff-2"
grep -qx -- '- Unverified: Y' "$HOME/handoff-2"
if grep -q 'Unverified: X' "$HOME/handoff-2"; then exit 1; fi
grep -qx -- '- Unverified: X' docs/plans/P001-plan.md

# 2. `docflow prompt` and the runner prompt of the dependent unit carry the
# Depends rule; only the runner's cycle has the unverified bullet.
note='`Unverified:` items below stay open on their own units; do not repeat them in your `unverified`.'
for prompt in "$HOME/manual-prompt-2" "$HOME/prompt-2"; do
    awk '/^## Handoff from Depends$/{on=1; next} /^### /{on=0} on' "$prompt" >"$HOME/depends"
    grep -qxF -- "$note" "$HOME/depends"
done
grep -qF 'unverified: a risk of what this unit changed that `Verify` does not' "$HOME/prompt-2"
grep -qF 'Not: items already' "$HOME/prompt-2"

# 3. init in a fresh repository writes the plan trigger line into AGENTS.md.
mkdir "$HOME/fresh"
cd "$HOME/fresh"
git init -q -b main
git commit -q --allow-empty -m init
docflow init
grep -qxF 'Plans: docflow (run docflow status), only on an explicit request; not the bsv-docs PLAN genre' AGENTS.md
