---
id: P001
title: Autonomous
type: feature
status: draft
created: 2026-10-20
---
## Goal
Прогон от запуска до ready to close без человека.

## Context

## Constraints

## Traps

## Decisions

## Phase 1: Files

### [ ] P001.1.1 Write one
Read:
- a.txt
Do:
- write f1.txt
Verify: `test -f f1.txt`
Commit: P001.1.1: write one

### [ ] P001.1.2 Write two
Read:
- a.txt
Do:
- write f2.txt
Verify: `test -f f2.txt`
Commit: P001.1.2: write two

### [ ] P001.1.3 Write three
Read:
- c.txt
Do:
- write f3.txt
Verify: `test -f f3.txt`
Commit: P001.1.3: write three

