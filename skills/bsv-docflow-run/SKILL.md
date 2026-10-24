---
name: "bsv-docflow-run"
description: "Runs a docflow plan unit — the next one in order or by id — through the cycle prompt → work → finish (verify → done → commit): what to write in Handoff and Decisions, when to block a unit, what to do on each docflow exit code. Use in a project with a docs/docflow.md file, when asked to continue the work, do the next unit, or do unit Pxxx.N.M. Not for composing or replanning a plan and not for closing it."
---

# Running a docflow unit

A unit is one session. Everything needed is in the unit's prompt and in
the files of its `Context` and `Read:`. The mechanics (statuses, the
fingerprint, the commit) are handled by `docflow`; the agent's part is the
work within `Do:` and the judgment: what to pass on and when to stop.

The general documentation and CHANGELOG rules live in the standalone
`bsv-docs` and `bsv-changelog`; use them when a unit touches those topics.
This skill covers only running a docflow unit.

## Procedure

1. `docflow prompt` — the next unit, or `docflow prompt <id>`. It sets
   `[~]`. The unit is already `[~]` (an interrupted session) — the same
   prompt again; the work continues from the current state of the tree.
   Right after the output — one line to the user, before reading any
   files: `Taking up <id> <title> — plan <plan id> <plan title>`
   (from the prompt's first two lines); when the stderr line of
   `docflow prompt` contains `(resumed)` — add that this continues an
   interrupted session.
2. Read the files from the plan's `## Context` and the unit's `Read:` —
   only those, only the named ranges; do not read the architecture in
   full. Any other code — only when a specific `Do:` item cannot be done
   without it.
3. Do exactly what `Do:` says: no less and no more. Respect `Traps`,
   `Constraints`, and `## Decisions` from the prompt. The
   `## Archive gotchas` section holds traps from the archive — treat them
   as `Traps`.
4. For iterations, run the unit's `Verify:` command or
   `docflow verify <id> --no-gate`. Before replying, run one full
   `docflow verify <id>`; it runs `preflight:` and the plan's `gate:` when
   configured. Do not pipe long checks through `tail` or `grep`. On failure,
   inspect the complete stdout and stderr at the path printed as `log:`.
   The runner reuses a successful full verify while the tree is unchanged;
   a `--skip` record is never reused.
5. `docflow finish <id> --handoff "…"` — `verify`, `done`, and `commit` in
   one call; one `--handoff` per item, a decision made along the way —
   `--decision "…"`. Check the printed file list against `Do:`.

A prompt with a `## Cycle (runner mode)` section — the unit is driven by
the `docflow run` runner. Then follow that section: do run `verify`, but
do not run `finish`, `done`, `commit`, or `block` (they refuse with code
5), do not commit into this repository yourself, and do not touch the
plan file; commits of the tool that a `Do:` step orders (a release,
pinned versions) are allowed. End the reply with the result JSON — the
runner parses it. In a `blocked` reply set `blocker`: `"plan"` when
`Do:` contradicts reality and the plan needs a new step, a new unit or a
fix; `"human"` when only a person can unblock it — a login, access, a
secret, a device or a decision — with `reason` saying what exactly to
do. A reply without the field counts as `human`. With the plan's
`replan:` key a `"plan"` block is replanned automatically and the unit
is re-prompted; `"human"`, or no `replan:` key, stops the run and
notifies.

## Executor

The runner assigns the executor before the call, from the plan's
`executor:`/`Executor:` (the format is in `docs/design/format.md`). The
current agent cannot switch itself by reading `Constraints`: "run on
Codex" in text does not switch the process.

In runner mode the runner has already launched the assigned agent, model,
and effort: do not check them and do not block over them. A model's own
idea of its name or version is unreliable.

In a manual session check only the agent (Claude, Codex, pi), never the
model or effort. When the agent does not match the unit's `Executor:`,
block the unit with that reason (`block`); fixing the assignment is a
replan. Do not create an auxiliary config and do not edit the plan
yourself.

## Handoff

What the next unit needs to know and cannot be seen from the code and the
plan: non-obvious behavior, workarounds, deferrals, a trap found.

- Not a diff retold: "added function X to Y" is visible from the commit.
- Nothing to pass on — one item stating the essence of the result, not
  emptiness.
- What was changed but not verified — as a separate item
  `Unverified: <what> — <which check is missing>` (layout on a device, a
  scenario outside `Verify`, a case without a test). No such items means
  everything was verified. In runner mode — the `unverified` field of the
  result JSON; the runner adds the prefix.
- Good: `parse_ref cuts the anchor at the first '#': anchors containing
  '#' are not supported`. Bad: `updated the parser, added tests`.

## Decision

A choice made along the way that the plan did not contain and that will
outlive the unit: `decision — why — the rejected alternative`. It goes to
the plan's `## Decisions`, and at closing — to the archive. An
implementation trifle the next unit will not dispute is not a decision.

## When to block

`docflow block <id> --needs human|plan --reason "…"` (default `human`)
and stop, when:

- `Do:` contradicts the code: a named file is missing, the API is
  different, the premise is wrong;
- `Do:` cannot be done without changing files or behavior outside the
  unit's frame;
- three `verify` runs in a row without progress (the same error, the same
  number of failures);
- `Verify:` cannot be checked honestly: it passes without the work, or it
  will not pass with the work done right.

The reason is concrete: what the plan expected, what is real. Pick
`--needs`: `plan` when the fix is a plan edit — a stale path, a wrong
premise, a missing step or unit; under the runner this triggers the
auto-replan. `human` when the unblock is not a plan edit — a login, a
secret, a device or simulator step, a decision. In runner mode the
`blocker` field of the result JSON replaces `docflow block`.

Outside the runner — `bsv-docflow-plan` in replan mode next, not this
skill.

`verify --skip "<reason>"` — only when the check cannot run in this
environment (no device, no network) and the user agrees. A failing
`Verify` is not a reason.

## Exit codes

| Code | Means | What to do |
|---|---|---|
| 0 | success | the next step of the cycle |
| 1 | nothing to do | report to the user; a plan `ready to close` is closed by `bsv-docflow-close` |
| 2 | format error | read stderr, `docflow lint`; do not edit the plan — report. An old format version — the `docflow migrate` hint; run it with the user's consent |
| 3 | unit `[!]` | stop; replan is `bsv-docflow-plan` |
| 4 | `Verify` failed | fix per the tail of the output, repeat; see "When to block" |
| 5 | order violated | read stderr, below |

Code 5:
- `working tree is dirty` on `prompt` — someone else's changes; show them
  to the user; do not stash or roll back on your own.
- `… is in progress` — another unit is `[~]`: continue that one
  (`docflow prompt` without an id).
- `depends on unfinished …` — the dependencies first.
- `done`/`commit` after tree changes — run `verify` again.
- `commit` before `done` — `done` first (`finish` does all of it in
  order).
- `the agent rewrote history` from `docflow run` — show the rewritten
  commits to the user; with the user's consent, `docflow finish <id>`.

## Nested repositories

In a project with several git repositories (`repos:` in the plan
frontmatter), `docflow finish` commits into every repo from `repos:` and
into the root. The agent does not commit on its own — neither to the root
nor to the nested repos — `docflow commit` does all of it.

## What not to do

- Do not edit the plan file by hand: statuses, Handoff, and Decisions are
  written by `docflow`.
- Do not commit outside `docflow finish`/`commit` and make no commits
  inside a unit. Do not rewrite history (`rebase`, `amend`, `reset`), even
  when a `Do:` asks for it: that is a plan error — block the unit with
  that reason.
- Do not widen the unit: something found along the way is a Handoff item
  or a question to the user, not a "while I'm here" fix.
- Do not take up the next unit without the user's request.
