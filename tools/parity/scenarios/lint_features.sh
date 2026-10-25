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
title: Plan
type: feature
status: active
created: 2026-10-22
permissions: full
executor: codex | gpt-5.5 | high
replan: pi | zai/glm-5.3 | high
---
## Goal
Goal.

## Context

## Constraints

## Prerequisites
- Go toolchain: `go version`
- Account is available

## Traps

## Decisions

## Phase 1: First

### [ ] P001.1.1 Unit
Read:
- fixture.txt
Do:
- edit the fixture
Verify: `true`
Commit: P001.1.1: update fixture
EOF

docflow lint
