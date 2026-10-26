# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Pi routes on `strata/…` display a light-grey `Local` badge with dark
  text. Local runs skip subscription quota probes and quota display.
- Live smoke and demo scripts accept `--dry-run` to inspect their plans
  without model calls.

### Changed
- Live planning, smoke and demo scripts use Pi/GLM Flash and Codex,
  preserving Claude quota. The planning check accepts `--model` for
  Z.ai and Strata routes.
- The `bsv-docflow-plan` skill ends every plan that changes a UI, CLI or
  API with an acceptance unit, a one-unit plan included; a review unit no
  longer stands in for it.

### Fixed
- The live planning check rejects failed or timed-out agent calls,
  cleans up its watchdog, and checks the requested default roster routes
  against the snapshot saved for that call.

## [0.13.0] - 2026-10-25

### Added
- Windows amd64 and arm64 release archives include `docflow.exe`.
  Release activation and personal skill links use directory junctions
  without requiring Developer Mode or administrator rights.
- Windows installation from Git Bash configures the user `Path`;
  `docflow update` and `docflow uninstall` work from the installed executable.

### Changed
- On Windows, plan commands (`Verify:`, `gate:`, `preflight:`, runner
  prerequisites) run through Git for Windows `bash -c` instead of `cmd.exe`,
  so plans written in POSIX sh work on every OS; without Git Bash they fall
  back to `cmd.exe`.

### Fixed
- On Windows, `docflow run` kills the whole agent process tree on a timeout
  or stop (`taskkill /T /F`), not only the agent process.
- `docflow run` reads the Claude result even when Claude Code writes events
  after it (`task_summary`); the unit no longer spends attempts on "no
  result JSON in the reply".
- `tools/smoke-run.sh` uses the plan format 3, portable `sed` and
  `docflow.exe` on Windows.
- On Windows, `install-skills` no longer fails with `user "/.docflow" does not
  exist`: `~/` paths split on `/` as well as ``.
- On Windows, `tools/install-dev.sh` builds and links `docflow.exe`, so
  `doctor` finds it on `PATH`; without symlink rights it fails instead of
  silently copying the binary.

## [0.12.0] - 2026-10-25

### Added
- `docflow run --close` asks an agent to archive a ready plan, checks the
  documentation changes and lint, and commits the closure. Dry runs show
  the close route; call logs and a `close` event record the operation.
- `docflow run` writes a versioned JSONL event stream beside its call logs,
  covering attempts, quota, route switches, replans, verification and commits;
  stream failures warn once without stopping the run.

### Changed
- `docflow prompt` and `docflow run` give review units the phase's commit
  subjects, diff base, and command to inspect the diff.
- `docflow install-skills` relinks personal skill links that still point
  into `~/.docflow/` when installing from another source; foreign paths remain conflicts.
- `docflow run` gives a fallback executor the previous attempt's diff
  summary, untracked paths, and recent assistant messages, excluding the refusal.

## [0.11.0] - 2026-10-25

### Changed
- `docflow run --replans N` limits auto-replans per plan phase instead of
  per run (default 3, still at most 2 per unit): phase review rounds no
  longer exhaust the budget of a long plan.
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
  `--profile NAME` applies a `[profile.NAME]` override of the spend list
  and provider flags; a profile may drop a provider, whose routes are then
  skipped. The runner never reads the roster.
- `docflow roster apply PLAN [--profile NAME] [--roster PATH]` writes the
  picked chains into a plan header in `docs/plans/` (`executor:`,
  `fallback:`, `replan:` and the role keys of the unit `Role:` fields;
  role keys without units are removed, other keys keep their place),
  lints the plan and restores it on lint errors; refuses while a run is
  in progress.
- The `bsv-docflow-plan` skill plans with the roster: the survey starts
  from `docflow roster show` (roster / profile / manual triples, with a
  fallback route per role on a manual choice), units get `Role:` by the
  cheap/heavy/review/acceptance criteria, a phase whose units run on a
  `check` provider ends with a review unit, and `docflow roster apply`
  runs after the units, before `lint` and the dry run.
- The `bsv-docflow-run` skill: a `Role: review` unit edits no files and
  blocks with `--needs plan` on a defect; in a manual session the agent
  fits when it is in the unit's route chain (its `Role:` chain, else
  `Executor:` or the header `executor:` with `fallback:`).

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

[Unreleased]: https://github.com/sboko83/docflow/compare/v0.13.0...HEAD
[0.13.0]: https://github.com/sboko83/docflow/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/sboko83/docflow/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/sboko83/docflow/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/sboko83/docflow/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/sboko83/docflow/compare/v0.8.1...v0.9.0
[0.8.1]: https://github.com/sboko83/docflow/compare/v0.8.0...v0.8.1
[0.8.0]: https://github.com/sboko83/docflow/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/sboko83/docflow/releases/tag/v0.7.0
