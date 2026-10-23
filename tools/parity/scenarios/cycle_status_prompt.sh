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
- P002
- P003

## Frozen
- P004 paused

## Backlog
EOF
cat >docs/plans/P001-plan.md <<'EOF'
---
id: P001
title: Blocked plan
type: feature
status: active
created: 2026-10-22
---
## Goal
Goal.

## Context

## Constraints

## Prerequisites

## Traps

## Decisions

## Phase 1: First

### [!] P001.1.1 Blocked unit
Read:
- fixture.txt
Do:
- change fixture
Verify: `true`
Commit: P001.1.1: blocked
EOF
cat >docs/plans/P002-plan.md <<'EOF'
---
id: P002
title: Draft plan
type: feature
status: draft
created: 2026-10-22
---
## Goal
Goal.

## Context

## Constraints

## Prerequisites

## Traps

## Decisions

## Phase 1: First

### [ ] P002.1.1 Draft unit
Read:
- fixture.txt
Do:
- change fixture
Verify: `true`
Commit: P002.1.1: draft
EOF
cat >docs/plans/P003-plan.md <<'EOF'
---
id: P003
title: Active plan
type: feature
status: active
created: 2026-10-22
---
## Goal
Goal.

## Context

## Constraints

## Prerequisites
- Runtime is available

## Traps

## Decisions

## Phase 1: First

### [ ] P003.1.1 Active unit
Read:
- fixture.txt
Do:
- change fixture
Verify: `true`
Commit: P003.1.1: active
EOF
cat >docs/plans/P004-plan.md <<'EOF'
---
id: P004
title: Frozen plan
type: feature
status: frozen
created: 2026-10-22
---
## Goal
Goal.

## Context

## Constraints

## Prerequisites

## Traps

## Decisions

## Phase 1: First

### [ ] P004.1.1 Frozen unit
Read:
- fixture.txt
Do:
- change fixture
Verify: `true`
Commit: P004.1.1: frozen
EOF
git add -A
git commit -q -m 'Create cycle plans'

docflow status
docflow next || [ "$?" -eq 3 ]
docflow prompt || [ "$?" -eq 3 ]
docflow prompt P002.1.1 || [ "$?" -eq 2 ]
docflow prompt P004.1.1 || [ "$?" -eq 2 ]
docflow prompt P003.1.1
docflow prompt P003.1.1
docflow next
