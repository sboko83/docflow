package main

var helpTexts = map[string]string{
	"skills-apply": `usage: docflow skills-apply [-h] [--skills {global,project}]

apply the stored skills mode; docflow update runs it through the freshly
activated release

Not listed in the help on purpose: it is a ` + "`update`" + ` implementation
detail. Without --skills the mode stored in ~/.docflow/skills-mode applies;
without a stored mode and without a terminal global is used and nothing is
stored.

options:
  -h, --help  show this help message and exit
  --skills {global,project}
              personal links (global) or project-only skills (project)
`,
	"install": `usage: docflow install [-h] [--skills {global,project}]

install an unpacked release into ~/.docflow and link personal skills

Run the docflow binary beside skills/ and LICENSE from a release archive.
Configure PATH for zsh/bash and install personal skills for agent CLIs in
PATH (claude, codex, pi, opencode), and for ~/.zcode when it exists.
--skills sets the skills mode: global links the personal skill directories,
project keeps them clean and only project copies (docflow init --skills,
docflow install-skills --project) carry the skills. Without the flag the
mode stored in ~/.docflow/skills-mode applies; a first run on a terminal
asks once and remembers the answer; without a terminal global is used.

options:
  -h, --help  show this help message and exit
  --skills {global,project}
              personal links (global) or project-only skills (project)
`,
	"update": `usage: docflow update [-h] [--check] [--skills {global,project}]

download and activate the latest release

Only standalone installations can update. For a development checkout use:
git pull && bash tools/install-dev.sh
Personal skill links point into ~/.docflow/current and follow the active
version. --skills global|project sets the skills mode: it is stored in
~/.docflow/skills-mode, asked once on a terminal when neither is given,
and global without a terminal prints a hint instead. An explicit mode
applies to the links even when the release is up to date.
Stale project skill copies in the current project are reported with
their install-skills --project command.
The previous release stays in ~/.docflow/versions; roll back with
ln -sfn versions/<previous> ~/.docflow/current; the next update reinstalls
the latest release.
DOCFLOW_RELEASE_URL overrides the GitHub Releases base URL.
Daily terminal checks can be disabled with DOCFLOW_NO_UPDATE_CHECK=1.

options:
  -h, --help  show this help message and exit
  --check     check for a new release without installing it
  --skills {global,project}
              personal links (global) or project-only skills (project)
`,
	"uninstall": `usage: docflow uninstall [-h]

remove the standalone installation and its personal skill links

Remove ~/.docflow, its managed PATH entries, and personal skill symlinks
pointing into it. Development checkouts cannot be uninstalled this way.

options:
  -h, --help  show this help message and exit
`,
	"init": `usage: docflow init [-h] [--agent {claude,codex,pi,opencode}] [--skills]

create docs/docflow.md, plans/, archive/ and a line in AGENTS.md; --skills
also copies the skills into the project

options:
  -h, --help            show this help message and exit
  --agent {claude,codex,pi,opencode}
                        skill directory set (default: claude)
  --skills              copy skills/* into the selected project directory
`,
	"status": `usage: docflow status [-h]

show the queue, the current plan and its progress

options:
  -h, --help  show this help message and exit
`,
	"next": `usage: docflow next [-h]

print the id of the next available unit

options:
  -h, --help  show this help message and exit
`,
	"prompt": `usage: docflow prompt [-h] [id]

print the prompt for the next unit

positional arguments:
  id

options:
  -h, --help  show this help message and exit
`,
	"verify": `usage: docflow verify [-h] [--skip REASON] [--no-gate] id

run the Verify command of the current unit

positional arguments:
  id

options:
  -h, --help     show this help message and exit
  --skip REASON  record the fingerprint without running Verify
  --no-gate      skip the plan's gate; nothing is recorded
`,
	"done": `usage: docflow done [-h] --handoff HANDOFF [--decision DECISION] id

mark the current unit done and write its Handoff

positional arguments:
  id

options:
  -h, --help           show this help message and exit
  --handoff HANDOFF    Handoff item; repeat per item
  --decision DECISION  item for ## Decisions; repeat per item
`,
	"commit": `usage: docflow commit [-h] id

commit the current unit with its Commit subject

positional arguments:
  id

options:
  -h, --help  show this help message and exit
`,
	"finish": `usage: docflow finish [-h] --handoff HANDOFF [--decision DECISION]
                      [--skip REASON]
                      id

verify, done and commit the current unit in one call

positional arguments:
  id

options:
  -h, --help           show this help message and exit
  --handoff HANDOFF    Handoff item; repeat per item
  --decision DECISION  item for ## Decisions; repeat per item
  --skip REASON        record the fingerprint without running Verify
`,
	"stats": `usage: docflow stats [-h] [--runs] [--json] [PLAN]

token use per unit: context proxy against the real figures; --runs summarises run events instead

positional arguments:
  PLAN        only the units of this plan, e.g. P009

options:
  -h, --help  show this help message and exit
  --runs      per-unit docflow version, attempts, verify failures, replans, blocked ends, time and rework (commits outside docflow that change the unit's lines within 7 days) from the run event streams
  --json      runs: print the report as JSON
`,
	"block": `usage: docflow block [-h] --reason REASON [--needs {human,plan}] id

mark the current unit blocked

positional arguments:
  id

options:
  -h, --help            show this help message and exit
  --reason REASON
  --needs {human,plan}  who unblocks the unit (default: human)
`,
	"unblock": `usage: docflow unblock [-h] [--note NOTE] id

resume a unit blocked for a human

The unit goes back to [~] and the note lands in its Handoff as
Unblocked: ...; the resumed agent checks the blocked condition first.
A block raised with --needs plan is lifted by replanning, not here.
The runner runs this command itself; the person confirms the step.

positional arguments:
  id

options:
  -h, --help   show this help message and exit
  --note NOTE  what the person did to lift the block
`,
	"lint": `usage: docflow lint [-h]

check structure, ids, Depends and the Read budget

options:
  -h, --help  show this help message and exit
`,
	"new": `usage: docflow new [-h] [--title TITLE]
                   [--type {feature,bugfix,perf,module,refactor}]
                   slug

create a plan from the template and add it to the Queue

positional arguments:
  slug

options:
  -h, --help            show this help message and exit
  --title TITLE
  --type {feature,bugfix,perf,module,refactor}
`,
	"activate": `usage: docflow activate [-h] id

make a draft plan active after a clean lint of it

positional arguments:
  id

options:
  -h, --help  show this help message and exit
`,
	"close": `usage: docflow close [-h] id

archive a finished plan and delete it

positional arguments:
  id

options:
  -h, --help  show this help message and exit
`,
	"freeze": `usage: docflow freeze [-h] [--reason REASON] id

move a plan from the Queue to Frozen

positional arguments:
  id

options:
  -h, --help       show this help message and exit
  --reason REASON  why the plan is frozen
`,
	"thaw": `usage: docflow thaw [-h] id

move a frozen plan back to the end of the Queue

positional arguments:
  id

options:
  -h, --help  show this help message and exit
`,
	"note": `usage: docflow note [-h] [--title TITLE] slug

write an archive entry without a plan

positional arguments:
  slug

options:
  -h, --help     show this help message and exit
  --title TITLE
`,
	"migrate": `usage: docflow migrate [-h]

convert docs from previous format versions

options:
  -h, --help  show this help message and exit
`,
	"install-skills": `usage: docflow install-skills [-h] [--agent {claude,codex,pi,opencode}]
                              [--target DIR] [--project]

symlink skills/* into the selected agent directory, or copy them into the
project with --project

options:
  -h, --help            show this help message and exit
  --agent {claude,codex,pi,opencode}
                        skill directory set (default: claude)
  --target DIR          personal skills directory override
  --project             copy skills/* into the project
`,
	"roster": `usage: docflow roster [-h] [--roster PATH] [--addr ADDR]
                      [--until DATE] [--preset NAME] [--off A,B]
                      [--expires A=DATE] [--json] [--dry-run]
                      [--refresh] [--online] [--all] [--write]
                      {show,brief,explain,apply,scan,models,status,off,on,expire,reset,migrate,web}
                      [ACCESS] [ARG ...]

scan model access catalogs, show the route chains per plan role from the
personal roster, or write them into a plan header

Reads ~/.config/docflow/roster.toml (or --roster PATH): a version 4
policy whose accesses, in priority order, name the MODEL@EFFORT they
serve each plan role with, so the chain of a role is its column of the
access x role table. It prints the plan-header lines executor:,
fallback:, cheap:, heavy:, review:, acceptance: and replan: with their
unit budgets. roster brief prints the conditions in force, one row per
role with its chain and read budget, the review matrix, why every chain
was chosen and the warnings of incomplete chains. A condition flag of
the call — --preset, --off, --expires — is merged in memory for this
one call and never written, so the planner can answer a question
without changing the roster state; brief, show, explain and apply share
the same flags. roster show is an alias of roster brief.

roster explain PLAN loads the plan from docs/plans and prints the
header keys apply would rewrite as "key: old → new" plus the warnings
of the roles the plan uses. It changes neither the plan nor the state
file and never commits.

roster apply PLAN writes executor:, fallback:, replan: and the role
keys of the unit Role: fields into the plan header in docs/plans
(draft, active or frozen), removes role keys without units, lints the
plan and restores it on lint errors; a run in progress stops apply.
An open unit whose executor or role chain the conditions leave empty
stops apply before the write: removing the key would send run back to
a legacy tier. --dry-run prints the changes without writing the plan;
--json prints the changes and the warnings. apply warns about a unit whose Executor:
is outside the current conditions, an open unit whose Read: proxy is
larger than the role budget and a role without a fallback. The runner
never reads the roster; a plan stays reproducible without it.

roster scan collects the accesses, their models, context windows,
prices and reasoning levels from Pi, OpenCode, the built-in Codex and
Claude tables and the auth probes, writes the catalog cache under
$XDG_CACHE_HOME/docflow/roster and prints the access summary; --online
adds models.dev metadata and --refresh fetches it again. A source that
cannot answer is a warning on stderr and the scan still exits 0. Scan
also runs on a roster of a previous format, so the catalog can come
before roster migrate. roster models lists the models of the roster
accesses, or of one ACCESS, or of the whole catalog with --all; roster
status lists every access with its channels, auth status, model count
and sources, then the active conditions with their source. scan, models
and status take --json.

Conditions are the temporary layer over the policy: off, on, expire and
reset write roster.state.toml next to the roster (roster.toml becomes
roster.state.toml) and never rewrite roster.toml. off ACCESS switches
an access off until --until DATE (the last day it stays off) or until
on ACCESS; expire ACCESS DATE drops an access after DATE; reset deletes
the state file. An off condition whose until date has passed is
dropped when the file is loaded. A condition command reads the version 4
policy and fails on an older roster with the migration hint.

roster web serves the table editor on 127.0.0.1: one select per cell,
tabs for the default policy and the named policies of the variants
files, the chains of the tab below. It writes roster.toml and the
variants files; the catalog stays with roster scan.

roster migrate converts a version 1, 2 or 3 roster into a version 4
policy and prints it with the files around it; --write keeps every old
file in FILE.bak and replaces it atomically. The v3 rules — classes,
class requirements, budgets, trusted and the model allowlist — are
resolved once against the scanned catalog and written as cells, so run
roster scan first; presets become named policies in the variants file
and an access with budget = "off" an off condition. A roster that is
already version 4 is exit code 2 and nothing is written; every other
roster command refuses an older file with this command in the hint, and
--profile is gone — the flag was v1 and --preset replaces it.

subcommands:
  show             print the brief (alias of brief)
  brief            print the conditions, chains, budgets and warnings
  web [--addr ADDR]
                   serve the workspace and its operations on 127.0.0.1
                   for the browser editor; the project variants layer
                   follows the working directory, Ctrl-C stops it
  explain PLAN     compare the plan header with the brief
  apply ID         write the chains into the plan header
  scan             collect the catalog and write the cache
  models [ACCESS]  list the models of the catalog
  status           list the accesses and the active conditions
  off ACCESS       switch an access off (--until DATE bounds it)
  on ACCESS        lift the off condition of an access
  expire ACCESS DATE
                   drop an access after DATE
  reset            delete the condition state
  migrate          convert a v1, v2 or v3 roster to version 4

options:
  -h, --help      show this help message and exit
  --roster PATH   roster.toml path (default ~/.config/docflow/roster.toml)
  --addr ADDR     web: address to listen on (default: a random port on 127.0.0.1)
  --until DATE    off: the last day the access stays off (YYYY-MM-DD, UTC)
  --preset NAME   brief and apply: the named policy to apply on top of the default
  --off A,B       brief and apply: accesses switched off for this call only
  --expires A=DATE
                  brief and apply: expire an access on DATE for this call only
  --json          print the result as JSON
  --dry-run       apply: print the header changes without writing the plan
  --refresh       scan: ignore fresh caches and scan again
  --online        scan: enrich the catalog from models.dev
  --all           models: the whole catalog, not only the roster accesses
  --write         migrate: write roster.toml and the files around it, keeping FILE.bak
`,
	"doctor": `usage: docflow doctor [-h] [--agent {claude,codex,pi,opencode}] [--target DIR]

check that the installed script and skills match this repo

options:
  -h, --help            show this help message and exit
  --agent {claude,codex,pi,opencode}
                        skill directory set (default: claude)
  --target DIR          personal skills directory override
`,
	"tui": `usage: docflow tui [-h] [--attempts N] [--replans N] [--roster PATH]

open the interactive terminal workspace for the project

The workspace has four screens. 1 Проект keeps the Queue tree, the selected
unit card, the Backlog and the project, plan and session parameters. 2 Ростер
shows the accesses, conditions and variants of the roster and writes a variant
into the selected plan header. 3 Прогон streams the messages, reasoning, tool
calls and checks of the selected unit. 4 История lists past runs and replays
their streams. The workspace reads docs/docflow.md and the plans it names with
the docflow parser. s starts the current Queue run, R resumes its checkpoint,
p pauses, S stops after the current unit and x stops now. a, F, T and C run
the same activate, freeze, thaw and close checks as the CLI. --roster names the
file the roster screen reads (roster.DefaultPath() otherwise). Without a
terminal it refuses and points at the ordinary CLI commands.

options:
  -h, --help    show this help message and exit
  --attempts N  agent calls per unit (default 3)
  --replans N   auto-replan calls per phase (default 3; 0 disables)
  --roster PATH roster.toml path of the roster screen (default
                ~/.config/docflow/roster.toml)
`,
	"run": `usage: docflow run [-h] [--config PATH] [--agent NAME] [--model M]
                   [--effort E] [--attempts N] [--replans N] [--max-units N]
                   [--no-wait] [--no-notify] [--dry-run] [--all] [--plan ID]
                   [--print-config] [--yes] [-v] [--stop]

drive the plan unit by unit through a coding agent

options:
  -h, --help      show this help message and exit
  --config PATH   agents.toml (default ~/.config/docflow/agents.toml); plans
                  without executors only
  --agent NAME    agent profile for every unit (default: by tier); plans
                  without executors only
  --model M       model id or tier for every unit; plans without executors
                  only
  --effort E      reasoning effort for every unit; plans without executors
                  only
  --attempts N    agent calls per unit (default 3)
  --replans N     auto-replan calls per phase (default 3, at most 2 per unit;
                  0 disables)
  --max-units N   stop after N units
  --no-wait       stop instead of waiting out a usage limit
  --no-notify     skip the end-of-run desktop notification
  --dry-run       show the next unit and the agent command
  --all           with --dry-run: the agent, model and effort of every unit
                  still to do
  --plan ID       with --dry-run: read this draft, active or frozen plan
                  instead of the Queue
  --print-config  print the built-in agents.toml
  --yes           answer the start confirmation (permissions full,
                  prerequisites) without asking
  -v, --verbose   add per-unit details: tokens, quota, eta
  --stop          ask the active runner to stop after its current unit
`,
}
