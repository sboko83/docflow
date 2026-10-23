---
id: P007
title: Offline cache for feed
type: feature
status: draft
created: 2026-10-12
executor: claude | claude-sonnet-5-5 | medium
permissions: auto
replan: claude | claude-haiku-4-5 | low
---
## Goal
## Context
## Constraints
## Prerequisites
- Test account signed in on the simulator; the credentials live in
  `$FEED_TEST_ACCOUNT`, not in the repository
- The feed backend is reachable: `curl -fsS https://feed.example/health`
## Traps
## Decisions

## Phase 1: Storage layer

### [ ] P007.1.1 Add SQLDelight schema
Read:
- shared/src/commonMain/kotlin/feed/FeedRepository.kt:L1-80
Do:
- ...
Verify: `./gradlew :shared:allTests`
Traps: ...
Depends: -
Executor: codex | gpt-5.5 | -
Commit: P007.1.1: add feed cache schema
