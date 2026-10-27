#!/bin/bash
# A whole-file Read over whole_read_limit warns with the file size and the
# Gotchas of the archive entries it links; a range or a raised limit does not.
parity_repo
mkdir -p docs/plans docs/archive

write_docflow() {
    {
        printf -- '---\ndocflow: 3\nread_budget: 40000\n'
        if [ -n "$1" ]; then printf 'whole_read_limit: %s\n' "$1"; fi
        printf -- '---\n'
    } >docs/docflow.md
    cat >>docs/docflow.md <<EOF
## Queue
- P001

## Backlog
EOF
}

for id in P008 P009; do
    cat >"docs/archive/$id-trap.md" <<EOF
---
id: $id
title: Trap $id
closed: 2026-10-02
---
## Outcome
Done.

## Decisions
- decision — why — alternative

## Gotchas
- trap of $id: a reference to the whole file pulls this line into every prompt

## Follow-ups
- none
EOF
done

# 250 lines of 80 bytes plus the archive links: 20061 bytes, ~5015 tokens.
awk 'BEGIN { for (i = 0; i < 250; i++) printf "line %03d %s\n", i, "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" }' >big.md
echo 'See docs/archive/P008-trap.md and docs/archive/P009-trap.md.' >>big.md

write_plan() {
    cat >docs/plans/P001-plan.md <<EOF
---
id: P001
title: Plan
type: feature
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
- $1
Do:
- edit the file
Verify: \`true\`
Commit: P001.1.1: update file
EOF
}

# Each formatted Gotcha '- (P00N) trap …' is 86 bytes: 2 × 86 / 4 = 43 tokens.
warning='docs/plans/P001-plan.md:23: warning: big.md is ~5015 tokens whole (+43 tokens of archive gotchas); reference a range or #anchor'

write_docflow ''
write_plan big.md
docflow lint
grep -Fqx "$warning" "$parity_output/0001.stdout"

write_plan big.md:L1-3
docflow lint
if grep -Fq 'tokens whole' "$parity_output/0002.stdout" "$parity_output/0002.stderr"; then exit 1; fi

write_docflow 6000
write_plan big.md
docflow lint
if grep -Fq 'tokens whole' "$parity_output/0003.stdout" "$parity_output/0003.stderr"; then exit 1; fi
