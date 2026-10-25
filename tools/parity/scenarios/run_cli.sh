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
title: Executor routes
type: feature
status: active
created: 2026-10-23
executor: claude | claude-opus-5-5 | high
---
## Goal
Проверить маршруты {model}.

## Context

## Constraints

## Prerequisites
- token: `echo no-token >&2; exit 3`

## Traps

## Decisions

## Phase 1: First

### [ ] P001.1.1 Inherits
Read:
- fixture.txt
Do:
- change fixture
Verify: `true`
Commit: P001.1.1: inherits

### [ ] P001.1.2 Overrides
Read:
- fixture.txt
Do:
- change fixture
Verify: `true`
Executor: codex | gpt-5.5 | -
Commit: P001.1.2: overrides
EOF
git add -A
git commit -q -m 'Create executor plan'

docflow run --dry-run --all
docflow run --dry-run
docflow run --print-config
docflow run --no-wait || [ "$?" -eq 2 ]
docflow run --stop || [ "$?" -eq 1 ]

# Legacy profiles and prompt-in-argv use the same byte count and shell quoting.
sed '/executor: /d; /Executor: /d' docs/plans/P001-plan.md >plan.tmp
mv plan.tmp docs/plans/P001-plan.md
cat >"$HOME/agents.toml" <<'EOF'
[tiers]
default = {agent = "x"}
[agents.x]
cmd = ["x", "{prompt}"]
EOF
docflow run --dry-run --config "$HOME/agents.toml" --model custom
