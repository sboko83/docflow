# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `docflow run` distinguishes subscription limits, request throttles, and
  provider outages, and retries each class on its own delay.
- Plans can define an ordered `fallback:` executor chain and a `timeout:`
  override for agent calls.

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

[Unreleased]: https://github.com/sboko83/docflow/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/sboko83/docflow/releases/tag/v0.7.0
