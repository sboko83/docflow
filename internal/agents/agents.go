// Package agents loads agent profiles and resolves runner routes.
package agents

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/lint"
)

const Unverified = "Unverified: "

var ResultSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"status":     map[string]any{"enum": []any{"done", "blocked"}},
		"handoff":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"decisions":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"unverified": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"reason":     map[string]any{"type": "string"},
		"blocker":    map[string]any{"enum": []any{"", "plan", "human"}},
	},
	"required":             []any{"status", "handoff", "decisions", "unverified", "reason", "blocker"},
	"additionalProperties": false,
}

// DefaultAgents is the built-in agents.toml text. Keep its bytes aligned with
// the Python CLI because `docflow run --print-config` prints it for copying.
const DefaultAgents = `[tiers]
fast = {agent = "claude", model = "haiku"}
default = {agent = "claude", model = "sonnet"}
strong = {agent = "claude", model = "opus"}
max = {agent = "claude", model = "opus", effort = "xhigh"}

[agents.claude]
cmd = ["claude", "-p", "--output-format", "stream-json", "--verbose",
       "--json-schema", "{schema}"]
model_args = ["--model", "{model}"]
effort_args = ["--effort", "{effort}"]
resume_args = ["--resume", "{session}"]
model = "sonnet"
result = "json:structured_output"
text = "json:result"
session = "json:session_id"
error = "json:is_error"
cost = "json:total_cost_usd"
stream = "claude"
output_tokens = "json:usage.output_tokens"
turns = "json:num_turns"
tokens = ["json:usage.input_tokens", "json:usage.cache_read_input_tokens",
          "json:usage.cache_creation_input_tokens"]
limit = "usage limit|limit reached|hit your limit|rate limit"
limit_wait = 1800
auth = "failed to authenticate|not logged in|invalid api key|oauth"
timeout = 7200
idle_timeout = 1200
instructions = ""
quota = ["claude", "-p", "/usage"]

[agents.claude.model_extra]
haiku = ["--allowedTools", "Write", "Edit", "Bash"]
claude-haiku-5-5 = ["--allowedTools", "Write", "Edit", "Bash"]

[agents.claude.permissions]
auto = ["--permission-mode", "auto", "--permission-prompts", "none"]
full = ["--permission-mode", "bypassPermissions"]

[agents.codex]
cmd = ["codex", "exec", "--json", "--output-schema", "{schema_file}"]
model_args = ["-m", "{model}"]
effort_args = ["-c", "model_reasoning_effort={effort}"]
resume_args = ["resume", "{session}"]
result = "json:text"
text = "json:text"
session = "json:thread_id"
error = "json:is_error"
tokens = ["json:usage.input_tokens"]
output_tokens = "json:usage.output_tokens"
stream = "codex"
limit = "usage limit|rate limit|hit your limit"
limit_wait = 1800
auth = "401 unauthorized|not logged in|codex login|token_expired|refresh token"
timeout = 7200
idle_timeout = 1200
quota = ["codex", "app-server"]
instructions = """Commands run in a workspace-write sandbox. If it blocks a command the unit needs (a write outside the repository such as ~/.gradle or DerivedData), rerun the command requesting escalated permissions with a one-line justification; the request is reviewed automatically."""

[agents.codex.permissions]
auto = ["--approve-for-me", "-c",
        "sandbox_workspace_write.network_access=true"]
full = ["--dangerously-bypass-approvals-and-sandbox"]

[agents.pi]
cmd = ["pi", "-p", "--mode", "json"]
model_args = ["--model", "{model}"]
effort_args = ["--thinking", "{effort}"]
resume_args = ["--session", "{session}"]
text = "json:text"
session = "json:session"
error = "json:is_error"
cost = "json:cost.total"
stream = "pi"
output_tokens = "json:usage.output"
tokens = ["json:usage.input", "json:usage.cacheRead", "json:usage.cacheWrite"]
limit = "usage limit|rate limit|hit your limit|limit exhausted"
limit_wait = 1800
auth = "no api key found"
timeout = 7200
idle_timeout = 1200

[agents.pi.permissions]
auto = []
full = ["--approve"]
`

var AgentKeys = map[string]string{
	"cmd": "list", "model_args": "list", "resume_args": "list", "model": "str",
	"result": "str", "text": "str", "session": "str", "error": "str", "cost": "str",
	"tokens": "list", "stream": "str", "output_tokens": "str", "turns": "str",
	"limit": "str", "limit_wait": "int", "auth": "str", "timeout": "int",
	"idle_timeout": "int",
	"instructions": "str", "quota": "list", "model_extra": "dict",
	"effort_args": "list", "permissions": "dict",
}

var Tiers = []string{"fast", "default", "strong", "max"}
var tierKeys = []string{"agent", "model", "effort"}
var Streams = []string{"", "claude", "codex", "pi"}
var PlaceholderRE = regexp.MustCompile(`\{(prompt|model|effort|schema|schema_file|session)\}`)

const schemaJSON = `{"type":"object","properties":{"status":{"enum":["done","blocked"]},"handoff":{"type":"array","items":{"type":"string"}},"decisions":{"type":"array","items":{"type":"string"}},"unverified":{"type":"array","items":{"type":"string"}},"reason":{"type":"string"},"blocker":{"enum":["","plan","human"]}},"required":["status","handoff","decisions","unverified","reason","blocker"],"additionalProperties":false}`

var RunnerCycle = strings.Join([]string{
	"## Cycle (runner mode)",
	"A runner drives this unit: it reuses an unchanged full agent verify or",
	"runs `docflow verify`, then runs `done` and `commit` itself after your reply.",
	"1. Do exactly what `Do:` says, no less and no more.",
	"   Handoff `Unblocked:` after the latest `Blocked (human)` — a person did the",
	"   step; a legacy `Blocked:` item counts as human. Before `Do:`",
	"   check that condition with a command first (a file, a login, a device",
	"   state). Met → go on with `Do:`; not met → reply blocked with",
	"   `blocker: \"human\"` naming what is still missing, without doing the rest of",
	"   `Do:`.",
	"2. Iterate with the unit's `Verify:` command or `docflow verify {id} --no-gate`.",
	"   Do not pipe long checks through `tail` or `grep`; inspect their full output.",
	"   Before replying, run one full `docflow verify {id}`; it runs the project's",
	"   `preflight:` and the plan's `gate:` when configured. On failure, inspect",
	"   the complete output at the path printed as `log:`. The runner reuses a",
	"   successful full verify while the tree is unchanged; `--skip` is not reused.",
	"   Run `Verify` and tests in the foreground. Do not end your reply while",
	"   a background task or subagent is still running: wait for it or stop it",
	"   first, or the reply loses its result JSON.",
	"3. Do not run `docflow finish`, `done`, `commit`, `block` or",
	"   `git commit` in this repository yourself, and do not edit or commit the",
	"   plan file. Commits made by a tool that `Do:` tells you to run are fine.",
	"4. End your reply with the result JSON as the last ```json block, every",
	"   key present (an empty list or string when unused):",
	`   {"status": "done", "handoff": ["..."], "decisions": ["..."],`,
	`    "unverified": ["..."], "reason": "", "blocker": ""}`,
	"   - handoff: what the next unit must know and the code does not show;",
	"     at least one item.",
	"   - decisions: choices made on the way that outlive the unit, each as",
	"     \"decision — why — rejected alternative\"; may be empty.",
	"   - unverified: a risk of what this unit changed that `Verify` does not",
	"     cover, each naming the missing check (layout on a device, a flow",
	"     `Verify` does not run, a case without a test). Not: items already",
	"     listed under `## Handoff from Depends`, ideas to improve tests,",
	"     checks of code this unit did not change. A `Do:` step or Goal",
	"     requirement left undone is not unverified: finish it, or reply",
	"     blocked with blocker \"plan\". An empty list claims every risk of this",
	"     change is covered by `Verify`.",
	"   If the work cannot go on, stop and reply blocked; `blocker` says who",
	"   unblocks it:",
	"   - \"plan\" — `Do:` contradicts reality: the plan needs a new step, a new",
	"     unit or a fix; the runner replans the plan and continues.",
	"   - \"human\" — only a person can unblock it: a login, access, a secret,",
	"     a device or a decision; `reason` says what exactly to do.",
	`   {"status": "blocked", "handoff": [], "decisions": [], "unverified": [],`,
	`    "reason": "<what the plan expected, what is real>",`,
	`    "blocker": "plan"}`,
}, "\n") + "\n"

var planIDRE = regexp.MustCompile(`^P\p{Nd}{3,}$`)
var executorPlanStatuses = map[string]bool{"draft": true, "active": true, "frozen": true}

// ConfigPath resolves --config, DOCFLOW_AGENTS, and the XDG default.
func ConfigPath(option string) string {
	if option != "" {
		return expandUser(option)
	}
	if env := os.Getenv("DOCFLOW_AGENTS"); env != "" {
		return expandUser(env)
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			base = "~/.config"
		} else {
			base = filepath.Join(home, ".config")
		}
	}
	return filepath.Join(base, "docflow", "agents.toml")
}

func expandUser(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	separators := string(filepath.Separator)
	if filepath.Separator == '\\' {
		separators += "/"
	}
	separator := strings.IndexAny(path[1:], separators)
	if separator >= 0 {
		separator++
	}
	if separator < 0 {
		separator = len(path)
	}
	username := path[1:separator]
	suffix := ""
	if separator < len(path) {
		suffix = path[separator+1:]
	}
	var home string
	if username == "" {
		home, _ = os.UserHomeDir()
	} else if account, err := user.Lookup(username); err == nil {
		home = account.HomeDir
	}
	if home == "" {
		return path
	}
	return filepath.Join(home, suffix)
}

// LoadAgents returns the built-in profiles with an optional user file merged
// over them. An empty path selects only the built-ins.
func LoadAgents(path string, required bool) (map[string]any, error) {
	config, err := ParseTOML(DefaultAgents)
	if err != nil {
		return nil, err
	}
	where := "<built-in agents>"
	if required && (path == "" || !isRegularFile(path)) {
		return nil, agentsError(fmt.Sprintf("%s: agents config not found", path))
	}
	if path != "" && isRegularFile(path) {
		where = path
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		userConfig, err := ParseTOML(string(content))
		if err != nil {
			return nil, agentsError(fmt.Sprintf("%s: %v", where, err))
		}
		if _, exists := userConfig["default_agent"]; exists {
			return nil, agentsError(fmt.Sprintf("%s: 'default_agent' moved to [tiers.default]", where))
		}
		for _, key := range sortedKeys(userConfig) {
			if key != "tiers" && key != "agents" {
				return nil, agentsError(fmt.Sprintf("%s: unknown key %s", where, pyRepr(key)))
			}
		}
		if err := mergeTiers(where, config, userConfig["tiers"]); err != nil {
			return nil, err
		}
		profiles := mapAt(config, "agents")
		userProfiles, ok := userConfig["agents"]
		if ok {
			newProfiles, valid := userProfiles.(map[string]any)
			if !valid {
				return nil, agentsError(fmt.Sprintf("%s: agents must be a table", where))
			}
			for _, name := range sortedKeys(newProfiles) {
				userProfile, valid := newProfiles[name].(map[string]any)
				if !valid {
					return nil, agentsError(fmt.Sprintf("%s: agent %s must be a table", where, pyRepr(name)))
				}
				if _, exists := userProfile["models"]; exists {
					return nil, agentsError(fmt.Sprintf("%s: agent %s: 'models' moved to [tiers]", where, pyRepr(name)))
				}
				merged := copyTable(mapAt(profiles, name))
				mergedTables := make(map[string]any)
				for _, key := range []string{"model_extra", "permissions"} {
					value, exists := userProfile[key]
					userTable, tableOK := value.(map[string]any)
					if !exists || tableOK {
						combined := copyTable(mapAt(merged, key))
						if tableOK {
							for subkey, subvalue := range userTable {
								combined[subkey] = subvalue
							}
						}
						if len(combined) > 0 {
							mergedTables[key] = combined
						}
					}
				}
				for key, value := range userProfile {
					merged[key] = value
				}
				for key, value := range mergedTables {
					merged[key] = value
				}
				profiles[name] = merged
			}
		}
	}
	profiles := mapAt(config, "agents")
	for _, name := range sortedKeys(profiles) {
		profile, ok := profiles[name].(map[string]any)
		if !ok {
			return nil, agentsError(fmt.Sprintf("%s: agent %s must be a table", where, pyRepr(name)))
		}
		if err := checkAgent(where, name, profile); err != nil {
			return nil, err
		}
	}
	if err := checkTiers(where, config); err != nil {
		return nil, err
	}
	return config, nil
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func checkAgent(where, name string, agent map[string]any) error {
	for _, key := range sortedKeys(agent) {
		value := agent[key]
		kind, known := AgentKeys[key]
		if !known {
			return agentsError(fmt.Sprintf("%s: agent %s: unknown key %s", where, pyRepr(name), pyRepr(key)))
		}
		valid := false
		switch kind {
		case "str":
			_, valid = value.(string)
		case "int":
			switch value.(type) {
			case int, int64:
				valid = true
			}
		case "list":
			values, ok := value.([]any)
			valid = ok
			if valid {
				for _, item := range values {
					if _, ok := item.(string); !ok {
						valid = false
						break
					}
				}
			}
		case "dict":
			_, valid = value.(map[string]any)
		}
		if valid && (key == "model_extra" || key == "permissions") {
			table := value.(map[string]any)
			for _, subkey := range sortedKeys(table) {
				items, ok := table[subkey].([]any)
				if !ok || !allStrings(items) {
					return agentsError(fmt.Sprintf("%s: agent %s: %s must be a table of string lists", where, pyRepr(name), pyRepr(key)))
				}
			}
		}
		if valid && key == "idle_timeout" {
			seconds := 0
			switch value := value.(type) {
			case int:
				seconds = value
			case int64:
				seconds = int(value)
			}
			if seconds != 0 && (seconds < 5 || seconds > 86400) {
				return agentsError(fmt.Sprintf("%s: agent %s: idle_timeout must be 0 or an integer from 5 to 86400 seconds", where, pyRepr(name)))
			}
		}
		if !valid {
			suffix := ""
			if kind == "list" || kind == "dict" {
				suffix = " of strings"
			}
			return agentsError(fmt.Sprintf("%s: agent %s: %s must be %s%s", where, pyRepr(name), pyRepr(key), kind, suffix))
		}
	}
	stream, _ := agent["stream"].(string)
	if !contains(Streams, stream) {
		return agentsError(fmt.Sprintf("%s: agent %s: 'stream' must be one of: \"\", \"claude\"", where, pyRepr(name)))
	}
	cmd, _ := stringList(agent["cmd"])
	if len(cmd) == 0 {
		return agentsError(fmt.Sprintf("%s: agent %s: cmd must be a non-empty list of strings", where, pyRepr(name)))
	}
	return nil
}

func mergeTiers(where string, config map[string]any, value any) error {
	user, exists := value.(map[string]any)
	if !exists {
		if value == nil {
			user = map[string]any{}
		} else {
			return agentsError(fmt.Sprintf("%s: tiers must be a table", where))
		}
	}
	tiers := mapAt(config, "tiers")
	for _, tier := range sortedKeys(user) {
		if !contains(Tiers, tier) {
			return agentsError(fmt.Sprintf("%s: unknown tier %s; known: %s", where, pyRepr(tier), strings.Join(Tiers, ", ")))
		}
		row, ok := user[tier].(map[string]any)
		if !ok {
			return agentsError(fmt.Sprintf("%s: tier %s must be a table", where, pyRepr(tier)))
		}
		combined := copyTable(mapAt(tiers, tier))
		for key, item := range row {
			combined[key] = item
		}
		tiers[tier] = combined
	}
	return nil
}

func checkTiers(where string, config map[string]any) error {
	tiers := mapAt(config, "tiers")
	agents := mapAt(config, "agents")
	for _, tier := range sortedKeys(tiers) {
		row, _ := tiers[tier].(map[string]any)
		for _, key := range sortedKeys(row) {
			value := row[key]
			if !contains(tierKeys, key) {
				return agentsError(fmt.Sprintf("%s: tier %s: unknown key %s", where, pyRepr(tier), pyRepr(key)))
			}
			if _, ok := value.(string); !ok {
				return agentsError(fmt.Sprintf("%s: tier %s: %s must be str", where, pyRepr(tier), pyRepr(key)))
			}
		}
		name, _ := row["agent"].(string)
		if _, ok := agents[name]; !ok {
			return agentsError(fmt.Sprintf("%s: tier %s: agent %s has no profile", where, pyRepr(tier), pyRepr(name)))
		}
	}
	return nil
}

// ResolveRoute chooses the profile, model, and effort for one unit.
func ResolveRoute(config map[string]any, unit *format.Unit, args RunArgs) (Route, error) {
	tiers := mapAt(config, "tiers")
	given := args.Model
	if given == "" && unit != nil {
		if field := unit.Field("Model"); field != nil {
			given = field.Value
		}
	}
	tier, modelID := "default", ""
	if _, ok := tiers[given]; given != "" && ok {
		tier = given
	} else if given != "" {
		modelID = given
	}
	row, ok := tiers[tier].(map[string]any)
	if !ok {
		return Route{}, agentsError(fmt.Sprintf("run: missing tier %s", pyRepr(tier)))
	}
	name, _ := row["agent"].(string)
	model, _ := row["model"].(string)
	effort, _ := row["effort"].(string)
	if args.Agent != "" {
		name = args.Agent
		profile, exists := mapAt(config, "agents")[name].(map[string]any)
		if !exists {
			return Route{}, agentsError(fmt.Sprintf("run: unknown agent %s; known: %s", pyRepr(name), strings.Join(sortedKeys(mapAt(config, "agents")), ", ")))
		}
		model, _ = profile["model"].(string)
		effort = ""
	}
	if modelID != "" {
		model = modelID
	}
	if args.Effort != "" {
		effort = args.Effort
	}
	return Route{Agent: name, Model: model, Effort: effort}, nil
}

// RunArgs contains flags that affect run selection and routing.
type RunArgs struct {
	Config    string
	Agent     string
	Model     string
	Effort    string
	Plan      string
	Executors bool
}

// Route is the full route for a single unit.
type Route struct {
	Agent  string
	Model  string
	Effort string
}

// RunSelection selects the current unit or the plan named by --plan.
func RunSelection(root string, args RunArgs) (cycle.Selection, error) {
	if err := format.CheckVersion(root); err != nil {
		return cycle.Selection{}, err
	}
	if args.Plan == "" {
		text, err := format.ReadText(filepath.Join(root, filepath.FromSlash(format.DocflowFilePath)))
		if err != nil {
			return cycle.Selection{}, err
		}
		docflow, err := format.ParseDocflow(text, format.DocflowFilePath)
		if err != nil {
			return cycle.Selection{}, err
		}
		return cycle.SelectUnit(root, docflow)
	}
	if !planIDRE.MatchString(args.Plan) {
		return cycle.Selection{}, agentsError(fmt.Sprintf("run: --plan %s is not a plan id", pyRepr(args.Plan)))
	}
	plan, err := findPlan(root, args.Plan)
	if err != nil {
		return cycle.Selection{}, err
	}
	if !executorPlanStatuses[plan.Meta["status"]] {
		status := "None"
		if value, ok := plan.Meta["status"]; ok {
			status = pyRepr(value)
		}
		return cycle.Selection{}, agentsError(fmt.Sprintf("run: %s has status %s; expected one of: draft, active, frozen", args.Plan, status))
	}
	return planSelection(plan)
}

func findPlan(root, planID string) (*format.Plan, error) {
	plans := filepath.Join(root, "docs", "plans")
	found, err := filepath.Glob(filepath.Join(plans, planID+"-*.md"))
	if err != nil {
		return nil, err
	}
	plain, err := filepath.Glob(filepath.Join(plans, planID+".md"))
	if err != nil {
		return nil, err
	}
	found = append(found, plain...)
	sort.Strings(found)
	if len(found) == 0 {
		return nil, agentsError(fmt.Sprintf("%s: no plan in docs/plans/", planID))
	}
	path, err := filepath.Rel(root, found[0])
	if err != nil {
		return nil, err
	}
	text, err := format.ReadText(found[0])
	if err != nil {
		return nil, err
	}
	return format.ParsePlan(text, filepath.ToSlash(path))
}

func planSelection(plan *format.Plan) (cycle.Selection, error) {
	for _, unit := range plan.Units {
		if unit.Status == "~" {
			return cycle.Selection{Kind: "unit", Plan: plan, Unit: unit}, nil
		}
	}
	for _, unit := range plan.Units {
		if unit.Status == "!" {
			return cycle.Selection{Kind: "blocked", Plan: plan, Unit: unit}, nil
		}
	}
	statuses := make(map[string]string, len(plan.Units))
	for _, unit := range plan.Units {
		statuses[unit.ID] = unit.Status
	}
	for _, unit := range plan.Units {
		if unit.Status != " " {
			continue
		}
		depends, err := format.UnitDepends(plan, unit)
		if err != nil {
			return cycle.Selection{}, err
		}
		ready := true
		for _, id := range depends {
			if statuses[id] != "x" && statuses[id] != "-" {
				ready = false
				break
			}
		}
		if ready {
			return cycle.Selection{Kind: "unit", Plan: plan, Unit: unit}, nil
		}
	}
	return cycle.Selection{Kind: "nothing", Plan: plan}, nil
}

// RunConfig loads the config selected for a plan and records executor mode.
func RunConfig(plan *format.Plan, args *RunArgs) (map[string]any, error) {
	args.Executors = plan != nil && lint.ExecutorMode(plan)
	if args.Executors {
		flags := make([]string, 0, 4)
		for _, flag := range []struct{ name, value string }{{"config", args.Config}, {"agent", args.Agent}, {"model", args.Model}, {"effort", args.Effort}} {
			if flag.value != "" {
				flags = append(flags, "--"+flag.name)
			}
		}
		if len(flags) > 0 {
			return nil, agentsError(fmt.Sprintf("run: %s assigns executors; %s would replace them: change Executor in the plan", plan.Meta["id"], strings.Join(flags, ", ")))
		}
		if err := CheckExecutors(plan); err != nil {
			return nil, err
		}
		return LoadAgents("", false)
	}
	explicit := args.Config != "" || os.Getenv("DOCFLOW_AGENTS") != ""
	config, err := LoadAgents(ConfigPath(args.Config), explicit)
	if err != nil {
		return nil, err
	}
	if args.Agent != "" {
		if _, ok := mapAt(config, "agents")[args.Agent]; !ok {
			return nil, agentsError(fmt.Sprintf("run: unknown agent %s; known: %s", pyRepr(args.Agent), strings.Join(sortedKeys(mapAt(config, "agents")), ", ")))
		}
	}
	return config, nil
}

// CheckExecutors reports every invalid or missing assignment on the plan.
func CheckExecutors(plan *format.Plan) error {
	issues := lint.ExecutorIssueStrings(plan)
	if len(issues) == 0 {
		return nil
	}
	return agentsError(strings.Join(issues, "\n"))
}

// PlanAgents returns sorted names of profiles a run can invoke. Every
// route of each open unit's chain is covered -- a role key chain or the
// executor triple followed by the plan fallback -- plus every route of
// the replan chain, so a missing skill surfaces before a fallback fires,
// not when it does.
func PlanAgents(config map[string]any, plan *format.Plan, args RunArgs) []string {
	names := make(map[string]bool)
	addRoutes := func(routes []Route) {
		for _, route := range routes {
			names[route.Agent] = true
		}
	}
	for _, unit := range plan.Units {
		if unit.Status == "x" || unit.Status == "-" {
			continue
		}
		primary, err := RunRoute(config, plan, unit, args)
		if err != nil {
			continue
		}
		routes := []Route{primary}
		if args.Executors {
			// The unit runs its full chain: the chain of its role key, or
			// the executor triple followed by fallback.
			if chain, err := UnitRoleRoutes(plan, unit); err == nil {
				routes = chain
			}
		}
		addRoutes(routes)
	}
	if value := plan.Meta["replan"]; value != "" {
		if routes, err := ChainRoutes(value); err == nil {
			addRoutes(routes)
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// RunRoute resolves a unit route and refuses plans whose executor mode changed.
func RunRoute(config map[string]any, plan *format.Plan, unit *format.Unit, args RunArgs) (Route, error) {
	if lint.ExecutorMode(plan) != args.Executors {
		return Route{}, agentsError(fmt.Sprintf("run: %s changed its executor assignment during the run; run again", plan.Meta["id"]))
	}
	if args.Executors {
		if err := CheckExecutors(plan); err != nil {
			return Route{}, err
		}
		agent, model, effort, err := lint.UnitExecutor(plan, unit)
		if err != nil {
			return Route{}, err
		}
		route := Route{Agent: agent, Model: model}
		if effort != nil {
			route.Effort = *effort
		}
		return route, nil
	}
	return ResolveRoute(config, unit, args)
}

// RouteSource identifies whether a route comes from a unit, its plan, or a legacy tier.
func RouteSource(plan *format.Plan, unit *format.Unit) string {
	if !lint.ExecutorMode(plan) {
		return "legacy"
	}
	if field := unit.Field("Role"); field != nil {
		return "role " + field.Value
	}
	if unit.Field("Executor") != nil {
		return "unit"
	}
	return "plan"
}

// RouteText formats a route as the Executor field does.
func RouteText(route Route) string {
	model, effort := route.Model, route.Effort
	if model == "" {
		model = "-"
	}
	if effort == "" {
		effort = "-"
	}
	return fmt.Sprintf("%s | %s | %s", route.Agent, model, effort)
}

// routeOf parses a single "agent | model | effort" triple into a Route.
func routeOf(value string) (Route, error) {
	name, model, effort, err := lint.ParseExecutor(value)
	if err != nil {
		return Route{}, err
	}
	route := Route{Agent: name, Model: model}
	if effort != nil {
		route.Effort = *effort
	}
	return route, nil
}

// ChainRoutes parses a "agent | model | effort; ..." chain into routes;
// empty segments are skipped.
func ChainRoutes(value string) ([]Route, error) {
	var routes []Route
	for _, raw := range strings.Split(value, ";") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		route, err := routeOf(strings.TrimSpace(raw))
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, nil
}

// UnitRoleRoutes resolves the ordered route chain of a unit: an explicit
// Executor triple followed by the plan fallback, the chain of the unit's
// role key, or the plan executor followed by fallback.
func UnitRoleRoutes(plan *format.Plan, unit *format.Unit) ([]Route, error) {
	if field := unit.Field("Role"); field != nil {
		routes, err := ChainRoutes(plan.Meta[field.Value])
		if err != nil {
			return nil, err
		}
		if len(routes) == 0 {
			return nil, fmt.Errorf("role %s needs at least one route", field.Value)
		}
		return routes, nil
	}
	value := ""
	if field := unit.Field("Executor"); field != nil {
		value = field.Value
	} else if head, ok := plan.Meta["executor"]; ok {
		value = head
	} else {
		return nil, fmt.Errorf("no executor assignment")
	}
	primary, err := routeOf(value)
	if err != nil {
		return nil, err
	}
	fallback, err := ChainRoutes(plan.Meta["fallback"])
	if err != nil {
		return nil, err
	}
	return append([]Route{primary}, fallback...), nil
}

// BuildCommand constructs argv and returns nil stdin when {prompt} is used.
func BuildCommand(agent map[string]any, prompt, model, schemaFile, session, effort, permissions string) ([]string, *string, error) {
	if permissions == "" {
		permissions = "auto"
	}
	args, _ := stringList(agent["cmd"])
	args = append([]string(nil), args...)
	permissionsTable, _ := agent["permissions"].(map[string]any)
	modeArgs, exists := permissionsTable[permissions]
	if !exists {
		if permissions == "full" {
			return nil, nil, agentsError("run: permissions full: the agent profile has no 'permissions' table with a 'full' key; add one or keep the plan at auto")
		}
	} else {
		values, _ := stringList(modeArgs)
		args = append(args, values...)
	}
	if model != "" {
		values, _ := stringList(agent["model_args"])
		args = append(args, values...)
		if extra, ok := agent["model_extra"].(map[string]any); ok {
			values, _ := stringList(extra[model])
			args = append(args, values...)
		}
	}
	if effort != "" {
		values, _ := stringList(agent["effort_args"])
		args = append(args, values...)
	}
	if session != "" {
		values, _ := stringList(agent["resume_args"])
		args = append(args, values...)
	}
	usesPrompt := false
	for _, arg := range args {
		if strings.Contains(arg, "{prompt}") {
			usesPrompt = true
			break
		}
	}
	values := map[string]string{
		"prompt": prompt, "model": model, "effort": effort,
		"schema": schemaJSON, "schema_file": schemaFile, "session": session,
	}
	argv := make([]string, len(args))
	for i, arg := range args {
		argv[i] = PlaceholderRE.ReplaceAllStringFunc(arg, func(match string) string {
			parts := PlaceholderRE.FindStringSubmatch(match)
			return values[parts[1]]
		})
	}
	if usesPrompt {
		return argv, nil, nil
	}
	return argv, &prompt, nil
}

func mapAt(table map[string]any, key string) map[string]any {
	if value, ok := table[key].(map[string]any); ok {
		return value
	}
	return map[string]any{}
}

func copyTable(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func stringList(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, len(items))
	for i, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		result[i] = text
	}
	return result, true
}

func allStrings(values []any) bool {
	for _, value := range values {
		if _, ok := value.(string); !ok {
			return false
		}
	}
	return true
}

func contains(values []string, item string) bool {
	for _, value := range values {
		if value == item {
			return true
		}
	}
	return false
}

func sortedKeys(table map[string]any) []string {
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func pyRepr(value string) string {
	if !strings.Contains(value, "'") {
		return "'" + value + "'"
	}
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "'", `\'`)
	return "'" + value + "'"
}

func agentsError(message string) error {
	return clierrors.DocflowError{Message: message, Code: clierrors.EXIT_FORMAT}
}
