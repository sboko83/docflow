---
name: "bsv-docs"
description: "Rules for project documentation: the purpose of each genre, placement, language, record lifetime, telling fact, decision, and status apart, auditing duplicates and stale working notes. Use when writing or updating README, architecture documents, API contracts, and other docs, when choosing where a record goes, and when auditing documentation. Not for CHANGELOG — that is governed by bsv-changelog. In projects with docs/docflow.md, plans, task statuses, and the decision archive are handled by docflow. Works standalone, with docflow or without it."
---

# Project documentation

Documentation keeps the context and contracts the project's readers need.
Each document answers its own question; volume by itself is neither a goal
nor a reason to delete useful specifications.

## Genres

| Document | Question | What lives inside |
|---|---|---|
| `CHANGELOG.md` | what changed | notable behavior per the rules of `bsv-changelog` |
| `docs/ARCHITECTURE.md` or `docs/architecture/*.md` | why it is built this way | acting decisions, reasons, and alternatives |
| `docs/PLAN.md` or a roadmap | what is in flight and what is next | current task statuses |
| `docs/API.md` and contracts | what is exposed to the outside | interfaces, models, limits, and format traps |
| `README.md` | how to get started | requirements, commands, project layout |
| `AGENTS.md` and agent instructions | how to work in the project | rules for agent tasks |

Names and placement are set by the project. If there is no need, do not
create a document just to fill in a genre.

## Fact, reason, and status

One piece of work can yield several records if each answers a separate
question:

- an observable change — `CHANGELOG.md`;
- the reason for a choice — an architecture document, and in a docflow
  project an archive decision;
- the current state — README, a roadmap, or the docflow status.

Do not copy the same story into these places. Each record holds only its
own aspect; link instead of retelling when navigation needs it.

## Lifetime

- After a task closes, keep the status that is still needed and delete
  temporary working notes that no longer help maintain the project.
- A closed plan phase can be reduced to one status line with a date once
  its details are no longer needed for current work.
- An architecture decision stays while it acts. When it is cancelled,
  update the current description and keep the history in the archive the
  project provides, if it has one.
- About 15 lines for a decision record is a signal to check whether the
  decision and its implementation have merged. It is not a limit: useful
  rationales, specifications, and constraints keep the details they need.

## Docflow mode

The marker is a `docs/docflow.md` file in the project.

With the marker:

- the `docs/PLAN.md` genre and the task and phase lifecycle rules do not
  apply; the queue, plans, statuses, and handoff are handled by docflow
  (`bsv-docflow-plan`, `bsv-docflow-run`, `bsv-docflow-close`);
- a rationale is written into the plan's `## Decisions` and moved into the
  archive entry at closing;
- the architecture document describes the current design and links to the
  archive for the rationale instead of retelling it;
- a cancelled decision in the architecture document is updated;
  `superseded_by:` links archive entries only.

Without the marker the ordinary genres apply, including `PLAN` or a
roadmap. Do not turn on docflow mode merely because the CLI of the same
name is installed in the project.

## What not to keep in documentation

- Verification protocols: test lists and counters, build output, the
  running scheme or simulator details. Report them next to the check
  result; long-lived documentation keeps only reproducible instructions.
- Retold code, lists of touched files, and the course of reasoning. The
  code and the change history are already in the repository; a document
  must explain a contract, a decision, or a way of working.

## Language and style

- The language is set by the project's explicit instructions and the user's
  commission. When they do not set one, follow the language of the existing
  section; in a new project, the language of the request. When editing,
  keep the source document's language unless a translation was asked for.
- Tell automatically loaded instructions apart from on-demand reference.
  Keep automatically loaded context short; put detailed formats and
  specifications separately and load them as needed.
- Write directly, group material by questions, avoid repetition and
  introductory phrases.

## Workflow

1. Determine what is being recorded: behavior, the reason for a choice,
   the current state, or an instruction.
2. Pick a genre from the table and follow the project's rules; for
   `CHANGELOG.md` use `bsv-changelog`.
3. Check the affected links, tables of contents, and the current state.
   Do not repeat the record's text there — add a link if navigation
   needs it.
4. If behavior, a decision, or the live status did not change, the
   documentation may need no edit at all.

## Documentation audit

Run an audit on request, or when documentation has visibly bloated or gone
stale. Its scope is three checks:

1. Duplicates: the same fact, decision, or status repeated in several
   places.
2. Protocols: permanent documents still hold logs, counters, or one-off
   check results.
3. Working notes: closed tasks or phases still carry temporary notes that
   are no longer needed.

Documentation at roughly 10% of the code's size and decisions longer than
about 15 lines are signals for a thoughtful review, not separate defects
and not a demand to cut. Keep the specifications and documentation that
are needed. Propose a targeted plan for the problems found; do not rewrite
correct text for the sake of size.
