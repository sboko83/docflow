#!/bin/bash
parity_repo
mkdir -p docs/plans docs/archive
cat >docs/docflow.md <<'EOF'
---
docflow: 3
read_budget: 40000
---
## Queue
- P001

## Backlog
EOF
cat >docs/plans/P001-plan.md <<'EOF'
---
id: P001
title: Commit after peer verify
type: feature
status: active
created: 2026-10-22
---
## Goal
Commit a tree verified by the other CLI.

## Context

## Constraints

## Prerequisites

## Traps

## Decisions

## Phase 1: Commit

### [ ] P001.1.1 Cross-binary commit
Read:
- tracked.txt
Do:
- edit the file
Verify: `true`
Commit: P001.1.1: cross-binary commit
EOF
printf 'base\n' >tracked.txt
git add -A
git commit -q -m 'Create cross-binary fixture'

docflow prompt P001.1.1
printf 'verified edit\n' >tracked.txt
docflow verify P001.1.1
peer_docflow done P001.1.1 --handoff 'done by peer'
peer_docflow commit P001.1.1
