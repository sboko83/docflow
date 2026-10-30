# bsv-docflow

English | [Русский](README.ru.md)

bsv-docflow runs software work with AI agents as a small spec-driven
process. A task is written up as a plan, the plan splits into one-session
units, each unit is executed, verified, and committed on its own, and the
closed plan collapses into an archive entry that keeps the decisions.
The CLI is a single self-contained Go binary installed with one command;
Go is needed only for development.

## Why

A long agent session degrades: context fills up, the agent loses track,
and nothing proves the result was checked. bsv-docflow keeps the working
state in the repository instead of the agent's memory.

- **Spec first.** A task becomes a plan of phases and one-session units.
  A unit carries Goal, Read list, Do, Verify, Traps, and a ready commit
  subject; `lint` checks it against a read budget (~100k tokens
  per session), so a session opens with its own bounded context instead
  of the whole repository.
- **The cycle is enforced, not promised.** `prompt → work → finish`
  (verify → done → commit); a command refuses when the previous step is
  missing. `verify` fingerprints the working tree and `commit` commits
  exactly that tree — what was verified is what landed; one unit is one
  commit.
- **The whole plan can run unattended.** `docflow run` drives the units
  without a human: prompts the agent, verifies, commits, retries a failed
  unit, switches to the fallback executor on a provider limit or outage,
  and stops safely on a timeout or a stalled agent. Claude Code, Codex CLI,
  Pi and OpenCode are built in; the plan (or a personal roster, `docflow roster`)
  routes each unit to its own assistant, model, and reasoning effort
  ([details](docs/usage.md#автозапуск), in Russian).
- **Sessions hand off.** A finished unit leaves Handoff lines and
  Decisions, and the next unit's prompt already carries the handoff of
  its dependencies and the plan's decisions. Work spans sessions without
  re-reading history.
- **Decisions survive, bloat does not.** Closing a plan writes an
  archive entry — outcome, decisions, gotchas, follow-ups — and the
  working plan is deleted. Docs keep decisions and link to the archive
  instead of retelling it.

## How it works

A plan is a Markdown file made of fixed blocks. The agent writes what goes
inside them; the `docflow` CLI owns their state and moves them through the
cycle.

```
docs/docflow.md             Queue · Backlog
docs/plans/P007-cache.md    Goal · Context · Traps · Decisions
  ## Phase 1
  ### [x] P007.1.1          Read · Do · Verify · Commit · Handoff
  ### [~] P007.1.2          ← in progress
  ### [ ] P007.2.1          Depends: P007.1.2
        │  close: every unit is [x]
        ▼
docs/archive/P007-cache.md  Outcome · Decisions · Gotchas · Follow-ups
```

`prompt` builds a unit's prompt from its block, the plan's Decisions, and
the Handoff of its dependencies, and marks it `[~]`. `verify` runs the
unit's `Verify:` command and fingerprints the tree; `done` marks it `[x]`
and writes Handoff; `commit` commits that exact tree under the `Commit:`
subject. A unit that cannot proceed is marked `[!]` by `block`: a stale
plan goes to replanning, a human-only step waits for
`docflow unblock <id> --note "…"`. `lint` checks the structure and the
`Read:` budget; `docflow run` drives the same commands unit after unit.

## See it run

A recorded `docflow run` of a small demo plan: a Go `slug` command built
in three phases by Codex, Pi, and Claude. `bash tools/demo-run.sh`
replays the scenario in a throwaway repository and spends their quota
(`--dry-run`: routes only, no model calls).

![docflow run: start and the first units](docs/img/demo-run.svg)

Each unit goes to the agent, model, and reasoning effort the plan routes
it to. The runner verifies and commits the unit itself; whatever the agent
could not check is reported as `Unverified` instead of being passed off as
done. Quota bars show each subscription window as the share a unit spent
(or the share left, `[display] quota = "left"`).

![docflow run: a stale unit and an automatic replan](docs/img/demo-replan.svg)

Unit 2.1 moved `main.go` under `cmd/slug/`, so unit 2.2 still read a file
that no longer exists. `prompt` refuses such a unit; the agent from the
plan's `replan:` key repairs the plan and commits it, and the unit runs
again in the same run.

![docflow run: the last unit, the summary, and docflow status](docs/img/demo-finish.svg)

The summary counts units, time, cost, unverified notes, and replans;
`docflow status` then reports the plan ready to close: closing turns it
into an archive entry with its decisions.

## Skills

- [bsv-docs](skills/bsv-docs/SKILL.md) — project documentation.
- [bsv-changelog](skills/bsv-changelog/SKILL.md) — changelog and manual release.
- [bsv-docflow-plan](skills/bsv-docflow-plan/SKILL.md) — planning and replan.
- [bsv-docflow-run](skills/bsv-docflow-run/SKILL.md) — running a plan unit.
- [bsv-docflow-close](skills/bsv-docflow-close/SKILL.md) — closing a plan and the archive.

The first two work without docflow; the rest cover the docflow planning
cycle.

## Quick start

Requirements: macOS, Linux, or Windows 10/11; amd64 or arm64. No Go
toolchain needed.

macOS and Linux:

~~~bash
curl -fsSL https://raw.githubusercontent.com/sboko83/docflow/main/install.sh | sh
~~~

Windows (PowerShell):

~~~powershell
irm https://raw.githubusercontent.com/sboko83/docflow/main/install.ps1 | iex
~~~

The installer verifies the release checksum and runs `docflow install`:
everything lands under `~/.docflow`, PATH is configured (restart your
terminal), and skills are linked for agent CLIs found in `PATH` — or left
to project copies, as `install` asks once.

~~~bash
docflow doctor --agent codex
cd <project>
docflow init --agent codex
~~~

Claude Code is the default; omit `--agent codex` for it, use `--agent pi`
for Pi, `--agent opencode` for OpenCode —
`docflow install-skills --agent opencode` links the skills into
`~/.config/opencode/skills`. `docflow update` activates the latest release,
`docflow uninstall` removes it. Details — Git Bash, versions, skills mode,
rollback: [installation and updates](docs/usage.md#установка) (in Russian).

### Development

From a checkout (requires Go 1.24+ and git):

~~~bash
git clone https://github.com/sboko83/docflow bsv-docflow
cd bsv-docflow
bash tools/install-dev.sh
~~~

The script builds `bin/docflow` and links it from the checkout (keep the
checkout); `docflow update` refuses in such a build.

## Project links (docs in Russian)

- [Decisions and format](docs/design/README.md)
- [Command reference](docs/design/cli.md)
- [Skill boundaries and installation](docs/design/skills.md)
- [Task queue](docs/docflow.md)
- [Changelog](CHANGELOG.md)

## Acknowledgements

Written with the assistance of Claude Code, Codex, ZCode, and Pi.

## License

MIT — see [LICENSE](LICENSE).
