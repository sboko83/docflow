package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/lint"
	"bsv-docflow/internal/release"
	"bsv-docflow/internal/roster"
	"bsv-docflow/internal/roster/web"
	"bsv-docflow/internal/runner"
	docskills "bsv-docflow/internal/skills"
	"bsv-docflow/internal/stats"
	"bsv-docflow/internal/term"
	tuiterm "bsv-docflow/internal/tui"
)

var version = "0.26.0"

var currentExecutable = os.Executable

const runnerEnv = "DOCFLOW_RUNNER_UNIT"

var commands = []commandSpec{
	{name: "init", help: "create docs/docflow.md, plans/, archive/ and a line in AGENTS.md; --skills also copies the skills into the project", args: []argument{choiceOption("--agent", "", "skill directory set (default: claude)", "claude", "claude", "codex", "pi", "opencode"), flag("--skills", "copy skills into the selected project directory")}},
	{name: "status", help: "show the queue, the current plan and its progress"},
	{name: "next", help: "print the id of the next available unit"},
	{name: "prompt", help: "print the prompt for the next unit", args: []argument{positional("id", "", false)}},
	{name: "verify", help: "run the Verify command of the current unit", args: []argument{positional("id", "", true), option("--skip", "REASON", "record the fingerprint without running Verify"), flag("--no-gate", "skip the plan's gate; nothing is recorded")}},
	{name: "done", help: "mark the current unit done and write its Handoff", args: []argument{positional("id", "", true), requiredAppend("--handoff", "HANDOFF", "Handoff item; repeat per item"), appendOption("--decision", "DECISION", "item for ## Decisions; repeat per item")}},
	{name: "commit", help: "commit the current unit with its Commit subject", args: []argument{positional("id", "", true)}},
	{name: "finish", help: "verify, done and commit the current unit in one call", args: []argument{positional("id", "", true), requiredAppend("--handoff", "HANDOFF", "Handoff item; repeat per item"), appendOption("--decision", "DECISION", "item for ## Decisions; repeat per item"), option("--skip", "REASON", "record the fingerprint without running Verify")}},
	{name: "stats", help: "token use per unit: context proxy against the real figures; --runs summarises run events instead", args: []argument{positional("PLAN", "", false), flag("--runs", "per-unit docflow version, attempts, verify failures, replans, blocked ends, time and rework (commits outside docflow that change the unit's lines within 7 days) from the run event streams"), flag("--json", "runs: print the report as JSON")}},
	{name: "block", help: "mark the current unit blocked", args: []argument{positional("id", "", true), requiredOption("--reason", "REASON", ""), choiceOption("--needs", "", "who unblocks the unit (default: human)", "human", "human", "plan")}},
	{name: "unblock", help: "resume a unit blocked for a human", args: []argument{positional("id", "", true), option("--note", "NOTE", "what the person did to lift the block")}},
	{name: "lint", help: "check structure, ids, Depends and the Read budget"},
	{name: "new", help: "create a plan from the template and add it to the Queue", args: []argument{positional("slug", "", true), option("--title", "TITLE", ""), choiceOption("--type", "", "", "feature", "feature", "bugfix", "perf", "module", "refactor")}},
	{name: "activate", help: "make a draft plan active after a clean lint of it", args: []argument{positional("id", "", true)}},
	{name: "close", help: "archive a finished plan and delete it", args: []argument{positional("id", "", true)}},
	{name: "freeze", help: "move a plan from the Queue to Frozen", args: []argument{positional("id", "", true), option("--reason", "TEXT", "why the plan is frozen")}},
	{name: "thaw", help: "move a frozen plan back to the end of the Queue", args: []argument{positional("id", "", true)}},
	{name: "note", help: "write an archive entry without a plan", args: []argument{positional("slug", "", true), option("--title", "TITLE", "")}},
	{name: "migrate", help: "convert docs from previous format versions"},
	{name: "install-skills", help: "symlink skills/* into the selected agent directory, or copy them into the project with --project", args: []argument{choiceOption("--agent", "", "skill directory set (default: claude)", "claude", "claude", "codex", "pi", "opencode"), option("--target", "DIR", "personal skills directory override"), flag("--project", "copy skills/* into the project")}},
	{name: "doctor", help: "check that the installed script and skills match this repo", args: []argument{choiceOption("--agent", "", "skill directory set (default: claude)", "claude", "claude", "codex", "pi", "opencode"), option("--target", "DIR", "personal skills directory override")}},
	{name: "roster", help: "scan model access catalogs, show the route chains per plan role from the personal roster, or write them into a plan header", args: []argument{positional("subcommand", "", true), positional("id", "", false), restPositional("args", ""), option("--profile", "NAME", "removed with the version 1 roster; a version 3 policy uses --preset"), option("--roster", "PATH", "roster.toml path (default ~/.config/docflow/roster.toml)"), option("--addr", "ADDR", "web: address to listen on (default: a random port on 127.0.0.1)"), option("--until", "DATE", "off: the last day the access stays off (YYYY-MM-DD, UTC)"), option("--preset", "NAME", "brief and apply: the preset to apply on top of the policy"), appendOption("--off", "A,B", "brief and apply: accesses switched off for this call only"), appendOption("--minimal", "A,B", "brief and apply: accesses at the minimal budget for this call only"), appendOption("--lean", "A,B", "brief and apply: accesses at the lean budget for this call only"), appendOption("--prefer", "A,B", "brief and apply: accesses at the prefer budget for this call only"), appendOption("--expires", "A=DATE", "brief and apply: expire an access on DATE for this call only"), appendOption("--only", "A=GLOB", "brief and apply: narrow an access to the model glob for this call only"), flag("--json", "print the result as JSON"), flag("--dry-run", "apply: print the header changes without writing the plan"), flag("--refresh", "scan: ignore fresh caches and scan again"), flag("--online", "scan: enrich the catalog from models.dev"), flag("--all", "models: the whole catalog, not only the roster accesses"), flag("--write", "migrate: write roster.toml, keeping roster.toml.bak")}},
	{name: "run", help: "drive the plan unit by unit through a coding agent", args: runArguments()},
	{name: "tui", help: "open the interactive terminal workspace for the queue and plans", args: tuiArguments()},
	{name: "install", help: "install an unpacked release into ~/.docflow and link personal skills", args: []argument{choiceOption("--skills", "", "personal links (global) or project-only skills (project)", "", "global", "project")}},
	{name: "update", help: "download and activate the latest release", args: []argument{flag("--check", "check for a new release without installing it"), choiceOption("--skills", "", "personal links (global) or project-only skills (project)", "", "global", "project")}},
	{name: "uninstall", help: "remove the standalone installation and its personal skill links"},
	{name: "skills-apply", hidden: true, help: "apply the stored skills mode; docflow update runs it through the freshly activated release", args: []argument{choiceOption("--skills", "", "personal links (global) or project-only skills (project)", "", "global", "project")}},
}

type argumentKind int

const (
	positionalArg argumentKind = iota
	restPositionalArg
	stringOption
	integerOption
	booleanOption
	appendOptionKind
)

type argument struct {
	name     string
	metavar  string
	help     string
	kind     argumentKind
	required bool
	optional bool
	choices  []string
	defaultV any
}

type commandSpec struct {
	name string
	help string
	args []argument
	// hidden keeps the command out of the root help and the invalid-choice
	// error; it stays callable and parseable.
	hidden bool
}

type parsedArgs struct {
	command string
	values  map[string]any
}

func positional(name, metavar string, required bool) argument {
	return argument{name: name, metavar: metavar, kind: positionalArg, required: required, optional: !required}
}

// restPositional collects the positional tokens past the fixed ones, so a
// subcommand can take a list (`roster allow ACCESS PATTERN...`).
func restPositional(name, metavar string) argument {
	return argument{name: name, metavar: metavar, kind: restPositionalArg, optional: true}
}

func option(name, metavar, help string) argument {
	return argument{name: name, metavar: metavar, help: help, kind: stringOption}
}

func requiredOption(name, metavar, help string) argument {
	a := option(name, metavar, help)
	a.required = true
	return a
}

func flag(name, help string) argument {
	return argument{name: name, help: help, kind: booleanOption, defaultV: false}
}

func appendOption(name, metavar, help string) argument {
	return argument{name: name, metavar: metavar, help: help, kind: appendOptionKind}
}

func requiredAppend(name, metavar, help string) argument {
	a := appendOption(name, metavar, help)
	a.required = true
	return a
}

func choiceOption(name, metavar, help, defaultValue string, choices ...string) argument {
	a := option(name, metavar, help)
	a.choices = choices
	a.defaultV = defaultValue
	return a
}

func tuiArguments() []argument {
	return []argument{
		integer("--attempts", "N", "agent calls per unit (default 3)", intValue(3)),
		integer("--replans", "N", "auto-replan calls per phase (default 3; 0 disables)", intValue(3)),
		option("--roster", "PATH", "roster.toml path of the roster screen (default ~/.config/docflow/roster.toml)"),
	}
}

func runArguments() []argument {
	return []argument{
		option("--config", "PATH", "agents.toml (default ~/.config/docflow/agents.toml); plans without executors only"),
		option("--agent", "NAME", "agent profile for every unit (default: by tier); plans without executors only"),
		option("--model", "M", "model id or tier for every unit; plans without executors only"),
		option("--effort", "E", "reasoning effort for every unit; plans without executors only"),
		integer("--attempts", "N", "agent calls per unit (default 3)", intValue(3)),
		integer("--replans", "N", "auto-replan calls per phase (default 3, at most 2 per unit; 0 disables)", intValue(3)),
		integer("--max-units", "N", "stop after N units", nil),
		flag("--no-wait", "stop instead of waiting out a usage limit"),
		flag("--no-notify", "skip the end-of-run desktop notification"),
		flag("--close", "ask an agent to close the plan when ready"),
		flag("--dry-run", "show the next unit and the agent command"),
		flag("--all", "with --dry-run: the agent, model and effort of every unit still to do"),
		option("--plan", "ID", "with --dry-run: read this draft, active or frozen plan instead of the Queue"),
		flag("--print-config", "print the built-in agents.toml"),
		flag("--yes", "answer the start confirmation (permissions full, prerequisites) without asking"),
		flagPair("-v", "--verbose", "add per-unit details: tokens, quota, eta"),
		flag("--stop", "ask the active runner to stop after its current unit"),
	}
}

func integer(name, metavar, help string, defaultValue any) argument {
	return argument{name: name, metavar: metavar, help: help, kind: integerOption, defaultV: defaultValue}
}

func intValue(value int) any { return value }

func flagPair(short, long, help string) argument {
	a := flag(long, help)
	a.name = short + " " + long
	return a
}

const rootHelp = `usage: docflow [-h] [--version] command ...

docflow: plans split into one-session units, handoff, decision archive.

positional arguments:
  command
    init            create docs/docflow.md, plans/, archive/ and a line in
                    AGENTS.md; --skills also copies the skills into the
                    project
    status          show the queue, the current plan and its progress
    next            print the id of the next available unit
    prompt          print the prompt for the next unit
    verify          run the Verify command of the current unit
    done            mark the current unit done and write its Handoff
    commit          commit the current unit with its Commit subject
    finish          verify, done and commit the current unit in one call
    stats           token use per unit: context proxy against the real figures; --runs: run events
    block           mark the current unit blocked
    unblock         resume a unit blocked for a human
    lint            check structure, ids, Depends and the Read budget
    new             create a plan from the template and add it to the Queue
    activate        make a draft plan active after a clean lint of it
    close           archive a finished plan and delete it
    freeze          move a plan from the Queue to Frozen
    thaw            move a frozen plan back to the end of the Queue
    note            write an archive entry without a plan
    migrate         convert docs from previous format versions
    install-skills  symlink skills/* into the selected agent directory, or
                    copy them into the project with --project
    doctor          check that the installed script and skills match this repo
    roster          scan model access catalogs, show the route chains per
                    plan role from the personal roster, or write them into
                    a plan header
    run             drive the plan unit by unit through a coding agent
    tui             open the interactive terminal workspace for the queue and
                    plans
    install         install an unpacked release into ~/.docflow and link
                    personal skills
    update          download and activate the latest release
    uninstall       remove the standalone installation and its personal
                    skill links

options:
  -h, --help        show this help message and exit
  --version         print the version and exit
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(argv []string, stdout, stderr io.Writer) int {
	// run_start records the version for every runner entry: run and tui.
	runner.Version = version
	parsed, code := parseArgs(argv, stdout, stderr)
	if code != clierrors.EXIT_OK {
		return code
	}
	if parsed == nil {
		return clierrors.EXIT_OK
	}
	defer checkReleaseUpdate(parsed.command, stderr)
	if runnerOnly(parsed.command) {
		if unit := os.Getenv(runnerEnv); unit != "" {
			fmt.Fprintf(stderr, "%s: the runner drives %s and runs it itself; end your reply with the result JSON\n", parsed.command, unit)
			return clierrors.EXIT_ORDER
		}
	}
	handler := handlers[parsed.command]
	if parsed.command == "run" {
		handler = func(parsed *parsedArgs) error { return handleRun(parsed, stdout, stderr) }
	}
	if parsed.command == "update" {
		handler = releaseCommand(func(parsed *parsedArgs) error { return handleUpdate(parsed, stderr) })
	}
	if parsed.command == "tui" {
		handler = func(parsed *parsedArgs) error { return handleTUI(parsed, stdout) }
	}
	if handler == nil {
		fmt.Fprintf(stderr, "docflow: %s is not implemented yet\n", parsed.command)
		return clierrors.EXIT_FORMAT
	}
	if err := handler(parsed); err != nil {
		if result, ok := err.(commandResult); ok {
			fmt.Fprint(stdout, result.stdout)
			fmt.Fprint(stderr, result.stderr)
			return result.code
		}
		fmt.Fprintln(stderr, err)
		if coded, ok := err.(interface{ ExitCode() int }); ok {
			return coded.ExitCode()
		}
		return clierrors.EXIT_FORMAT
	}
	return clierrors.EXIT_OK
}

type handlerFunc func(*parsedArgs) error

type commandResult struct {
	code   int
	stdout string
	stderr string
}

func (r commandResult) Error() string { return "" }
func (r commandResult) ExitCode() int { return r.code }

// handlers is the Go counterpart of the Python CLI's HANDLERS dispatch table.
var handlers = makeHandlers()

func makeHandlers() map[string]handlerFunc {
	out := make(map[string]handlerFunc, len(commands))
	for _, spec := range commands {
		name := spec.name
		out[name] = func(parsed *parsedArgs) error {
			return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s is not implemented yet", name), Code: clierrors.EXIT_FORMAT}
		}
	}
	out["status"] = handleStatus
	out["next"] = handleNext
	out["prompt"] = handlePrompt
	out["verify"] = handleVerify
	out["done"] = handleDone
	out["commit"] = handleCommit
	out["finish"] = handleFinish
	out["block"] = handleBlock
	out["unblock"] = handleUnblock
	out["lint"] = handleLint
	out["init"] = handleInit
	out["new"] = handleNew
	out["activate"] = handleActivate
	out["close"] = handleClose
	out["freeze"] = handleFreeze
	out["thaw"] = handleThaw
	out["note"] = handleNote
	out["migrate"] = handleMigrate
	out["install-skills"] = handleInstallSkills
	out["doctor"] = handleDoctor
	out["roster"] = handleRoster
	out["stats"] = handleStats
	out["tui"] = func(parsed *parsedArgs) error { return handleTUI(parsed, os.Stdout) }
	out["install"] = releaseCommand(handleInstall)
	out["update"] = releaseCommand(func(parsed *parsedArgs) error { return handleUpdate(parsed, os.Stderr) })
	out["uninstall"] = releaseCommand(handleUninstall)
	out["skills-apply"] = releaseCommand(handleSkillsApply)
	return out
}

var currentSkillDirectory = docskills.CurrentSkillDir

func handleRun(parsed *parsedArgs, stdout, stderr io.Writer) error {
	v := parsed.values
	str := func(key string) string { value, _ := v[key].(string); return value }
	flag := func(key string) bool { value, _ := v[key].(bool); return value }
	attempts, _ := v["--attempts"].(int)
	replans, _ := v["--replans"].(int)
	var maxUnits *int
	if n, ok := v["--max-units"].(int); ok {
		maxUnits = &n
	}
	args := runner.Args{RunArgs: agents.RunArgs{Config: str("--config"), Agent: str("--agent"), Model: str("--model"), Effort: str("--effort"), Plan: str("--plan")}, Attempts: attempts, Replans: replans, MaxUnits: maxUnits, NoWait: flag("--no-wait"), NoNotify: flag("--no-notify"), Close: flag("--close"), DryRun: flag("--dry-run"), All: flag("--all"), PrintConfig: flag("--print-config"), Yes: flag("--yes"), Verbose: flag("-v --verbose"), Stop: flag("--stop")}
	root := ""
	if !args.PrintConfig {
		var err error
		root, err = projectRoot()
		if err != nil {
			return err
		}
	}
	// A missing checkout only disables the advisory skill check.
	source, _ := currentSkillDirectory()
	code, err := runner.Run(root, args, stdout, stderr, source)
	if err != nil {
		return err
	}
	return commandResult{code: code}
}

func handleTUI(parsed *parsedArgs, stdout io.Writer) error {
	attempts, _ := parsed.values["--attempts"].(int)
	replans, _ := parsed.values["--replans"].(int)
	if attempts < 1 {
		return clierrors.DocflowError{Message: "docflow tui: --attempts must be at least 1", Code: clierrors.EXIT_FORMAT}
	}
	if replans < 0 {
		return clierrors.DocflowError{Message: "docflow tui: --replans must not be negative", Code: clierrors.EXIT_FORMAT}
	}
	root, err := projectRoot()
	if err != nil {
		return err
	}
	if file, ok := stdout.(*os.File); ok {
		term.EnableVTProcessing(file)
	}
	// A missing checkout only disables the advisory skill check.
	source, _ := currentSkillDirectory()
	return tuiterm.Run(root, os.Stdin, stdout, tuiterm.WithSkillDirectory(source), tuiterm.WithRunLimits(attempts, replans), tuiterm.WithRosterPath(rosterString(parsed, "--roster")))
}

func handleRoster(parsed *parsedArgs) error {
	subcommand, _ := parsed.values["subcommand"].(string)
	// --profile belonged to the version 1 roster. It stays a parsed flag so
	// the error names its replacement instead of an unrecognized word.
	if _, given := parsed.values["--profile"]; given {
		return clierrors.DocflowError{
			Message: fmt.Sprintf("docflow: roster: --profile was removed with the version 1 roster; a version %d roster uses --preset NAME", roster.PolicyVersion),
			Code:    clierrors.EXIT_FORMAT,
		}
	}
	// The commands take their values as positionals; every other one takes
	// none, so a stray token stays an error instead of being ignored.
	rest, _ := parsed.values["args"].([]string)
	switch subcommand {
	case "budget", "expire", "allow", "deny":
	default:
		if len(rest) > 0 {
			return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster %s: unrecognized arguments: %s", subcommand, strings.Join(rest, " ")), Code: clierrors.EXIT_FORMAT}
		}
	}
	// The one-shot condition flags belong to every command that resolves
	// the roster: brief, show, explain and apply share the in-memory
	// overlay. A condition flag on a command that does not resolve says so
	// instead of being ignored.
	if flags := briefFlagNames(parsed); len(flags) > 0 {
		switch subcommand {
		case "brief", "show", "explain", "apply":
		default:
			return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster %s: %s belongs to roster brief", subcommand, strings.Join(flags, ", ")), Code: clierrors.EXIT_FORMAT}
		}
	}
	switch subcommand {
	case "show", "brief":
		if id, _ := parsed.values["id"].(string); id != "" {
			return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster %s: unrecognized arguments: %s", subcommand, id), Code: clierrors.EXIT_FORMAT}
		}
		return rosterBriefCommand(parsed)
	case "apply":
		planID, _ := parsed.values["id"].(string)
		if planID == "" {
			return clierrors.DocflowError{Message: "docflow: roster apply: no plan id", Code: clierrors.EXIT_FORMAT}
		}
		root, err := projectRoot()
		if err != nil {
			return err
		}
		return rosterApply(parsed, root, planID)
	case "scan":
		return rosterScan(parsed)
	case "models":
		return rosterModels(parsed)
	case "status":
		return rosterStatus(parsed)
	case "off", "on", "budget", "expire", "allow", "deny", "reset":
		return rosterCondition(parsed)
	case "migrate":
		return rosterMigrate(parsed)
	case "web":
		return rosterWeb(parsed)
	case "explain":
		return rosterExplain(parsed)
	default:
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: unknown roster subcommand '%s' (known: show, brief, explain, apply, scan, models, status, off, on, budget, expire, allow, deny, reset, migrate, web)", subcommand), Code: clierrors.EXIT_FORMAT}
	}
}

// rosterWeb serves the roster workspace and its operations on 127.0.0.1
// until SIGINT or SIGTERM; the browser editor is the client. The project
// variants layer follows the working directory, the way every roster
// command reads it.
func rosterWeb(parsed *parsedArgs) error {
	addr, _ := parsed.values["--addr"].(string)
	return web.Run(addr, rosterString(parsed, "--roster"), os.Stdout)
}

// rosterScan builds the access catalog, writes the cache and prints the
// access summary. A source problem is a warning next to a partial
// catalog: the success code stays 0.
func rosterScan(parsed *parsedArgs) error {
	// The policy is read before the scan: a v1 or broken roster fails
	// before any CLI runs and before the cache is rewritten.
	names, err := roster.RosterAccessNames(rosterString(parsed, "--roster"))
	if err != nil {
		return err
	}
	catalog, warnings, err := roster.ScanCatalog(roster.ScanOptions{Refresh: rosterFlag(parsed, "--refresh"), Online: rosterFlag(parsed, "--online")})
	if err != nil {
		return err
	}
	stderr := rosterWarnings(warnings) + rosterMissingAccesses(names, catalog)
	if rosterFlag(parsed, "--json") {
		return rosterJSON(catalog, stderr)
	}
	return commandResult{code: clierrors.EXIT_OK, stdout: catalog.AccessTable(), stderr: stderr}
}

// rosterModels lists the models of the catalog cache: the ACCESS argument,
// the whole catalog with --all, or the accesses the roster names.
func rosterModels(parsed *parsedArgs) error {
	catalog, err := roster.LoadCatalog("")
	if err != nil {
		return err
	}
	var stderr strings.Builder
	if len(catalog.Accesses) == 0 {
		stderr.WriteString("docflow: roster: the catalog cache is empty; run docflow roster scan\n")
	}
	filter, err := rosterModelFilter(parsed, catalog, &stderr)
	if err != nil {
		return err
	}
	models := catalog.ModelRows(filter)
	if rosterFlag(parsed, "--json") {
		return rosterJSON(roster.ModelReport{Models: models}, stderr.String())
	}
	return commandResult{code: clierrors.EXIT_OK, stdout: roster.ModelTable(models), stderr: stderr.String()}
}

// rosterStatus prints the accesses of the catalog cache with their
// channels, auth status, model count and sources, then the active
// conditions with their source.
func rosterStatus(parsed *parsedArgs) error {
	catalog, err := roster.LoadCatalog("")
	if err != nil {
		return err
	}
	stderr := ""
	if len(catalog.Accesses) == 0 {
		stderr = "docflow: roster: the catalog cache is empty; run docflow roster scan\n"
	}
	// A missing roster has no conditions: the catalog commands keep
	// working without one. A v1 file fails here with the migration hint.
	workspace, err := roster.LoadPolicyState(rosterString(parsed, "--roster"), roster.ProjectRoot())
	if err != nil {
		return err
	}
	var conditions []roster.Condition
	if workspace.Policy != nil {
		conditions = workspace.Conditions
	}
	if rosterFlag(parsed, "--json") {
		report := catalog.Report()
		report.Conditions = conditions
		return rosterJSON(report, stderr)
	}
	stdout := catalog.StatusHeader() + "\n" + catalog.AccessTable()
	if workspace.Policy != nil {
		stdout += rosterConditionsText(workspace, conditions)
	}
	return commandResult{code: clierrors.EXIT_OK, stdout: stdout, stderr: stderr}
}

// rosterConditionsText renders the conditions section of the status: the
// two files conditions come from, then one row per condition; no
// conditions stay one line instead of an empty table.
func rosterConditionsText(workspace *roster.Workspace, conditions []roster.Condition) string {
	if len(conditions) == 0 {
		return "conditions: none\n"
	}
	return fmt.Sprintf("conditions (roster %s, state %s):\n", workspace.PolicyPath, workspace.StatePath) + roster.ConditionTable(conditions)
}

// rosterCondition runs the commands that write the condition state:
// off, on, budget, expire, allow, deny and reset write only
// roster.state.toml, never roster.toml. Every condition goes through the
// workspace operations, which check the access against the policy and save
// through State.Save, so a typo fails instead of sitting dead in the file.
func rosterCondition(parsed *parsedArgs) error {
	subcommand, _ := parsed.values["subcommand"].(string)
	access, _ := parsed.values["id"].(string)
	rest, _ := parsed.values["args"].([]string)
	statePath := roster.StatePath(rosterString(parsed, "--roster"))
	if subcommand == "reset" {
		// Reset reads no policy: dropping the conditions is the way out of a
		// broken or a version 1 roster too.
		workspace := &roster.Workspace{StatePath: statePath}
		if err := workspace.Reset(); err != nil {
			return err
		}
		if rosterFlag(parsed, "--json") {
			return rosterJSON(roster.ConditionsReport{State: statePath, Conditions: []roster.Condition{}}, "")
		}
		return commandResult{code: clierrors.EXIT_OK, stdout: "conditions: reset\n"}
	}
	workspace, err := roster.LoadPolicyState(rosterString(parsed, "--roster"), roster.ProjectRoot())
	if err != nil {
		return err
	}
	if access == "" {
		return rosterArgumentError(subcommand, "ACCESS")
	}
	var line string
	switch subcommand {
	case "off":
		var until time.Time
		if text := rosterString(parsed, "--until"); text != "" {
			if until, err = roster.ParseDay(text); err != nil {
				return err
			}
		}
		if err := workspace.SetOff(access, until); err != nil {
			return err
		}
		line = access + ": off"
		if !until.IsZero() {
			line += " until " + until.Format("2006-01-02")
		}
	case "on":
		if err := workspace.SetOn(access); err != nil {
			return err
		}
		line = access + ": on"
	case "budget":
		level, err := rosterFirstArgument(subcommand, rest, "LEVEL")
		if err != nil {
			return err
		}
		if err := workspace.SetBudget(access, level); err != nil {
			return err
		}
		line = access + ": budget " + level
	case "expire":
		text, err := rosterFirstArgument(subcommand, rest, "DATE")
		if err != nil {
			return err
		}
		day, err := roster.ParseDay(text)
		if err != nil {
			return err
		}
		if err := workspace.SetExpires(access, day); err != nil {
			return err
		}
		line = access + ": expires " + day.Format("2006-01-02")
	case "allow", "deny":
		if len(rest) == 0 {
			return rosterArgumentError(subcommand, "PATTERN")
		}
		if subcommand == "allow" {
			err = workspace.Allow(access, rest)
		} else {
			err = workspace.Deny(access, rest)
		}
		if err != nil {
			return err
		}
		line = access + ": " + subcommand + " " + strings.Join(rest, " ")
	}
	if rosterFlag(parsed, "--json") {
		return rosterJSON(roster.NewConditionsReport(workspace.Policy, workspace.State), "")
	}
	return commandResult{code: clierrors.EXIT_OK, stdout: line + "\n"}
}

// rosterMigrate converts a v1 roster to v2: without --write it prints the
// converted policy, with --write it keeps the v1 text in roster.toml.bak
// and replaces roster.toml atomically. A roster that is not v1 — already
// migrated included — is exit code 2 and nothing is written.
func rosterMigrate(parsed *parsedArgs) error {
	// migrate takes no positional argument; the shared roster spec puts a
	// stray token into the id slot, so refuse it here.
	if id, _ := parsed.values["id"].(string); id != "" {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster migrate: unrecognized arguments: %s", id), Code: clierrors.EXIT_FORMAT}
	}
	result, err := roster.MigrateFile(rosterString(parsed, "--roster"), rosterFlag(parsed, "--write"))
	if err != nil {
		return err
	}
	if !rosterFlag(parsed, "--write") {
		return commandResult{code: clierrors.EXIT_OK, stdout: result.Text}
	}
	return commandResult{code: clierrors.EXIT_OK, stdout: fmt.Sprintf("migrated %s to version %d (backup: %s)\n", result.Path, roster.PolicyVersion, result.Backup)}
}

// rosterFirstArgument picks the single value a subcommand expects after
// the access name.
func rosterFirstArgument(subcommand string, rest []string, what string) (string, error) {
	if len(rest) == 0 {
		return "", rosterArgumentError(subcommand, what)
	}
	if len(rest) > 1 {
		return "", clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster %s: unrecognized arguments: %s", subcommand, strings.Join(rest[1:], " ")), Code: clierrors.EXIT_FORMAT}
	}
	return rest[0], nil
}

// rosterArgumentError names the missing argument of a condition command.
func rosterArgumentError(subcommand, what string) error {
	return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster %s: %s is required", subcommand, what), Code: clierrors.EXIT_FORMAT}
}

// rosterModelFilter picks the accesses `roster models` lists. A missing
// roster or one without accesses falls back to the whole catalog with a
// note on stderr, so the command stays useful before a roster exists.
func rosterModelFilter(parsed *parsedArgs, catalog *roster.Catalog, stderr *strings.Builder) ([]string, error) {
	access := rosterString(parsed, "id")
	if access != "" {
		// An empty cache knows no access yet; the empty-cache note above
		// already says why the table is empty.
		if len(catalog.Accesses) == 0 {
			return []string{access}, nil
		}
		if !catalogHasAccess(catalog, access) {
			known := make([]string, 0, len(catalog.Accesses))
			for _, entry := range catalog.Accesses {
				known = append(known, entry.Access)
			}
			return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster models: unknown access '%s' (known: %s)", access, strings.Join(known, ", ")), Code: clierrors.EXIT_FORMAT}
		}
		return []string{access}, nil
	}
	if rosterFlag(parsed, "--all") {
		return nil, nil
	}
	path := rosterString(parsed, "--roster")
	if path == "" {
		path = roster.DefaultPath()
	}
	names, err := roster.RosterAccessNames(path)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		fmt.Fprintf(stderr, "docflow: roster: %s lists no accesses; showing the whole catalog (roster models --all)\n", path)
		return nil, nil
	}
	return names, nil
}

// catalogHasAccess reports whether the catalog holds an access.
func catalogHasAccess(catalog *roster.Catalog, name string) bool {
	for _, entry := range catalog.Accesses {
		if entry.Access == name {
			return true
		}
	}
	return false
}

// rosterMissingAccesses warns about roster accesses no channel of this
// machine reaches: the roster names them, the scan did not find them.
func rosterMissingAccesses(names []string, catalog *roster.Catalog) string {
	var stderr strings.Builder
	for _, name := range names {
		if !catalogHasAccess(catalog, name) {
			fmt.Fprintf(&stderr, "docflow: roster: access '%s' is not in the catalog: no channel of this machine reaches it\n", name)
		}
	}
	return stderr.String()
}

// rosterWarnings renders the scan warnings as stderr lines; every warning
// already names its channel and carries no response body or key.
func rosterWarnings(warnings []string) string {
	var stderr strings.Builder
	for _, warning := range warnings {
		stderr.WriteString("docflow: roster: " + warning + "\n")
	}
	return stderr.String()
}

// rosterJSON renders a roster command result as JSON on stdout.
func rosterJSON(value any, stderr string) error {
	return rosterJSONResult(value, stderr, clierrors.EXIT_OK)
}

// rosterJSONResult renders a roster command result as JSON with the exit
// code the command decided: roster apply reports a failed lint with code
// 2 and no changes made instead of turning it into a success.
func rosterJSONResult(value any, stderr string, code int) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	return commandResult{code: code, stdout: string(data) + "\n", stderr: stderr}
}

// rosterFlag reads a boolean flag of the roster command.
func rosterFlag(parsed *parsedArgs, name string) bool {
	value, _ := parsed.values[name].(bool)
	return value
}

// rosterString reads a string value of the roster command.
func rosterString(parsed *parsedArgs, name string) string {
	value, _ := parsed.values[name].(string)
	return value
}

func handleStats(parsed *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	planID, _ := parsed.values["PLAN"].(string)
	asJSON, _ := parsed.values["--json"].(bool)
	var result stats.Result
	if runs, _ := parsed.values["--runs"].(bool); runs {
		result, err = stats.RunsCommand(root, planID, asJSON)
	} else if asJSON {
		return clierrors.DocflowError{Message: "docflow stats: --json belongs to --runs", Code: clierrors.EXIT_FORMAT}
	} else {
		result, err = stats.Command(root, planID)
	}
	if err != nil {
		return err
	}
	return commandResult{code: result.Code, stdout: result.Stdout, stderr: result.Stderr}
}

func projectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return format.FindRoot(cwd)
}

func cycleResult(result cycle.Result) error {
	return commandResult{code: result.Code, stdout: result.Stdout, stderr: result.Stderr}
}

func handleStatus(_ *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	if err := lint.CheckVersion(root); err != nil {
		return err
	}
	report, err := lint.Lint(root)
	if err != nil {
		return err
	}
	errorCount := len(report.Errors())
	warnings := len(report.Issues) - errorCount
	lintLine := "lint: ok"
	if errorCount > 0 {
		lintLine = fmt.Sprintf("lint: %d errors — run docflow lint", errorCount)
	} else if warnings > 0 {
		lintLine = fmt.Sprintf("lint: %d warnings", warnings)
	}
	result, err := cycle.Status(root, lintLine)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handleNext(_ *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	if err := lint.CheckVersion(root); err != nil {
		return err
	}
	result, err := cycle.Next(root)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handlePrompt(parsed *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	if err := lint.CheckVersion(root); err != nil {
		return err
	}
	unitID, _ := parsed.values["id"].(string)
	result, err := cycle.Prompt(root, unitID, lint.ProjectRepos)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handleVerify(parsed *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	unitID, _ := parsed.values["id"].(string)
	skip, hasSkip := parsed.values["--skip"].(string)
	var skipValue *string
	if hasSkip {
		skipValue = &skip
	}
	noGate, _ := parsed.values["--no-gate"].(bool)
	result, err := cycle.Verify(root, unitID, skipValue, noGate, lint.ProjectRepos, lint.CheckVersion)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handleDone(parsed *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	unitID, _ := parsed.values["id"].(string)
	handoff, _ := parsed.values["--handoff"].([]string)
	decisions, _ := parsed.values["--decision"].([]string)
	result, err := cycle.Done(root, unitID, handoff, decisions, lint.ProjectRepos)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handleCommit(parsed *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	unitID, _ := parsed.values["id"].(string)
	result, err := cycle.Commit(root, unitID, lint.ProjectRepos)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handleFinish(parsed *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	unitID, _ := parsed.values["id"].(string)
	handoff, _ := parsed.values["--handoff"].([]string)
	decisions, _ := parsed.values["--decision"].([]string)
	skip, hasSkip := parsed.values["--skip"].(string)
	var skipValue *string
	if hasSkip {
		skipValue = &skip
	}
	result, err := cycle.Finish(root, unitID, handoff, decisions, skipValue, lint.ProjectRepos, lint.CheckVersion)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handleBlock(parsed *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	unitID, _ := parsed.values["id"].(string)
	reason, _ := parsed.values["--reason"].(string)
	needs, _ := parsed.values["--needs"].(string)
	result, err := cycle.Block(root, unitID, reason, needs)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handleUnblock(parsed *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	unitID, _ := parsed.values["id"].(string)
	note, _ := parsed.values["--note"].(string)
	result, err := cycle.Unblock(root, unitID, note)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handleLint(_ *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	if err := lint.CheckVersion(root); err != nil {
		return err
	}
	var stdout bytes.Buffer
	code, err := lint.CmdLint(root, &stdout)
	if err != nil {
		return err
	}
	return commandResult{code: code, stdout: stdout.String()}
}

func handleInit(parsed *parsedArgs) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	agent, _ := parsed.values["--agent"].(string)
	withSkills, _ := parsed.values["--skills"].(bool)
	skillDir := ""
	if withSkills {
		skillDir, err = currentSkillDirectory()
		if err != nil {
			return err
		}
	}
	result, err := cycle.Init(root, agent, withSkills, skillDir)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handleNew(parsed *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	slug, _ := parsed.values["slug"].(string)
	title, _ := parsed.values["--title"].(string)
	planType, _ := parsed.values["--type"].(string)
	result, err := cycle.New(root, slug, title, planType)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handleActivate(parsed *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	planID, _ := parsed.values["id"].(string)
	check := func(root string, plan *format.Plan) ([]string, bool, error) {
		return lint.ActivationIssues(root, plan)
	}
	result, err := cycle.Activate(root, planID, check)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handleClose(parsed *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	planID, _ := parsed.values["id"].(string)
	result, err := cycle.Close(root, planID)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handleFreeze(parsed *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	planID, _ := parsed.values["id"].(string)
	reason, _ := parsed.values["--reason"].(string)
	result, err := cycle.Freeze(root, planID, reason, lint.ProjectRepos)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handleThaw(parsed *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	planID, _ := parsed.values["id"].(string)
	check := func(root string, plan *format.Plan) ([]string, error) {
		return lint.ReferenceIssues(root, plan)
	}
	result, err := cycle.Thaw(root, planID, check)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handleNote(parsed *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	slug, _ := parsed.values["slug"].(string)
	title, _ := parsed.values["--title"].(string)
	result, err := cycle.Note(root, slug, title)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handleMigrate(_ *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	result, err := cycle.Migrate(root)
	if err != nil {
		return err
	}
	return cycleResult(result)
}

func handleInstallSkills(parsed *parsedArgs) error {
	skillDir, err := currentSkillDirectory()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	agent, _ := parsed.values["--agent"].(string)
	target, _ := parsed.values["--target"].(string)
	project, _ := parsed.values["--project"].(bool)
	result, err := docskills.InstallSkills(skillDir, cwd, docskills.InstallOptions{
		Agent: agent, Target: target, Project: project,
	})
	if err != nil {
		return err
	}
	return commandResult{code: result.Code, stdout: result.Stdout, stderr: result.Stderr}
}

func handleDoctor(parsed *parsedArgs) error {
	skillDir, err := currentSkillDirectory()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	executable, err := currentExecutable()
	if err != nil {
		return err
	}
	agent, _ := parsed.values["--agent"].(string)
	target, _ := parsed.values["--target"].(string)
	result, err := docskills.Doctor(skillDir, cwd, executable, docskills.DoctorOptions{
		Agent: agent, Target: target,
	})
	if err != nil {
		return err
	}
	return commandResult{code: result.Code, stdout: result.Stdout, stderr: result.Stderr}
}

func runnerOnly(name string) bool {
	return name == "finish" || name == "done" || name == "commit" || name == "block" || name == "unblock"
}

func parseArgs(argv []string, stdout, stderr io.Writer) (*parsedArgs, int) {
	if len(argv) == 0 {
		return nil, parseError(stderr, "docflow [-h] [--version] command ...", "docflow", "the following arguments are required: command")
	}
	if argv[0] == "-h" || argv[0] == "--help" {
		fmt.Fprint(stdout, rootHelp)
		return nil, clierrors.EXIT_OK
	}
	if argv[0] == "--version" || argv[0] == "-V" {
		executable, _ := currentExecutable()
		fmt.Fprintln(stdout, versionString(executable))
		return nil, clierrors.EXIT_OK
	}
	if strings.HasPrefix(argv[0], "-") {
		return nil, parseError(stderr, "docflow [-h] [--version] command ...", "docflow", "unrecognized arguments: "+argv[0])
	}
	spec, found := commandByName(argv[0])
	if !found {
		return nil, parseError(stderr, "docflow [-h] [--version] command ...", "docflow", fmt.Sprintf("argument command: invalid choice: %s (choose from %s)", repr(argv[0]), commandNames()))
	}
	prog := "docflow " + spec.name
	parsed, errText, showHelp := parseCommand(spec, argv[1:])
	if showHelp {
		fmt.Fprint(stdout, commandHelp(spec, prog))
		return nil, clierrors.EXIT_OK
	}
	if errText != "" {
		if strings.HasPrefix(errText, "unrecognized arguments:") {
			return nil, parseError(stderr, "docflow [-h] [--version] command ...", "docflow", errText)
		}
		return nil, parseError(stderr, usage(spec, prog), prog, errText)
	}
	return parsed, clierrors.EXIT_OK
}

func parseCommand(spec commandSpec, argv []string) (*parsedArgs, string, bool) {
	values := make(map[string]any)
	seen := make(map[string]bool)
	for _, arg := range spec.args {
		if arg.defaultV != nil {
			values[arg.name] = arg.defaultV
		}
	}
	positionals := make([]argument, 0)
	options := make(map[string]argument)
	for _, arg := range spec.args {
		if arg.kind == positionalArg || arg.kind == restPositionalArg {
			positionals = append(positionals, arg)
			continue
		}
		for _, name := range strings.Fields(arg.name) {
			options[name] = arg
		}
	}
	posIndex := 0
	unknown := make([]string, 0)
	assign := func(value string) {
		if posIndex >= len(positionals) {
			unknown = append(unknown, value)
			return
		}
		arg := positionals[posIndex]
		if arg.kind == restPositionalArg {
			current, _ := values[arg.name].([]string)
			values[arg.name], seen[arg.name] = append(current, value), true
			return
		}
		values[arg.name], seen[arg.name] = value, true
		posIndex++
	}
	for index := 0; index < len(argv); index++ {
		token := argv[index]
		if token == "--" {
			for _, value := range argv[index+1:] {
				assign(value)
			}
			break
		}
		if token == "-h" || token == "--help" {
			return nil, "", true
		}
		name, attached, hasAttached := strings.Cut(token, "=")
		if strings.HasPrefix(name, "-") {
			arg, ok := options[name]
			if !ok {
				unknown = append(unknown, token)
				continue
			}
			seen[arg.name] = true
			if arg.kind == booleanOption {
				if hasAttached {
					return nil, fmt.Sprintf("argument %s: ignored explicit argument %s", name, repr(attached)), false
				}
				values[arg.name] = true
				continue
			}
			value := attached
			if !hasAttached {
				if index+1 >= len(argv) || (strings.HasPrefix(argv[index+1], "-") && !isIntegerValue(argv[index+1], arg.kind)) {
					return nil, fmt.Sprintf("argument %s: expected one argument", name), false
				}
				index++
				value = argv[index]
			}
			if arg.kind == integerOption {
				n, err := parseInt(value)
				if err != nil {
					return nil, fmt.Sprintf("argument %s: invalid int value: %s", name, repr(value)), false
				}
				values[arg.name] = n
			} else if arg.kind == appendOptionKind {
				current, _ := values[arg.name].([]string)
				values[arg.name] = append(current, value)
			} else {
				values[arg.name] = value
			}
			if len(arg.choices) > 0 && !contains(arg.choices, value) {
				return nil, fmt.Sprintf("argument %s: invalid choice: %s (choose from %s)", name, repr(value), reprChoices(arg.choices)), false
			}
			continue
		}
		assign(token)
	}
	if len(unknown) > 0 {
		return nil, "unrecognized arguments: " + strings.Join(unknown, " "), false
	}
	missing := make([]string, 0)
	for _, arg := range positionals {
		if arg.required && !seen[arg.name] {
			missing = append(missing, arg.name)
		}
	}
	for _, arg := range spec.args {
		if arg.kind != positionalArg && arg.required && !seen[arg.name] {
			missing = append(missing, strings.Fields(arg.name)[0])
		}
	}
	if len(missing) > 0 {
		return nil, "the following arguments are required: " + strings.Join(missing, ", "), false
	}
	return &parsedArgs{command: spec.name, values: values}, "", false
}

func parseError(stderr io.Writer, usageText, prog, message string) int {
	if strings.HasPrefix(usageText, "usage: ") {
		fmt.Fprintln(stderr, usageText)
	} else {
		fmt.Fprintf(stderr, "usage: %s\n", usageText)
	}
	fmt.Fprintf(stderr, "%s: error: %s\n", prog, message)
	return clierrors.EXIT_FORMAT
}

func commandByName(name string) (commandSpec, bool) {
	for _, spec := range commands {
		if spec.name == name {
			return spec, true
		}
	}
	return commandSpec{}, false
}

func commandNames() string {
	names := make([]string, 0, len(commands))
	for _, spec := range commands {
		if spec.hidden {
			continue
		}
		names = append(names, spec.name)
	}
	return strings.Join(names, ", ")
}

func usage(spec commandSpec, _ string) string {
	text := helpTexts[spec.name]
	if text == "" {
		return "usage: docflow " + spec.name + " [-h]"
	}
	return strings.SplitN(text, "\n\n", 2)[0]
}

func commandHelp(spec commandSpec, _ string) string {
	return helpTexts[spec.name]
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func repr(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "\\'") + "'"
}

func reprChoices(values []string) string {
	return strings.Join(values, ", ")
}

func parseInt(value string) (int, error) {
	return strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(value), "_", ""))
}

func isIntegerValue(value string, kind argumentKind) bool {
	if kind != integerOption {
		return false
	}
	_, err := parseInt(value)
	return err == nil
}

func versionString(executable string) string {
	repo, ok := repositoryForExecutable(executable)
	if !ok {
		return version
	}
	sha, err := gitOutput(repo, "rev-parse", "--short", "HEAD")
	if err != nil {
		return version
	}
	dirty := ""
	if status, err := gitOutput(repo, "status", "--porcelain"); err == nil && strings.TrimSpace(status) != "" {
		dirty = "+dirty"
	}
	return fmt.Sprintf("%s (%s%s)", version, strings.TrimSpace(sha), dirty)
}

func repositoryForExecutable(executable string) (string, bool) {
	if executable == "" || release.InstalledExecutable(executable) {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err == nil {
		executable = resolved
	}
	absolute, err := filepath.Abs(executable)
	if err == nil {
		executable = absolute
	}
	dir := filepath.Dir(executable)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func gitOutput(repo string, args ...string) (string, error) {
	command := append([]string{"-C", repo}, args...)
	cmd := execCommand("git", command...)
	output, err := cmd.Output()
	return string(output), err
}

var execCommand = func(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
