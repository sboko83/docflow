---
name: "bsv-docflow-close"
description: "Closes a docflow plan: docflow close, filling the archive entry (Outcome, Decisions, Gotchas, Follow-ups), links from the current architecture, a CHANGELOG entry, and Follow-ups into the Backlog. Also handles an explicitly commissioned closing of an unfinished plan and writes a decision archive entry without a plan (docflow note). Use in a project with a docs/docflow.md file, when docflow status says ready to close, when asked to close or drop a plan, or to record a decision into the archive. Not for running units and not for composing or replanning a plan."
---

# Closing a docflow plan

A plan lives until it is closed; what remains after it is the archive
entry — the decision log — and a link to it from the architecture. The
mechanics (moving `## Decisions`, deleting the plan, the Queue line) are
done by `docflow close`; the skill writes what judgment requires.

The documentation and CHANGELOG rules live in the standalone `bsv-docs`
and `bsv-changelog`; this skill covers only closing. Closing a plan does
not start a release by itself; use the project's release process and
`bsv-changelog`.

## When

- `docflow status` says `ready to close`: every unit is `[x]` or `[-]`.
- The user asks to drop the plan — see "Dropped plan".
- A decision without a plan — see "Entry without a plan".

## Procedure

1. The working tree is clean, the units are closed. Read the plan in
   full: Goal, `## Decisions`, the units' Handoffs — the entry is written
   from them.
2. Links to the plan: `grep -rn 'plans/Pxxx' --include='*.md' .`. Rewrite
   each one to the future entry `docs/archive/Pxxx-<slug>.md` (the same
   slug as the plan's) or remove it. Otherwise `close` refuses (code 2).
3. `docflow close Pxxx` — prints the archive entry path.
4. Fill the entry:
   - `## Outcome` — 2–3 lines: what now exists. The result, not the
     process.
   - `## Decisions` — moved in by the script. Compress each to
     `decision — why — the rejected alternative`; drop duplicates and
     decisions cancelled later in the same plan. Drop implementation
     trifles nobody will dispute.
   - `## Gotchas` — traps that remain after closing: in the code, in the
     environment, in the tools. Traps of the plan process itself do not
     belong here. The items feed future units' prompts: each must be
     understandable without the plan. They reach a prompt only when the
     entry is referenced by a plan's `## Context` or a unit's `Read:`
     file — usually the architecture, step 5.
   - `## Follow-ups` — what was not done. Triage every open
     `Unverified:` item of the units' Handoffs: verify it now; or write
     `Accepted: <item> — <why it is not a risk>` — it stays only in the
     entry, like `Waits:`; or move it to the Backlog (step 8) when it is a
     Goal gap or a risk with a concrete failing scenario. Unfinished Goal
     work always goes to the Backlog. When even one `Unverified:` remains,
     the Outcome says what was left unverified.
   An empty section is a `lint` error; nothing to say — `None.`
5. The project's architecture (ARCHITECTURE.md or whatever AGENTS.md
   names): the current design after the plan, and a link to the archive
   entry next to each changed decision. Do not write the "was — became"
   history: it lives in the archive.
6. README (or the other current-state document): lines describing the
   plan as running or upcoming become the result, with a link to the
   entry.
7. CHANGELOG — per `bsv-changelog`, observable behavior only; no
   `CHANGELOG.md` in the project — skip the step, do not create the file.
8. Follow-ups — as lines in `## Backlog` of `docs/docflow.md`; one line
   is one task understandable without the plan. Triage each open item
   (step 4): unfinished Goal work always goes to the Backlog, so does the
   unfinished part of a dropped plan; an `Unverified:` item goes there when
   it is a Goal gap or a risk with a concrete failing scenario. Two kinds
   stay only in the entry: an observation outside the Goal that needs
   nothing until an external event (a project, a device, a failure that has
   not happened yet) — `Waits: <event> — <what to do>`; and an accepted
   risk — `Accepted: <item> — <why it is not a risk>`.
   `grep -rn 'Waits:' docs/archive` lists all of the first kind.
9. `docflow lint` — without errors.
10. When the project's rules or the commission require a commit, use the
    ordinary `git commit`: `docflow close` does not commit by itself. The
    project sets the message format. Include in the index only the entry,
    `docs/docflow.md`, the deleted plan, the architecture, README,
    CHANGELOG, and the files that held links. When the closing happens
    inside a unit of another plan — do not include that plan's file in
    this commit.

## Dropped plan

1. A `[~]` unit with uncommitted work — ask the user what to do with it.
   Do not roll it back on your own.
2. The remaining `[ ]`, `[~]`, `[!]` — set to `[-]` by hand. This is the
   only manual status edit besides replan.
3. Into the plan's `## Decisions` — an item
   `plan dropped — why — what instead`.
4. Then the "Procedure". The Outcome — what was finished and what was
   not; the unfinished part, when still wanted, goes to Follow-ups.

A frozen plan is closed the same way: closing a frozen plan is no
different from closing an ordinary one. Freezing is the alternative to
dropping when the plan must stay in history but not in the queue; when a
plan is rejected for good, it is closed.

## Entry without a plan

A small change settled something and there was no plan:
`docflow note <slug> [--title …]` — an entry in the same numbering. Fill
every section (`Outcome` — what changed; the rest — to the point or
`None.`), link it from the architecture, `docflow lint`, commit
`Pxxx: add note`.

## The archive is immutable

A committed archive entry is not edited. The only edit is
`superseded_by: Pyyy` in the frontmatter, when a decision is cancelled by
a later plan or entry; the architecture then links to the new entry.

## What not to do

- Do not copy the units' Handoffs or a retelling of the commits into the
  entry.
- Do not leave links to `docs/plans/Pxxx…` anywhere outside git history.
- Do not duplicate the "why" in the architecture: there — a link to the
  archive.
- Do not close a plan with a `[ ]`, `[~]`, or `[!]` unit without the
  user's decision to drop it.
