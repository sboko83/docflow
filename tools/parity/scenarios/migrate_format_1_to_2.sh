#!/bin/bash
parity_repo
mkdir -p docs/plans
cat >docs/docflow.md <<'EOF'
---
docflow: 1
read_budget: 40000
---
## Queue
- P001

## Backlog
EOF
cat >docs/plans/P001-legacy.md <<'EOF'
---
id: P001
title: Legacy plan
type: feature
status: draft
created: 2026-10-22
---
## Goal

## Context

## Constraints

## Traps

## Decisions
EOF

docflow migrate
peer_docflow migrate || [ "$?" -eq 1 ]
