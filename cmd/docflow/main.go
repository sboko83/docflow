package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/lint"
	"bsv-docflow/internal/release"
	"bsv-docflow/internal/roster"
	"bsv-docflow/internal/runner"
	docskills "bsv-docflow/internal/skills"
	"bsv-docflow/internal/stats"
)

var version = "0.11.0"

var currentExecutable = os.Executable

const runnerEnv = "DOCFLOW_RUNNER_UNIT"

var commands = []commandSpec{
	{name: "init", help: "create docs/docflow.md, plans/, archive/ and a line in AGENTS.md; --skills also copies the skills into the project", args: []argument{choiceOption("--agent", "", "skill directory set (default: claude)", "claude", "claude", "codex", "pi"), flag("--skills", "copy skills into the selected project directory")}},
	{name: "status", help: "show the queue, the current plan and its progress"},
	{name: "next", help: "print the id of the next available unit"},
	{name: "prompt", help: "print the prompt for the next unit", args: []argument{positional("id", "", false)}},
	{name: "verify", help: "run the Verify command of the current unit", args: []argument{positional("id", "", true), option("--skip", "REASON", "record the fingerprint without running Verify"), flag("--no-gate", "skip the plan's gate; nothing is recorded")}},
	{name: "done", help: "mark the current unit done and write its Handoff", args: []argument{positional("id", "", true), requiredAppend("--handoff", "HANDOFF", "Handoff item; repeat per item"), appendOption("--decision", "DECISION", "item for ## Decisions; repeat per item")}},
	{name: "commit", help: "commit the current unit with its Commit subject", args: []argument{positional("id", "", true)}},
	{name: "finish", help: "verify, done and commit the current unit in one call", args: []argument{positional("id", "", true), requiredAppend("--handoff", "HANDOFF", "Handoff item; repeat per item"), appendOption("--decision", "DECISION", "item for ## Decisions; repeat per item"), option("--skip", "REASON", "record the fingerprint without running Verify")}},
	{name: "stats", help: "token use per unit: context proxy against the real figures", args: []argument{positional("PLAN", "", false)}},
	{name: "block", help: "mark the current unit blocked", args: []argument{positional("id", "", true), requiredOption("--reason", "REASON", ""), choiceOption("--needs", "", "who unblocks the unit (default: human)", "human", "human", "plan")}},
	{name: "lint", help: "check structure, ids, Depends and the Read budget"},
	{name: "new", help: "create a plan from the template and add it to the Queue", args: []argument{positional("slug", "", true), option("--title", "TITLE", ""), choiceOption("--type", "", "", "feature", "feature", "bugfix", "perf", "module", "refactor")}},
	{name: "activate", help: "make a draft plan active after a clean lint of it", args: []argument{positional("id", "", true)}},
	{name: "close", help: "archive a finished plan and delete it", args: []argument{positional("id", "", true)}},
	{name: "freeze", help: "move a plan from the Queue to Frozen", args: []argument{positional("id", "", true), option("--reason", "TEXT", "why the plan is frozen")}},
	{name: "thaw", help: "move a frozen plan back to the end of the Queue", args: []argument{positional("id", "", true)}},
	{name: "note", help: "write an archive entry without a plan", args: []argument{positional("slug", "", true), option("--title", "TITLE", "")}},
	{name: "migrate", help: "convert docs from previous format versions"},
	{name: "install-skills", help: "symlink skills/* into the selected agent directory, or copy them into the project with --project", args: []argument{choiceOption("--agent", "", "skill directory set (default: claude)", "claude", "claude", "codex", "pi"), option("--target", "DIR", "personal skills directory override"), flag("--project", "copy skills/* into the project")}},
	{name: "doctor", help: "check that the installed script and skills match this repo", args: []argument{choiceOption("--agent", "", "skill directory set (default: claude)", "claude", "claude", "codex", "pi"), option("--target", "DIR", "personal skills directory override")}},
	{name: "roster", help: "show the route chains per plan role from the personal roster, or write them into a plan header", args: []argument{positional("subcommand", "", true), positional("id", "", false), option("--profile", "NAME", "spend and provider-flag overrides from [profile.NAME]"), option("--roster", "PATH", "roster.toml path (default ~/.config/docflow/roster.toml)")}},
	{name: "run", help: "drive the plan unit by unit through a coding agent", args: runArguments()},
	{name: "install", help: "install an unpacked release into ~/.docflow and link personal skills"},
	{name: "update", help: "download and activate the latest release", args: []argument{flag("--check", "check for a new release without installing it")}},
	{name: "uninstall", help: "remove the standalone installation and its personal skill links"},
}

type argumentKind int

const (
	positionalArg argumentKind = iota
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
}

type parsedArgs struct {
	command string
	values  map[string]any
}

func positional(name, metavar string, required bool) argument {
	return argument{name: name, metavar: metavar, kind: positionalArg, required: required, optional: !required}
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
    stats           token use per unit: context proxy against the real figures
    block           mark the current unit blocked
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
    roster          show the route chains per plan role from the personal
                    roster, or write them into a plan header
    run             drive the plan unit by unit through a coding agent
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
	out["install"] = releaseCommand(handleInstall)
	out["update"] = releaseCommand(handleUpdate)
	out["uninstall"] = releaseCommand(handleUninstall)
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
	args := runner.Args{RunArgs: agents.RunArgs{Config: str("--config"), Agent: str("--agent"), Model: str("--model"), Effort: str("--effort"), Plan: str("--plan")}, Attempts: attempts, Replans: replans, MaxUnits: maxUnits, NoWait: flag("--no-wait"), NoNotify: flag("--no-notify"), DryRun: flag("--dry-run"), All: flag("--all"), PrintConfig: flag("--print-config"), Yes: flag("--yes"), Verbose: flag("-v --verbose"), Stop: flag("--stop")}
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

func handleRoster(parsed *parsedArgs) error {
	subcommand, _ := parsed.values["subcommand"].(string)
	profileName, _ := parsed.values["--profile"].(string)
	path, _ := parsed.values["--roster"].(string)
	switch subcommand {
	case "show":
		r, err := roster.Load(path, profileName)
		if err != nil {
			return err
		}
		return commandResult{code: clierrors.EXIT_OK, stdout: r.Show()}
	case "apply":
		planID, _ := parsed.values["id"].(string)
		if planID == "" {
			return clierrors.DocflowError{Message: "docflow: roster apply: no plan id", Code: clierrors.EXIT_FORMAT}
		}
		root, err := projectRoot()
		if err != nil {
			return err
		}
		result, err := roster.Apply(root, planID, profileName, path)
		if err != nil {
			return err
		}
		return commandResult{code: result.Code, stdout: result.Stdout, stderr: result.Stderr}
	default:
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: unknown roster subcommand '%s' (known: show, apply)", subcommand), Code: clierrors.EXIT_FORMAT}
	}
}

func handleStats(parsed *parsedArgs) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	planID, _ := parsed.values["PLAN"].(string)
	result, err := stats.Command(root, planID)
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
	return name == "finish" || name == "done" || name == "commit" || name == "block"
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
		if arg.kind == positionalArg {
			positionals = append(positionals, arg)
			continue
		}
		for _, name := range strings.Fields(arg.name) {
			options[name] = arg
		}
	}
	posIndex := 0
	unknown := make([]string, 0)
	for index := 0; index < len(argv); index++ {
		token := argv[index]
		if token == "--" {
			for _, value := range argv[index+1:] {
				if posIndex >= len(positionals) {
					unknown = append(unknown, value)
					continue
				}
				arg := positionals[posIndex]
				values[arg.name], seen[arg.name] = value, true
				posIndex++
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
		if posIndex >= len(positionals) {
			unknown = append(unknown, token)
			continue
		}
		arg := positionals[posIndex]
		values[arg.name], seen[arg.name] = token, true
		posIndex++
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
	names := make([]string, len(commands))
	for i, spec := range commands {
		names[i] = spec.name
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
