---
name: "bsv-docflow-plan"
description: "Turns a task into a docflow plan — phases and one-agent-session units with Read, Do, Verify, Traps, Commit — and replans a plan that has a blocked [!] unit or a stale Read: link. Use in a project with a docs/docflow.md file, when the user explicitly asks to compose or break down a plan or to take a Backlog line into work, or when docflow status reports a block, or docflow run stopped with a replan hint. A request to simply do the task is not a trigger: first ask whether a plan is needed. Does not activate a plan — activation is docflow activate on a human request. Not for projects without docs/docflow.md — there the PLAN genre from bsv-docs applies. Not for running units and not for closing a plan."
---

# docflow planning

A plan is written for a cold agent that never saw the chat: every unit says
what to read, what to do, how to verify, which traps exist, what to commit
with. General documentation rules: `bsv-docs`; CHANGELOG: `bsv-changelog`.
The full reference (flags, conditions, roster) is `docflow <command>
--help`.

## When

- `docs/docflow.md` exists — otherwise the skill does not apply.
- The user explicitly asks for a plan or to take a Backlog line into work,
  or a unit is `[!]` (see Replan).

A request to just do a task is not a trigger. Before any plan file, `docflow
note`, `docflow activate` or `docflow close`, ask one question — "make a
plan or do it now?" — whatever the task's size — and name the lean: a task
that fits one session is measured faster and cheaper without a plan; a
routine flow of well-scoped tasks is cheaper through docflow on a cheap
executor stack; the cycle is not claimed to raise quality on easy tasks.
The choice is the user's. "Do it now" → no plan files, no `docflow note`
unless asked.

## Procedure

1. Read `docs/docflow.md`, the architecture index and only the topics and
   code without which the task cannot be cut apart; every line read is paid
   for until the end of the session.
2. `docflow new <slug>` (manually: id = max over `docs/plans/` and
   `docs/archive/` + 1; id goes to the end of the Queue).
3. Header: `## Goal` (1–3 lines: what holds after closing), `## Context`
   (files every unit needs; `- ` items are file links only), `##
   Constraints`, optional `## Prerequisites`, `## Traps`, `## Decisions`
   (`decision — why — rejected alternative`).
4. Survey: one block of questions before any unit is written (see below).
5. Phases (phase N+1 waits for N) and units (see Unit). A plan that changes
   what the user sees (UI, CLI, API) ends with an Acceptance unit.
6. Cold-agent check: any "as we discussed", "see above", unnamed file —
   rewrite.
7. `docflow roster apply <id>` with the survey's `--preset` and condition
   flags (`--dry-run` previews) writes the role chains into the header; read
   its warnings (executor outside conditions, `Read:` over its role budget,
   role without a fallback). Manual triples only on an explicit request.
8. `docflow lint`, then `docflow run --dry-run --plan <id> --all` and check
   every line against the chosen agents and models; a requirement the format
   cannot express is named a constraint, not written down as met.
9. Show the plan, apply edits, leave `status: draft`; commit per the
   project's rules, deleting the taken Backlog line in it. Last line of the
   reply: "activate: `docflow activate Pxxx`".

## Survey

One block, not drip-fed, nothing assigned on the user's behalf; a choice
already stated is recorded mechanically and not asked again.

- Conditions and budget: ask which temporary limits apply and whether a
  preset applies — not which model goes to which role. Translate the answer
  into flags: «локальные недоступны» `--off local`; «codex по минимуму»
  `--minimal codex` (acceptance and replan only); `--lean A,B` drops heavy,
  acceptance and replan; `--prefer A,B` puts an access first; «zai до
  четверга» `--expires zai=DATE`; «zai только flash» `--only zai=*flash*`.
  Show the resolved conditions and chains (`roster brief` with the flags)
  before writing units, pass the same flags to `roster apply`. A condition
  that outlives the plan is a `roster off|budget|expire|allow|deny` command.
- `permissions: auto|full` in the frontmatter. `full` runs agents without
  their sandbox and approvals; name the mode anyway — a committed `full`
  must not be a silent escalation. No key — `auto`.
- Auto-replan is not a question: it is on in executor mode; `replan:` only
  picks its route; `docflow run --replans 0` opts out per run.
- What only a human provides (login, test account — name where the
  credentials live, never their value — device, secret, access): each is a
  `## Prerequisites` item. A machine-checkable one carries a command in
  backticks after a colon (run at the start; `(once)` for code state the plan
  itself changes). Such a scenario is a prerequisite, never a `Do:` step;
  when the step only makes sense after earlier work, it opens the unit it
  gates, so `--needs human` arrives before the unit's cost.

## Unit

- One session, one `Verify`, 5–8 files. Keep the `Read:` proxy (bytes / 4)
  under the read budget `roster brief` prints for the role; a judge role has
  none. A plan that fits ~100k of context is one unit; a unit under half an
  hour of agent work is too small — merge. Too large: cut by layer or
  feature, not by files. Calibrate with `docflow stats`.
- `Read:` — exact `path`, `path:L10-80`, `path#anchor`; no directories. A
  file an earlier unit creates is allowed; one it deletes or renames is
  referenced by its new path (a stale link stops the unit, code 2). With
  `repos:` — paths from the project root, not markdown links.
- `Do:` — imperative steps, one action each; choices are made now, or name
  the criterion. No branch operations: a unit does not commit or rewrite
  history (`rebase`, `amend`, `reset` stop the runner, code 5); merge other
  branches at planning time.
- `Verify:` — one backticked command that fails when the unit is not done;
  never "check manually". A grep over `status` output or the plan text needs
  an anchor (`grep -q '^P003 '`). Verify must cover Do: for behavior it
  cannot observe (screen, device flow) add a UI/e2e test or schedule a
  live-check unit right after the phase's first such unit.
- `gate:` in the frontmatter — the cheapest full regression run (unit tests,
  not UI), run after each unit's `Verify`. Set it when units touch shared
  code.
- `Traps:` — only what a context-free agent will stumble on; otherwise no
  field. `Depends:` — default the previous unit (not written); `-`
  independent; else ids from this plan.
- `Executor: agent | model | effort` — the unit's whole triple, replaces the
  header's; only on the user's explicit choice. No shorthands. Any
  `executor:`/`Executor:` puts all open units in executor mode (`Model:` not
  allowed); without any, legacy `Model: fast|strong|max`.
- `Role: cheap|heavy|review|acceptance` — runs along the role's chain
  (`roster apply` writes the keys). `cheap`: mechanics, docs, renames;
  `heavy`: design across modules, concurrency, format and state. Ordinary
  unit: no field.
- `Commit:` — `Pxxx.N.M:` prefix plus the message, exact, so `docflow commit`
  can run it. `Handoff:` is not written — `docflow done` fills it.

## Acceptance

Last unit or phase, `Role: acceptance`, `Depends:` every unit of the plan
(their Handoffs with `Unverified:` come into its prompt). Required whenever
the plan changes what the user sees; a review unit does not replace it.

- `Read:` — existing e2e/UI tests and fixtures; from the implementation only
  the identifiers the tests need.
- `Do:` — one scenario per Goal statement (e2e/UI test or CLI run on real
  data or a same-shape fixture; no manual steps). Every `Unverified:`, `##
  Test changes` and `Replan changed check:` item ends as: closed by a test,
  `Accepted: <item> — <why it is not a risk>`, or left `Unverified:` (a Goal
  gap or a risk with a concrete failing scenario).
- `Verify:` — the full test run. A small failure is fixed in this unit;
  otherwise `blocked`.

## Review

Every phase run on a `check` access (see `roster brief`, its `review:`
matrix) ends with one `Role: review` unit whose chain holds no `check`
access; no independent reviewer is a policy error to fix with the user. At
most 4 units before the review unit.

- `Do:` — review the `## Phase diff` against Goal, Constraints, Decisions and
  the units' `Do:`; edit nothing. Only a violation of those or a regression
  of an earlier fix blocks; anything else is a Handoff item. A repeat review
  checks that findings are closed.
- A `## Test changes` item not justified by the diff and `Do:` (a test
  weakened to pass `Verify:`) is a defect: `blocked`, `blocker: "plan"`. The
  third `Blocked (plan)` of one review or acceptance unit stops the run.
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
---
## Goal
## Context
## Constraints
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

`type`: `feature|bugfix|perf|module|refactor`; the H2 set and order are
fixed (`## Prerequisites` optional, between Constraints and Traps). Statuses:
`[ ]` todo, `[~]` in progress, `[x]` done, `[-]` skipped, `[!]` blocked —
the CLI sets them, not the skill (except in replan).

## Replan

A unit is `[!]`, or `docflow run`/`prompt` rejected one with code 2 (stale
`Read:`): the plan drifted from reality. Under `docflow run` the runner hands
`blocker: "plan"` blocks to the replan executor automatically with the
prompt's own rules; this section is the same procedure by hand.

1. Read the block reason, the Handoffs of done units and `## Decisions`.
   When the trigger names a failing test or command, run it at HEAD several
   times (`-count=3`) and record command and result in `## Decisions`. Red —
   add a fix unit. Green does not lift the block (never blame load, a flake
   or another session): add a unit that reproduces the failure under the
   unit's conditions and fixes it, or block for a human.
2. Edit only `[ ]` and `[!]` units and the plan file; never frontmatter,
   Goal, Constraints, Prerequisites, `[x]`/`[-]` units. New units take the
   next numbers; no renumbering, no id disappears.
3. Reference checks are untouchable: `Verify:`, `Do:`, `Role:` of review and
   acceptance units stay and those units never move to `[-]` (status and
   `Depends:` may change). No open unit becomes `[x]`. `Verify:` of an
   ordinary unit may change and a unit may move to `[-]` — `docflow` records
   it in `## Decisions` itself.
4. Lift the block: uncommitted work still stands → `[~]`; no work → `[ ]`;
   work no longer needed → ask the user, never roll it back; unit not needed
   → `[-]`. Fix stale links in every `[ ]` unit.
5. `docflow lint`, show the changes. Do not do the unit's work and do not
   commit (the runner commits `Pxxx: replan Pxxx.N.M`). Under the runner end
   the reply with the result JSON (`handoff`: one item per plan change); a
   plan that needs a person (login, device, decision) → `blocked` with
   `blocker: "human"` and a concrete `reason`.

## Activation and freezing

A human activates: `docflow activate <id>` (clean lint → `status: active`, id
into the Queue, hint `Pxxx: activate plan`), only on a direct request; never
offer "activate and start" as one question; after activation do not take a
unit — execution is `bsv-docflow-run` on a separate request. Shelving:
`docflow freeze <id> --reason "…"` / `docflow thaw <id>`; none of these
commit.

## What not to do

- No plan or `docflow note` without an explicit request.
- Do not set `status: active` by hand.
- Do not restate Goal inside `Do:`, leave chat references in a unit, or plan
  "explore and decide" without a criterion (that is a question to the user).
- Do not duplicate architecture decisions — link them in `Read:`.
- Do not plan a new format key, section, status or command without a unit
  that removes or generalizes an existing one; name it in `## Decisions`.
