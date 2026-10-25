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

write_plan() {
    cat >docs/plans/P001-plan.md <<EOF
---
id: P001
title: Plan
type: $1
status: active
created: 2026-10-22
---
## Goal
Goal.

## Context

## Constraints

## Traps

## Decisions

## Phase 1: First

### [ ] P001.1.1 Unit
Read:
- fixture.txt
Do:
- edit the fixture
Verify: \`true\`
Commit: P001.1.1: update fixture
EOF
}

write_plan feature
docflow lint
write_plan unknown
docflow lint || [ "$?" -eq 2 ]
