#!/bin/bash
parity_repo
mkdir -p docs/plans deps/module
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
title: Verify parity
type: feature
status: active
created: 2026-10-22
---
## Goal
Cross-binary verification.

## Context

## Constraints

## Prerequisites

## Traps

## Decisions

## Phase 1: First

### [~] P001.1.1 Verify and finish
Read:
- tracked.txt
Do:
- edit the tree
Verify: `true`
Commit: P001.1.1: verify and finish

### [ ] P001.1.2 Block
Read:
- tracked.txt
Do:
- inspect the fixture
Verify: `true`
Commit: P001.1.2: block
EOF
cat >.gitignore <<'EOF'
deps/module/
EOF
printf 'tracked base\n' >tracked.txt
git add -A
git commit -q -m 'Create verify parity fixture'

git -C deps/module init -q -b main
git -C deps/module config user.name 'docflow parity'
git -C deps/module config user.email parity@example.invalid
printf 'nested base\n' >deps/module/tracked.txt
git -C deps/module add -A
git -C deps/module commit -q -m 'Create nested fixture'

printf 'tracked edit\n' >tracked.txt
printf '\000\001\377\002' >untracked.bin
ln -s tracked.txt tracked-link
printf 'nested edit\n' >deps/module/tracked.txt
printf 'nested untracked\n' >deps/module/untracked.txt

docflow verify P001.1.1
peer_docflow done P001.1.1 --handoff 'verified by the other binary'
docflow block P001.1.2 --reason 'needs a plan update'
