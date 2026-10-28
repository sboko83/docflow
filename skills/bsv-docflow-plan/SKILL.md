---
name: "bsv-docflow-plan"
description: "Turns a task into a docflow plan — phases and one-agent-session units with Read, Do, Verify, Traps, Commit — and replans a plan that has a blocked [!] unit or a stale Read: link. Use in a project with a docs/docflow.md file, when the user explicitly asks to compose or break down a plan or to take a Backlog line into work, or when docflow status reports a block, or docflow run stopped with a replan hint. A request to simply do the task is not a trigger: first ask whether a plan is needed. Does not activate a plan — activation is docflow activate on a human request. Not for projects without docs/docflow.md — there the PLAN genre from bsv-docs applies. Not for running units and not for closing a plan."
---

# docflow planning

A plan is written for a cold agent that never saw the chat: every unit is
self-sufficient — what to read, what to do, how to verify, which traps
exist, what message to commit with.

General documentation genres and rules are in the standalone `bsv-docs`.
This skill defines only docflow planning and statuses; the CHANGELOG, when
needed, is kept per `bsv-changelog`.

## When

- `docs/docflow.md` exists — otherwise this is not a docflow project and
  the skill does not apply.
- The user explicitly asks for a plan: "make a plan", "split it into
  units", "take into work" a Backlog line.
- A unit is in `[!]` status — replan mode, see below.

A request to just do a task, with no mention of a plan, is not a trigger.
Before any plan file, `docflow note`, `docflow activate` or `docflow
close`, ask the user one question — "make a plan or do it now?" —
whatever the task's size; never decide it for the user. "Do it now" →
work without a plan, create no plan files.

## Procedure

1. Read `docs/docflow.md`, the project's architecture (the directory is an
   index — read only the topics needed), and only the code without which
   the task cannot be cut apart. Do not read "for general understanding":
   every line of context is paid for until the end of the session.
2. Size estimate. The task fits one unit (criteria in "Unit") → ask the
   user: "do it without a plan?". Yes → do the work, run the project's
   checks, commit per the project's rules; no `docflow note` unless the
   user asks — when the work changed a design decision, offer the note in
   the reply. The CHANGELOG — per `bsv-changelog`. No → continue.
3. Skeleton: `docflow new <slug>`. No script in PATH — do it manually:
   id = max over `docs/plans/` and `docs/archive/` + 1, three digits
   (`P001`); file `docs/plans/Pxxx-slug.md` after the sample below; id goes
   to the end of the Queue.
4. Plan header:
   - `## Goal` — 1–3 lines: what will hold once the plan is closed.
   - `## Context` — docs and files every unit needs. What one unit needs
     goes into that unit's `Read:`, not here.
   - `## Constraints` — the frame: stack, compatibility, what not to touch.
   - `## Traps` — traps of the whole plan. A single unit's trap goes into
     its `Traps:`.
   - `## Decisions` — empty, or decisions made while planning:
     `decision — why — the rejected alternative`.
5. Phases: a group of units with a shared intermediate result. Phase N+1
   waits for phase N.
6. Survey — see "Ask before units": one block of questions to the user,
   before any unit is written — the plan's executor and separate ones for
   heavy units and acceptance, `permissions: auto|full` with what `full`
   removes, the auto-replan executor or off, and what only a human
   provides (the `## Prerequisites` items).
7. Units — see "Unit". The order of units is the order of execution.
   When the plan changes what the user sees (UI, CLI, API), the last phase
   is "Acceptance", see below.
8. Cold-agent check: reread every unit as if the chat never happened.
   Any "as we discussed", "see above", a reference to an unnamed file —
   rewrite.
9. Routes: on the roster choice of the survey — `docflow roster apply
   <id>` (plus `--profile NAME` when chosen) writes the role chains into
   the plan header; manual triples — the header keys by hand. When the
   user did not opt in to auto-replan, delete the `replan:` line apply
   wrote — after every repeated apply too.
10. `docflow lint`. No script — check against the sample manually.
11. Show the plan to the user, apply edits. Leave `status: draft`; the
   project's rules and the commission decide the commit and its message.
   If a commit is due, delete the Backlog line taken into work in the same
   commit. The last line of the reply is "activate:
   `docflow activate Pxxx`"; see "Activation".

## Ask before units

One block of questions to the user in every plan, before units are
written — not drip-fed, nothing assigned on the user's behalf. A choice
the user has already stated is recorded mechanically and not asked again.

- Executors: first run `docflow roster show`. It succeeds — ask which
  source fills the routes: the roster (the chains it shows) / a roster
  `--profile` / manual triples. Manual selection of triples — only on
  that answer or on a roster error, and then ask for a fallback route of
  every role too: a plan without a fallback — only on the user's
  explicit refusal. The rules are in "Executor".
- Permissions: `permissions: auto|full` in the frontmatter. `full` runs
  the agents without their sandbox and approvals for the whole run; the
  plan file itself runs nothing, but the runner asks one
  `continue? [y/N]` at the start on `full` and on prerequisites without
  commands. No key — `auto`, the default mode. Name the mode in the
  survey anyway: a committed `full` must not be a silent escalation by
  commit.
- Auto-replan: `replan: agent | model | effort` in the frontmatter — the
  executor that fixes a plan-class block or a stale `Read:` link and lets
  the run continue (see "Replan under the runner"). Without the key a
  block stops the run. Off unless the user opts in; executor mode only,
  `lint` enforces it. `roster apply` writes `replan:`; a refusal removes
  it.
- What only a human provides: a login, a test account — name where the
  credentials live (an environment variable, a file outside the
  repository), never their value — a device or simulator step, a secret,
  external access. Each becomes a `## Prerequisites` item, the optional
  section between `## Constraints` and `## Traps`. When the machine can
  check it, the item carries a command after a colon, in backticks — the
  runner runs the commands at the start of the run, before quota and
  agents, a failure stops with code 2. Add `(once)` at the end of a check
  of code state that the plan itself temporarily changes; the runner checks
  it before the first unit and skips it when resuming a started plan. Other
  conditions, such as access, devices, and tools, are checked on every run.
  An item without a command is shown in the same start confirmation. A
  scenario needing what only a human provides is a prerequisite, never a
  `Do:` step. When that step only makes sense after the plan's earlier
  work — installing a build on a device, clicking through a fresh release —
  it opens the unit it gates: the step comes first in that unit's `Do:`,
  before its long work, so the `--needs human` block arrives before the
  cost of the unit. The person lifts it with `docflow unblock <id> --note
  "…"` (`bsv-docflow-run` re-checks the condition on the resumed unit).

## Unit

- One session, a ~100k context budget: 5–8 files to change, one `Verify`,
  `Read:` fitting the `read_budget` from `docflow.md` (bytes / 4).
- A plan whose units together fit ~100k of context is one unit. A unit
  shorter than half an hour of agent work is too small: merge until the
  result fits the budget. "One bug — one unit" and "one file — one unit"
  are not reasons to cut. Calibrate with `docflow stats`.
- `Read:` — exact paths: `path`, `path:L10-80`, `path#anchor`. Not
  directories, not "the whole module". A file an earlier unit will create
  is allowed, but check that the earlier unit really creates it. A file
  an earlier unit deletes or renames is never referenced — name the new
  path: a stale link stops the unit with code 2. In a
  project with nested repositories (`repos:` key in the frontmatter) —
  paths from the project root, not markdown links like `[t](rel)`.
- `Do:` — steps in the imperative, one action per item. Decisions that
  need choosing are made now; when the choice depends on what the executor
  will see, name the criterion. Git operations on branches are not a `Do:`
  step: a unit does not commit and does not rewrite history (`rebase`,
  `amend`, `reset` stop the runner with code 5). When units need code from
  another branch, merge it now, at planning time, before `docflow run`.
  Moving a finished branch into `main` happens after `docflow close`.
- `Verify:` — one command in backticks that fails when the unit is not
  done. For text (a skill, a doc) — `docflow lint`, `grep -q`, a test. Not
  "check manually". A grep over `docflow status` output or over the plan
  text needs an anchor (`grep -q '^P003 '`): without it the id matches the
  unit's own heading. In a project with nested repositories the check may
  start with `cd <repo> &&` to run commands in the subproject.
  "Verify covers Do": when `Do:` changes behavior the `Verify` command
  cannot observe (a screen, an animation, a device flow), add a UI or e2e
  test to `Verify:`, or schedule a live-check unit right after the first
  such unit of the phase — not only in the final acceptance.
- `gate:` in the plan frontmatter — the cheapest full run that catches
  regressions between units (application or module unit tests, not UI):
  `verify` runs it after each unit's `Verify`. Set it when units touch
  shared code or state; the UI suite belongs in acceptance.
- `Traps:` — what an agent without context will stumble on: a non-obvious
  order, a lookalike name, a format trap. Nothing to say — no field. In a
  project with nested repositories — do not write about committing
  manually into the nested repo; `docflow commit` covers every repo from
  `repos:`.
- `Depends:` — by default the previous unit of the phase; the field is not
  written. `-` — independent. Otherwise comma-separated ids, from this
  plan only.
- `Executor:` — `agent | model | effort`, the unit's full triple; it
  replaces the plan's triple whole, there is no partial inheritance.
  Only for the user's explicit choice. See "Executor".
- `Role: cheap|heavy|review|acceptance` — the unit runs along its role's
  chain from the plan header instead of the shared `fallback:` (format
  3; `docflow roster apply` writes the keys). Criteria: `cheap` —
  mechanics, docs, renames, config; `heavy` — design across several
  modules, concurrency, format and state; `acceptance` and `review` —
  see "Acceptance" and "Review". An ordinary unit has no field.
- `Model:` — legacy, only in a plan without `executor:`/`Executor:`: the
  model tier for the runner: `fast` — mechanics (docs, renames, config),
  `strong` — design work and tricky logic, `max` — the heaviest units
  (design across several modules, concurrency, acceptance of a large
  plan). An ordinary unit has no field.
- `Commit:` — the mandatory docflow prefix `Pxxx.N.M:`, then the message
  per the project's rules and the user's commission. Give the line
  exactly, so that `docflow commit` can run the task.
- `Handoff:` is not written — `docflow done` fills it.

A unit that is too large is cut by layer (model → logic → UI), by team, or
by feature; not by files, unless the files are verified separately.

## Executor

The plan itself defines what runs it; neither a TOML config, nor
`DOCFLOW_AGENTS`, nor `--agent/--model/--effort` flags are needed for
that, and none are created.

- The plan's shared triple is `executor: agent | model | effort` in the
  frontmatter; a unit with a different executor gets `Executor: agent |
  model | effort` before `Commit:`. A unit's triple replaces the shared
  one whole.
- `agent` is `claude`, `codex` or `pi`; `model` is a full name without spaces
  and `|`; `effort` is a single word or `-` (do not pass an effort
  explicitly; for `pi` it is the `--thinking` level, `off`…`max`). There are no shorthands or tiers.
- At least one `executor:`/`Executor:` switches the mode on for all
  unfinished units: each gets a full triple of its own or from the header,
  and `Model:` is not allowed in them. Finished and skipped units stay as
  they were. Not a single `Executor` — legacy: `Model:` and the user
  config's tiers.
- The choice of agent, model, or effort belongs to the user: it is asked
  in the survey of "Ask before units", in the same block as permissions,
  replan, and prerequisites. Do not assign them on the user's behalf. A
  choice already stated is recorded mechanically (`executor:`/`Executor:`),
  not only in prose inside `## Constraints`, and is not asked again.
- An assignment is not ready until `docflow` has checked it: the text of
  an instruction proves nothing. Model availability in the account is not
  checked by `lint`.

Before handing the plan over:
1. `docflow lint` without errors.
2. `docflow run --dry-run --plan <id> --all` — one line per unit over the
   whole plan, draft plans included.
3. Check every line against the chosen agents, models, and efforts. Fix
   mismatches before reporting readiness. A requirement the format cannot
   express is named as a constraint explicitly, not written down as met.

## Acceptance

A unit's `Verify` checks its slice, and its test is written by the same
agent that wrote the code. Nobody checks the Goal as a whole unless an
acceptance pass is set up.

A plan that changes what the user sees (UI, CLI, API) always ends with
acceptance, even when it has one ordinary unit: a `Role: review` unit
does not replace it.

- A separate unit (or phase), last in the plan, `Role: acceptance`: it
  runs along the header `acceptance:` chain, which `roster apply` or the
  manual choice writes. `Executor:` instead — only on the user's explicit
  choice, as in "Executor". A plan without `executor:`/`Executor:` —
  `Model: strong`.
- `Depends:` — every unit of the plan: this pulls their Handoffs with
  `Unverified:` items into the acceptance prompt.
- `Read:` — the existing e2e/UI tests and fixtures; from the
  implementation — only the identifiers the tests need: acceptance checks
  behavior.
- `Do:` — one scenario per Goal statement: an e2e/UI test (XCUITest,
  Espresso, a CLI run) that walks the user's path on real data or a
  fixture of the same shape (the same payload format, not a convenient
  stand-in). No manual steps and no driving the UI from a desktop: the
  user's path is XCUITest, Espresso, or CLI. A scenario needing what only
  a human provides is a `## Prerequisites` item, not a `Do:` step. Every
  `Unverified:` item of earlier Handoffs ends one of three ways: closed by
  a test; `Accepted: <item> — <why it is not a risk>`; `Unverified:` only
  when it is a Goal gap or a risk with a concrete failing scenario.
- Every `## Test changes` item of the prompt and every
  `Replan changed check:` line of `## Decisions` is a check that shrank:
  each one ends as an `Unverified:` item — closed by a test,
  `Accepted: <item> — <why it is not a risk>`, or left `Unverified:`.
- `Verify:` — the full test run of the application or module, not
  `-only-testing` of one suite.
- In `Traps:` — a failing scenario is fixed inside this same unit when the
  fix is small; otherwise — `blocked` with the reason.

## Review

A provider with `check = true` in the roster does not verify its own
work: every phase whose units run on that provider (their primary
routes — visible in `docflow roster show`) ends with a review unit.

- One unit at the end of the phase, `Role: review` — its chain holds no
  `check` provider.
- A phase run by a `check` provider holds at most 4 units before its
  review unit; a longer stretch splits into phases.
- `Do:` template — review the diff named in the prompt's `## Phase diff`
  against `Goal`, `Constraints`, `## Decisions`, and each unit's `Do:`.
  The reviewer edits nothing.
- The prompt's `## Test changes` section lists the test edits of the
  phase's commits — a test file that lost lines, a deleted test file, an
  added skip marker. Every item is taken apart: justified by the diff and
  the unit's `Do:` — it passes; a test weakened to make `Verify:` pass —
  a defect, `blocked` with `blocker: "plan"`. `none` means the phase
  touched no tests.
- A defect — `blocked` with `blocker: "plan"` (outside the runner,
  `docflow block --needs plan`): the plan's `replan:` schedules the fix.
  Observations without a defect — a plain Handoff item, not `Unverified:`:
  the reviewer changed no code, so they are not unverified risks.
- `Verify:` — the plan's `gate:`.

## Sample

````markdown
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
````

- `type`: `feature|bugfix|perf|module|refactor`. A bug's repro goes into
  `## Context` as prose or a code block; `- ` items in `## Context` are
  file links only, otherwise `lint` errors.
- The set and order of H2 headings is fixed for every type;
  `## Prerequisites` is optional, between `## Constraints` and `## Traps`.
- `permissions:`, `replan:`, and `## Prerequisites` are optional; without
  them the run is `auto`, a plan-class block stops the run, and the start
  has nothing to confirm. The survey of "Ask before units" decides them.
- Unit statuses: `[ ]` todo, `[~]` in progress, `[x]` done, `[-]` skipped,
  `[!]` blocked. The script sets them, not the skill (the exception is
  replan).

## Replan

A unit is `[!]`, or `docflow run`/`prompt` rejected a unit with code 2 (a
file from `Read:` not found — an earlier unit renamed or deleted it): the
plan has drifted from reality.

1. Read the block reason or the `prompt` error, the Handoffs of the done
   units (the file's new path is usually there), `## Decisions`.
2. Edit only `[ ]` and `[!]` units. Do not touch `[x]` and `[-]` units —
   they are history.
3. New units take the next numbers of the phase; do not renumber the
   existing ones: `Depends` and commits refer to them. What replan does
   not change: the checks of the reference units — `Verify:`, `Do:`,
   `Role:` of a `Role: review`/`Role: acceptance` unit — and their move
   into `[-]`; their status marker and `Depends:` do change, replan
   lifts the block and adds the fix units. No open unit becomes `[x]`:
   the runner skips a `[x]` unit and its check disappears silently.
   `Verify:` of an ordinary unit changes freely — `docflow` records that
   in `## Decisions` itself, the agent records nothing there.
4. Lift the block:
   - the blocked unit has uncommitted work in the tree, and after the
     edit that work still stands → `[!]` → `[~]`: a repeated `prompt` of a
     `[~]` unit does not check the tree, the work continues;
   - the work is no longer needed → ask the user what to do with it; do
     not roll it back on your own;
   - there is no work → `[ ]`;
   - the unit is no longer needed → `[-]`.
   A `[ ]` unit with a stale link keeps its status: fix the links in all
   `[ ]` units.
5. `docflow lint`, show the changes to the user. The commit and its
   message follow the project's rules and the user's commission.

### Replan under the runner

Units driven by `docflow run` (the `## Cycle (runner mode)` prompt)
replan automatically: a `blocked` reply with `blocker: "plan"`, or a
`prompt` rejection on a stale `Read:` link (code 2), is handed to the
plan's `replan:` executor — the procedure above, no human in the loop —
and the run continues when it succeeds. The replan agent's part:

- Edit only the plan file, by the rules above: units `[ ]` and `[!]`,
  new units with the next phase numbers, `Read:`/`## Context` links,
  `Traps:`, an append to `## Decisions`. Never the frontmatter, `Goal`,
  `Constraints`, or `Prerequisites`, never a `[x]`/`[-]` unit, no unit
  id disappears, no renumbering.
- The reference checks are untouchable: `Verify:`, `Do:`, `Role:` of the
  `Role: review`/`Role: acceptance` units stay as they are and those
  units never move to `[-]` — their status marker and `Depends:` do
  change. An open unit never becomes `[x]`. A `Verify:` change and a
  move into `[-]` on an ordinary unit are allowed and need no record from
  the agent: `docflow` writes `Replan changed check: <id> Verify: <old>
  -> <new>` (and `<id> skipped`) into `## Decisions`, prints it in the
  run, counts it in the summary.
- Lift the block by step 4 above: the prompt lists the uncommitted
  files under `## Tree`; `[ ]` over uncommitted work is rejected, since
  the next `prompt` refuses a dirty tree. Every `Read:`/Context link of
  the unit must exist after the edit.
- Do not do the unit's work, touch no other file, do not commit: the
  runner checks the diff, itself commits `Pxxx: replan Pxxx.N.M`, and
  re-prompts the unit. A broken rule or an unchanged plan — the plan is
  restored, the run stops.
- End the reply with the result JSON; `handoff` — one item per plan
  change.
- The plan cannot be fixed without a person — a login, a device, a
  decision: reply `blocked` with `blocker: "human"` and the concrete
  what-to-do in `reason`; the runner stops the run and notifies.

## Activation

A plan becomes `active` only by the `docflow activate <id>` command: a
clean lint of the plan → `status: active`, the id into the Queue (if it
was not there), the hint `commit: Pxxx: activate plan`. Lint errors —
code 2, the plan stays `draft`. No script in PATH — the human sets
`status: active` by hand.

A human activates. The agent runs `activate` only on a direct request
("activate Pxxx"); if a commit is due after that, it is made per the
project's rules and the commission. After activation do not take up a
unit: execution is `bsv-docflow-run` on a separate request.

## Freezing a plan

A plan can be shelved without finishing it, when it is still needed:
`docflow freeze <id> --reason "…"`. A frozen plan leaves the Queue and
`docflow status`; the file stays. To return it to the queue —
`docflow thaw <id>`. This is the alternative to dropping a plan (`close`)
when the work is paused for a time. When these changes need a commit, its
format and execution are set by the project's rules and the commission.

The commands `activate`, `freeze`, and `thaw` do not commit on their own.

## What not to do

- Do not create a plan without an explicit request for one.
- Do not write `docflow note` without a request.
- Do not set `status: active` by hand and do not run `docflow activate`
  without a direct request; do not offer "activate and start" as one
  question.
- Do not restate the Goal in other words inside `Do:`.
- Do not leave references to the chat inside a unit.
- Do not plan "explore and decide" without a criterion — that is not a
  unit, it is a question to the user before planning.
- Do not duplicate decisions from the architecture — link them in `Read:`.
