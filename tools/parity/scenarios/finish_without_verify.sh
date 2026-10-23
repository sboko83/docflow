#!/bin/bash
parity_repo
mkdir -p docs/plans docs/archive
cat >docs/docflow.md <<'EOF'
---
docflow: 2
read_budget: 40000
---
## Queue
- P001

## Backlog
EOF
cat >docs/plans/P001-plan.md <<'EOF'
---
id: P001
title: Finish without verify
type: feature
status: active
created: 2026-10-22
---
## Goal
Do not finish a unit whose Verify command fails.

## Context

## Constraints

## Prerequisites

## Traps

## Decisions

## Phase 1: Finish

### [ ] P001.1.1 Verify required
Read:
- tracked.txt
Do:
- change the file
Verify: `false`
Commit: P001.1.1: verify required
EOF
printf 'base\n' >tracked.txt
git add -A
git commit -q -m 'Create finish fixture'

docflow prompt P001.1.1
printf 'edited\n' >tracked.txt
if docflow finish P001.1.1 --handoff 'must not finish'; then
    echo 'finish unexpectedly accepted a failed Verify command' >&2
    exit 1
else
    code=$?
    [ "$code" -eq 4 ] || exit "$code"
fi
docflow status
