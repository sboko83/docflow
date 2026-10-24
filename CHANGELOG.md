# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- `docflow run` spells the Pi agent as `Pi` and paints its badge in the
  pi.dev logo colours; the documentation follows the same spelling.
- `docflow run` displays the quota provider name as `Z.ai` in terminal output.
- `docflow run` follows the role chain of a unit's `Role:` field on route
  refusals instead of the shared `fallback:`, and uses the `replan:` chain
  as is, without appending `fallback:`. `run --dry-run --all` marks role
  units with `role <name>` and prints the `fallback:`, role, and `replan:`
  chains after the unit routes.
- The missing-skill warning of `docflow run` covers the agents of every
  route in a unit's chain, including fallback routes and the whole
  `replan:` chain, not only the primary routes.

### Added
- Format 3: role route chains in the plan header (`cheap:`, `heavy:`,
  `review:`, `acceptance:`) and the unit field `Role:` that routes a unit
  through its role chain instead of the shared `fallback:`; `replan:` is a
  full route chain. `docflow migrate` moves a format 2 project to 3 and
  folds `fallback:` routes into the `replan:` chain of open plans.
- `lint` warns about single-route chains (`no fallback for <role>`) and
  about role keys that no unit uses.
- `docflow roster show` prints the route chains per plan role picked from
  the personal roster (`~/.config/docflow/roster.toml`): a spend summary
  line plus the plan-header `executor:`, `fallback:` and role-key lines;
  `--profile NAME` applies a `[profile.NAME]` override of the spend order
  and provider flags. The runner never reads the roster.

### Fixed
- `docflow run` shows the quota delta for a window that was at 0% before
  the unit: the first use opens the window with a new reset time, which
  used to hide the `(+N)`.

## [0.10.0] - 2026-10-24

### Added
- `docflow run` reports z.ai subscription quota for pi routes using `zai/...`
  models, skips exhausted routes, and uses available reset times for limit
  refusals.

### Fixed
- `docflow run` ignores blank strings in the agent's `handoff`,
  `decisions` and `unverified` lists instead of retrying the unit.
- The run skill no longer blocks a unit because the agent misreports its
  own model: in runner mode the assignment is trusted, and a manual
  session checks only the agent, not the model or effort.

## [0.9.0] - 2026-10-24

### Added
- Prerequisites can use `(once)` to run only before a plan starts.

## [0.8.1] - 2026-10-24

### Changed
- `docflow install` and `docflow update` print one line per personal skill
  directory instead of a line per skill, and suggest updating project skill
  copies only when the current project has stale ones.

### Fixed
- `docflow update` after a manual rollback to the previous version installs
  the latest release instead of failing with `release X already exists`.

## [0.8.0] - 2026-10-24

### Added
- `docflow run` distinguishes subscription limits, request throttles, and
  provider outages, and retries each class on its own delay.
- `docflow run` switches to the next `fallback:` executor on provider limits or
  outages, and waits only when every route is unavailable; plans can also set
  a `timeout:` override for agent calls.
- `docflow run` skips fallback and replan routes whose quota window is full
  until reset, and sends replan through the same fallback chain.

### Changed
- `docflow verify` keeps the complete output of each check in a per-unit log
  and prints its path on failure; `docflow run` reuses a successful agent
  verify while the tree is unchanged.

### Fixed
- pi's z.ai usage-limit errors retain their reset time instead of falling
  back to the generic profile wait.
- `docflow run` in a terminal keeps the output of a failed `prompt`,
  `verify` or `commit`, such as a stale `Read:` link, inside the unit's
  frame instead of printing it flush left.
- `docflow run` shows the starting quota of the plan's `replan:` agent too,
  not only of the agents assigned to the remaining units.
- `docflow run` stops detached agent descendants on timeout or interruption
  and keeps earlier call logs when a unit is run again.
- `docflow run` no longer hangs after an agent exits or is stopped while a
  detached background process it left behind keeps the agent's output open.

## [0.7.0] - 2026-10-23

First public release.

### Added
- `docflow` CLI, a single Go binary without dependencies: a queue and
  backlog in `docs/docflow.md`, plans of phases and one-session units in
  `docs/plans/`, and a decision archive in `docs/archive/` (document
  format `docflow: 2`; `docflow migrate` converts format 1).
- The unit cycle `prompt → work → finish` (verify → done → commit):
  `verify` fingerprints the working tree and `commit` commits exactly that
  tree; `lint` checks plan structure and the per-session read budget.
- `docflow run` drives a plan unattended through Claude Code, Codex CLI or
  pi, with a per-unit executor, model and effort, retries, usage-limit
  waits, timeouts, prerequisites, replanning of blocked units and a
  desktop notification at the end.
- Plans across nested repositories (`repos:`), frozen plans
  (`freeze`/`thaw`), and token statistics per unit (`stats`).
- Skills `bsv-docs`, `bsv-changelog`, `bsv-docflow-plan`, `bsv-docflow-run`
  and `bsv-docflow-close`; `docflow install-skills` links them for Claude
  Code, Codex and pi, and `docflow doctor` reports stale copies.
- One-line installer for macOS and Linux (amd64, arm64) from GitHub
  Releases with sha256 verification; `docflow update` and
  `docflow uninstall`.

[Unreleased]: https://github.com/sboko83/docflow/compare/v0.10.0...HEAD
[0.10.0]: https://github.com/sboko83/docflow/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/sboko83/docflow/compare/v0.8.1...v0.9.0
[0.8.1]: https://github.com/sboko83/docflow/compare/v0.8.0...v0.8.1
[0.8.0]: https://github.com/sboko83/docflow/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/sboko83/docflow/releases/tag/v0.7.0
