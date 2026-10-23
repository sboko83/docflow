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
title: Finish after verify
type: feature
status: active
created: 2026-10-22
---
## Goal
Finish a verified unit.

## Context

## Constraints

## Prerequisites

## Traps

## Decisions

## Phase 1: Finish

### [ ] P001.1.1 Finish after verify
Read:
- tracked.txt
Do:
- change the file
Verify: `true`
Commit: P001.1.1: finish after verify
EOF
printf 'base\n' >tracked.txt
git add -A
git commit -q -m 'Create finish fixture'

docflow prompt P001.1.1
printf 'edited\n' >tracked.txt
docflow verify P001.1.1
docflow finish P001.1.1 --handoff 'finish verified tree'
