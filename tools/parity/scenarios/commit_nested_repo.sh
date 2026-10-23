#!/bin/bash
parity_repo
mkdir -p docs/plans docs/archive deps/module
cat >docs/docflow.md <<'EOF'
---
docflow: 2
read_budget: 40000
repos: deps/*
---
## Queue
- P001

## Backlog
EOF
cat >docs/plans/P001-plan.md <<'EOF'
---
id: P001
title: Nested commit
type: feature
status: active
created: 2026-10-22
---
## Goal
Commit both repositories.

## Context

## Constraints

## Prerequisites

## Traps

## Decisions

## Phase 1: Commit

### [ ] P001.1.1 Nested commit
Read:
- tracked.txt
Do:
- edit both repositories
Verify: `true`
Commit: P001.1.1: nested commit
EOF
cat >.gitignore <<'EOF'
deps/module/
EOF
printf 'root base\n' >tracked.txt
git add -A
git commit -q -m 'Create root fixture'

git -C deps/module init -q -b main
git -C deps/module config user.name 'docflow parity'
git -C deps/module config user.email parity@example.invalid
printf 'nested base\n' >deps/module/tracked.txt
git -C deps/module add -A
git -C deps/module commit -q -m 'Create nested fixture'

docflow prompt P001.1.1
printf 'root edit\n' >tracked.txt
printf 'nested edit\n' >deps/module/tracked.txt
docflow finish P001.1.1 --handoff 'both repositories committed'
