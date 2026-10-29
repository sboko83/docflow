---
id: P001
title: Change the value
type: feature
status: active
created: 2026-10-29
---
## Goal

`tinybase.Value` returns 2.

## Context

Tiny fixture.

## Constraints

Keep the public function signature.

## Traps

None.

## Decisions

None.

## Phase 1: Value

### [ ] P001.1.1 Change the value
Read:
- value.go
Do:
- Change `Value` to return 2.
Verify: `go test ./...`
Commit: P001.1.1: change value
