# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Windows PowerShell installer `install.ps1`
  (`irm https://raw.githubusercontent.com/sboko83/docflow/main/install.ps1 | iex`):
  the same download, checksum, and unpack flow as install.sh without Git
  Bash. Covered by CI on the Windows runner in both entry forms.

### Fixed
- Windows: stopping a timed-out or stalled agent no longer leaks its
  shell descendants. The call now runs inside a kill-on-close job object;
  the previous `taskkill /T /F` could not reach processes spawned through
  Git Bash (MSYS re-parents them), which kept pipes open and cost up to
  5 s per forced stop.
- Windows: the closing prompt hands the agent the absolute path of the
  running CLI, so a foreign `docflow` on PATH can no longer answer the
  close call.
- Log paths of call attempts (event stream and the progress line) use
  forward slashes on every OS.

## [0.17.0] - 2026-10-27

### Added
- Optional `whole_read_limit` key of `docs/docflow.md` (positive integer,
  tokens, default 4000): `lint` warns when a whole-file `Read:` item of an
  open unit or a `## Context` item of a plan with open units exceeds it,
  counting the archive gotchas the file pulls, and suggests a range or
  `#anchor` reference. `docs/design/runner.md` is split into H2 sections so
  such references can name a single topic.

### Fixed
- The read audit measures a `grep` run with `grep -l`/`-L`, `-c` or `-q` by
  its output - a file name, a count or nothing - instead of the full text of
  every file it names, which overstated extra reads (one 326-file `grep -l`
  added about 12k extra tokens).

## [0.16.1] - 2026-10-27

### Fixed
- `docflow install` and `docflow update` ask the skills question only when
  stderr is a terminal; with stderr redirected they take the `global`
  fallback instead of waiting for an answer, which hung `curl | sh` on the
  Windows CI runner.

## [0.16.0] - 2026-10-27

### Added
- The standalone install asks once how skills are installed: `global` links
  them into the personal agent directories, `project` keeps those directories
  clean and leaves the skills to project copies. The answer is stored in
  `~/.docflow/skills-mode` and `docflow update` follows it; `docflow update
  --skills global|project` switches the mode and applies it to the links even
  when the release is up to date, and `DOCFLOW_SKILLS=global|project` answers
  the question for `install.sh`. Without a terminal `global` applies and
  nothing is stored, so scripted installs keep today's behavior.

### Fixed
- On a Windows console the installed CLI now shows the new-release notice;
  terminal detection there always reported no terminal before.
- Quota windows line up in columns across agents in the `docflow run`
  frame: a window without a reset time no longer shifts the next one left.

## [0.15.1] - 2026-10-27

### Changed
- The `docflow run` start frame shows the plan title on the `run:` line,
  labels its timestamp `Start at`, ends with `Go! Go! Go!`, and separates
  quota reset icons from times.

### Fixed
- The `docflow run` spinner's age now counts from the last agent turn instead
  of resetting on every stream event.

## [0.15.0] - 2026-10-27

### Added
- `docflow run` tells a working agent from a hung one: an agent silent for
  the profile's new `idle_timeout` seconds (built-in profiles 1200; 0 turns
  the check off, range 5–86400) with no tool running is stopped as stalled
  and the attempt is retried on the same session with a note about the stall.
- A time cap reached while the agent is active (its last event is under 60 s
  old, or a tool is running) is not a failure: the run prints
  `↻ attempt N: time cap <cap> reached while active (<turns> turns); resuming
  the session`, names the tool in flight where there is one, and the run
  summary adds `hint: <id> needs more than <cap> on <route>: split the unit
  or set timeout: in the plan header`. A closing call that reaches the cap
  while active reads the same way — `↻ close attempt N: …` — and resumes the
  session, names its running tool and suggests setting the plan's timeout;
  a silent closing call keeps the `close agent timed out` problem line.
- The TTY spinner shows what the agent is doing:
  `<id> <elapsed> · turn <n> · <age> ago`, plus `· tool <duration>` while a
  tool runs. The call log `<git-dir>/docflow/run/<unit>.<attempt>.out` is
  written while the call runs, so `tail -f` follows the agent.
- The quota display is configurable through the new `[display]` table of the
  agents config: `quota = "used" | "left"` — `left` shows the remainder of the
  window (percent, bar, delta and colours all mirror) — and
  `resets = true | false`, which hides the window reset time. An unknown key
  or value is a config error (exit 2).

### Fixed
- The spinner and cap report count agent turns per Claude message, not per
  stream event, so `turn <n>` no longer runs ahead of the real turn count.
- A call the runner stopped is not a provider failure: an attempt killed for
  silence, whose output quotes a limit or a login error, no longer marks the
  route unavailable, does not send the run to a fallback route and does not
  stop the run for a login — it is retried as a stalled attempt on the same
  route.
- `docflow init` replaces the legacy AGENTS.md plan marker instead of
  appending a second one; when both versions exist, it removes the old one.

### Changed
- The built-in `claude`, `codex` and `pi` profiles raise the per-call cap from
  3600 to 7200 seconds: a unit that works the whole hour is cut off while it
  is still making progress. The plan header's `timeout:` keeps overriding it.
- A unit and replan block names its own span: the header starts with the
  block's start time (`┌─ HH:MM · <id>: <title>`), the quota rows sit inside
  the frame, and the block ends with `└─ HH:MM · <duration>` in place of the
  `Time: <duration>` row. Text output prints
  `<id> time: <duration> (HH:MM:SS–HH:MM:SS)` after the per-unit `quota:` row.
- Quota rows colour by 50% and 80% of the shown share (was 75% and 90%), and
  the window reset time sits inline after the percent (`↻HH:MM`, `↻Mon HH:MM`)
  instead of a separate row that only `--verbose` showed.
- The call log ends with `--- exit N` carrying a ` (timed out)` or
  ` (stalled)` suffix instead of starting with that line: `$ argv`,
  `--- stdout` streamed line by line, `--- stderr`, exit. The file is
  complete only when the call is.
- `docflow init` writes the AGENTS.md marker as
  `Plans: docflow (run docflow status), only on an explicit request; not
  the bsv-docs PLAN genre`.
- `bsv-docflow-plan` asks one question — "make a plan or do it now?" —
  before any plan file, `docflow note`, `activate` or `close`, whatever the
  task's size; `docflow note` is written only on a request, and after work
  without a plan the reply offers it when a design decision changed. A
  unit's `Verify:` must cover its `Do:`: behavior a command cannot observe
  (a screen, an animation, a device flow) adds a UI/e2e test to `Verify:`
  or a live-check unit right after the first such unit of the phase; a
  phase run by a `check` provider holds at most 4 units before its review
  unit.
- `bsv-docflow-close` triages every open `Unverified:` item at closing:
  verify it now, accept it in the entry as
  `Accepted: <item> — <why it is not a risk>` like `Waits:`, or move it to
  the Backlog only when it is a Goal gap or a risk with a concrete failing
  scenario; unfinished Goal work always goes to the Backlog. Acceptance
  ends each inherited item the same three ways.
- Review observations without a defect are plain Handoff items, not
  `Unverified:`: the reviewer changes no code.
- A unit's `unverified` names a risk of the unit's own change that its
  `Verify` does not cover: not an item already open in a `Depends` unit's
  Handoff, not an idea to improve tests, not a check of code the unit did not
  change; an unfinished `Do:` step is finished or blocked, never listed
  there. The runner prompt and the `bsv-docflow-run` skill say so, and a
  prompt with `## Handoff from Depends` adds that those items stay open on
  their own units.
- `docflow run` drops a result `unverified` item that repeats an
  `Unverified:` item in the Handoff of a direct `Depends` unit; the
  comparison ignores case, spacing and trailing punctuation, and cuts one
  trailing `( … )` tail only when it marks inheritance — it starts with
  `inherited` or names a unit id, so a tail with the item's own detail
  (`layout on iPhone (small)` vs `(large)`) keeps the items distinct. The
  plan Handoff, the unit's count and the run summary carry only the kept
  items, and `--verbose` prints
  `unverified: N repeated from Depends dropped`.

## [0.14.0] - 2026-10-26

### Added
- `docflow unblock <id> --note "…"` resumes a unit blocked for a human:
  the unit goes back to `[~]` and the note becomes an `Unblocked:` Handoff
  item. A block raised with `--needs plan` is refused with a hint to
  replan; the command does not commit. The block is read only from the
  exact Handoff forms `Blocked (human): …`, `Blocked (plan): …` and the
  legacy `Blocked: …`, so a lookalike item cannot hide a `plan` block.
- `docflow run` prints `unblock: docflow unblock <id> --note "…", then
  docflow run` wherever it stops on a human block. When a run starts on such
  a unit in a terminal without `--yes`, it prints the reason and asks
  `done? [y/N] `, then takes a one-line note: `y` lifts the block and the run
  continues with the usual start; any other answer, `--yes` or no terminal
  stop the run with code 3 and the command. A Windows console counts as a
  terminal; redirected input does not. Ctrl-C at either prompt ends the run
  with code 130 and leaves the unit blocked.
- Pi routes on `strata/…` display a light-grey `Local` badge with dark
  text. Local runs skip subscription quota probes and quota display.
- Live smoke and demo scripts accept `--dry-run` to inspect their plans
  without model calls.

### Changed
- The cycle text in every unit prompt — `## Cycle` and `## Cycle (runner
  mode)` — tells the agent how to treat a resumed unit: when its Handoff has
  `Unblocked:` after the latest `Blocked (human)` — a legacy `Blocked:` item
  without a class counts as human — it checks that condition
  with a command before the rest of `Do:`; not met — block again with
  `human`, naming what is still missing. The `bsv-docflow-run` skill names
  the command that lifts each block; `bsv-docflow-plan` puts a human-only
  step at the head of the unit it gates.
- `docflow status` names who lifts a block: the `blocked:` line ends with
  `(needs human)` and is followed by a line with the
  `docflow unblock <id> --note "…"` command, or ends with `(needs plan)`.
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

[Unreleased]: https://github.com/sboko83/docflow/compare/v0.17.0...HEAD
[0.17.0]: https://github.com/sboko83/docflow/compare/v0.16.1...v0.17.0
[0.16.1]: https://github.com/sboko83/docflow/compare/v0.16.0...v0.16.1
[0.16.0]: https://github.com/sboko83/docflow/compare/v0.15.1...v0.16.0
[0.15.1]: https://github.com/sboko83/docflow/compare/v0.15.0...v0.15.1
[0.15.0]: https://github.com/sboko83/docflow/compare/v0.14.0...v0.15.0
[0.14.0]: https://github.com/sboko83/docflow/compare/v0.13.0...v0.14.0
[0.13.0]: https://github.com/sboko83/docflow/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/sboko83/docflow/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/sboko83/docflow/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/sboko83/docflow/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/sboko83/docflow/compare/v0.8.1...v0.9.0
[0.8.1]: https://github.com/sboko83/docflow/compare/v0.8.0...v0.8.1
[0.8.0]: https://github.com/sboko83/docflow/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/sboko83/docflow/releases/tag/v0.7.0
