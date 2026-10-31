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
	"stats": `usage: docflow stats [-h] [PLAN]

token use per unit: context proxy against the real figures

positional arguments:
  PLAN        only the units of this plan, e.g. P009

options:
  -h, --help  show this help message and exit
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
	"roster": `usage: docflow roster [-h] [--roster PATH] [--until DATE]
                      [--preset NAME] [--off A,B] [--minimal A,B]
                      [--lean A,B] [--prefer A,B] [--expires A=DATE]
                      [--only A=GLOB] [--json] [--dry-run] [--refresh]
                      [--online] [--all] [--write]
                      {show,brief,explain,apply,scan,models,status,off,on,budget,expire,allow,deny,reset,migrate}
                      [ACCESS] [ARG ...]

scan model access catalogs, show the route chains per plan role from the
personal roster, or write them into a plan header

Reads ~/.config/docflow/roster.toml (or --roster PATH), picks the chain
of every role, and prints the plan-header lines executor:, fallback:,
cheap:, heavy:, review:, acceptance: and replan: with their unit
budgets. roster brief prints the conditions in force, one row per role
with its chain and read budget, the review matrix, why every chain was
chosen and the warnings of incomplete chains. A condition flag of the
call — --preset, --off, --minimal, --lean, --prefer, --expires,
--only — is merged in memory for this one brief and never written, so
the planner can answer a question without changing the roster state.
roster show is an alias of roster brief.

roster explain PLAN loads the plan from docs/plans and prints the
header keys apply would rewrite as "key: old → new" plus the warnings
of the roles the plan uses. It changes neither the plan nor the state
file and never commits.

roster apply PLAN writes executor:, fallback:, replan: and the role
keys of the unit Role: fields into the plan header in docs/plans
(draft, active or frozen), removes role keys without units, lints the
plan and restores it on lint errors; a run in progress stops apply.
--preset applies a preset condition set and --dry-run prints the changes
without writing the plan; --json prints the changes and the warnings. apply warns about a unit whose Executor:
is outside the current conditions, an open unit whose Read: proxy is
larger than the role budget and a role without a fallback. The runner
never reads the roster; a plan stays reproducible without it.

roster scan collects the accesses, their models, context windows,
prices and reasoning levels from Pi, OpenCode, the built-in Codex and
Claude tables and the auth probes, writes the catalog cache under
$XDG_CACHE_HOME/docflow/roster and prints the access summary; --online
adds models.dev metadata and --refresh fetches it again. A source that
cannot answer is a warning on stderr and the scan still exits 0.
roster models lists the models of the roster accesses, or of one
ACCESS, or of the whole catalog with --all; roster status lists every
access with its channels, auth status, model count and sources, then
the active conditions with their source. scan, models
and status take --json.

Conditions are the temporary layer over the policy: off, on, budget,
expire, allow, deny and reset write roster.state.toml next to the
roster (roster.toml becomes roster.state.toml) and never rewrite
roster.toml. off ACCESS switches an access off until --until DATE (the
last day it stays off) or until on ACCESS; budget ACCESS LEVEL sets
prefer, normal, lean, minimal or off; expire ACCESS DATE drops an
access after DATE; allow ACCESS PATTERN... narrows the models of an
access to the globs, deny ACCESS PATTERN... hides them; reset deletes
the state file. An off condition whose until date has passed is
dropped when the file is loaded. A condition command reads the version 2
policy and fails on a v1 roster with the migration hint.

roster migrate converts a v1 roster — spend, [provider], [role],
[route] and [profile] — into a v2 policy and prints it; --write keeps
the v1 file in roster.toml.bak and replaces roster.toml atomically. A
pi or opencode model prefix becomes the access, the route tier the
model class, the route effort the [effort] level, and every model of an
access its models allowlist; a profile becomes a preset. A roster that
is already version 2 is exit code 2 and nothing is written; every other
roster command refuses a v1 file with this command in the hint, and
--profile is gone — the flag was v1 and --preset replaces it.

subcommands:
  show             print the brief (alias of brief)
  brief            print the conditions, chains, budgets and warnings
  explain PLAN     compare the plan header with the brief
  apply ID         write the chains into the plan header
  scan             collect the catalog and write the cache
  models [ACCESS]  list the models of the catalog
  status           list the accesses and the active conditions
  off ACCESS       switch an access off (--until DATE bounds it)
  on ACCESS        lift the off condition of an access
  budget ACCESS LEVEL
                   override the budget of an access
  expire ACCESS DATE
                   drop an access after DATE
  allow ACCESS PATTERN...
                   narrow the models of an access to the globs
  deny ACCESS PATTERN...
                   hide model globs from an access
  reset            delete the condition state
  migrate          convert a v1 roster to version 2

options:
  -h, --help      show this help message and exit
  --roster PATH   roster.toml path (default ~/.config/docflow/roster.toml)
  --until DATE    off: the last day the access stays off (YYYY-MM-DD, UTC)
  --preset NAME   brief and apply: the preset to apply on top of the policy
  --off A,B       brief: accesses switched off for this call only
  --minimal A,B   brief: accesses at the minimal budget for this call only
  --lean A,B      brief: accesses at the lean budget for this call only
  --prefer A,B    brief: accesses at the prefer budget for this call only
  --expires A=DATE
                  brief: expire an access on DATE for this call only
  --only A=GLOB   brief: narrow an access to the model glob for this call
  --json          print the result as JSON
  --dry-run       apply: print the header changes without writing the plan
  --refresh       scan: ignore fresh caches and scan again
  --online        scan: enrich the catalog from models.dev
  --all           models: the whole catalog, not only the roster accesses
  --write         migrate: write roster.toml, keeping roster.toml.bak
`,
	"doctor": `usage: docflow doctor [-h] [--agent {claude,codex,pi,opencode}] [--target DIR]

check that the installed script and skills match this repo

options:
  -h, --help            show this help message and exit
  --agent {claude,codex,pi,opencode}
                        skill directory set (default: claude)
  --target DIR          personal skills directory override
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
