---
name: "bsv-changelog"
description: "Rules for CHANGELOG.md entries and manual release following Keep a Changelog and SemVer. Use when adding or checking an entry, deciding whether an entry is needed, and preparing a release. Works standalone and together with docflow, in any project and on any stack."
---

# CHANGELOG and release

A CHANGELOG is a curated selection of notable changes for users and
maintainers, not a copy of the commit history. These rules apply with
docflow and without it. General rules for other documents are in
`bsv-docs`.

## Format

Follow [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) and
[Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html). In the
`CHANGELOG.md` header, link both standards, for example:

```markdown
# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
```

- The default language is English. An explicit project rule or the user's
  commission takes priority.
- Add new changes at the top under `## [Unreleased]`. Place released
  versions below it in reverse chronological order:
  `## [1.2.0] - 2026-10-20`. The release date is the actual date in ISO
  `YYYY-MM-DD` format.
- Use the categories `Added`, `Changed`, `Fixed`, `Removed`, `Deprecated`,
  `Security`. Do not create empty categories.
- In a multi-module project, start an entry with the area the project
  defines: for example `core:`, `CLI:`, `Android:`, `iOS:`, or `API:`.
- The guideline for an entry is 1–3 lines and about 300 characters. It is
  a brevity measure, not a limit: keep a clear description of the
  consequences when it needs more.

## What to record

Record changes in observable behavior: what a user or integrator notices
in the interface, public API, configuration, CLI, or build requirements.
Use the public names the reader works with directly. Do not list internal
classes, private functions, implementation details, internal ids, or
personal paths.

Do not add an entry for a behavior-neutral refactor, formatting,
comments, file moves, tests, or a dependency update without an observable
effect. If a change is not visible to a CHANGELOG reader, it does not need
a separate entry.

Pick the category by the main effect:

- `Added` — a new capability;
- `Changed` — a change to an existing capability;
- `Fixed` — a defect fix;
- `Removed` — a capability taken away;
- `Deprecated` — a public capability announced as obsolete;
- `Security` — a fix or change related to security.

One entry describes one change in terms of the outcome, not the
implementation. Merge duplicates: when new information adds to a change
already recorded in `Unreleased`, refine that item instead. Do not repeat
the category heading in the text — for example, do not start a `Fixed`
item with the words "Fixed a bug".

## Version per SemVer

Assess the compatibility of the public contract, not just the entry
category:

- `MAJOR` — an incompatible change to the public contract;
- `MINOR` — compatible new functionality, or announcing public
  functionality deprecated;
- `PATCH` — a compatible defect fix.

The category alone does not set the number. Before choosing a version,
review the whole section — especially `Changed`, `Removed`, `Deprecated`,
and `Security` — and check what the behavior means for existing users.
For `0.x`, apply the project's explicit policy: under SemVer, the public
API before `1.0.0` is not considered stable.

Examples:

- A compatible new CLI option — `MINOR`.
- Removing a public option or an incompatible format change — `MAJOR`.
- Announcing a public API deprecated — `MINOR`; a compatible bug fix —
  `PATCH`.
- A `Security` entry does not predetermine the version: assess
  compatibility the same way as for every other change.

## Automatic versioning

Write directly into an open version section without `Unreleased` only when
the project's rules or the tool actually in use explicitly confirm such a
process. Check the instruction, command, or configuration that sets it.
A first heading with a version number by itself does not prove the section
is open. Do not edit a published section or its date on the strength of a
heading alone.

## Manual release

A manual release requires a separate commission or follows the project's
explicit process:

1. Move the contents of `## [Unreleased]` into `## [X.Y.Z] - YYYY-MM-DD`
   with the actual release date and the version chosen per SemVer and the
   project's policy.
2. Create a fresh `## [Unreleased]` on top, without empty categories.
3. Sync the version in the project's authoritative source; update the
   other files carrying a version according to the project's process.
4. Do not edit published sections or their dates. The CHANGELOG by itself
   grants no authority to create a tag, push changes, or publish a
   release; for those actions follow the project's rules and a separate
   commission.
