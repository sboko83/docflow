package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/gitx"
)

func planReady(root string, plan *format.Plan) (bool, error) {
	for _, unit := range plan.Units {
		pending, err := cycle.CommitPending(root, plan, unit)
		if err != nil {
			return false, err
		}
		if !doneStatus(unit.Status) || pending {
			return false, nil
		}
	}
	return true, nil
}

// Ordinary selection skips completed plans. With --close, take a ready
// queued plan before the next untouched unit, preserving recovery priority.
func runSelection(root string, args Args) (cycle.Selection, error) {
	sel, err := agents.RunSelection(root, args.RunArgs)
	if err != nil || !args.Close {
		return sel, err
	}
	if args.Plan != "" {
		if ready, err := planReady(root, sel.Plan); err != nil {
			return sel, err
		} else if ready {
			sel.Kind = "close"
		}
		return sel, nil
	}
	if sel.Unit != nil && (sel.Unit.Status == "~" || doneStatus(sel.Unit.Status)) {
		return sel, nil
	}
	text, err := format.ReadText(filepath.Join(root, format.DocflowFilePath))
	if err != nil {
		return sel, err
	}
	doc, err := format.ParseDocflow(text, format.DocflowFilePath)
	if err != nil {
		return sel, err
	}
	for _, item := range doc.Queue {
		plan, err := cycle.FindPlan(root, item.Text)
		if err != nil {
			return sel, err
		}
		if sel.Plan != nil && plan.Meta["id"] == sel.Plan.Meta["id"] {
			break
		}
		if plan.Meta["status"] == "draft" {
			continue
		}
		ready, err := planReady(root, plan)
		if err != nil {
			return sel, err
		}
		if ready {
			return cycle.Selection{Kind: "close", Plan: plan}, nil
		}
	}
	return sel, nil
}

func closeExecutorChain(config map[string]any, plan *format.Plan, args agents.RunArgs, unavailable map[string]time.Time) (*executorChain, error) {
	if value := plan.Meta["cheap"]; value != "" {
		routes, err := agents.ChainRoutes(value)
		if err != nil {
			return nil, err
		}
		return newExecutorChain(routes, config, unavailable, true), nil
	}
	// A synthetic unit deliberately has no Role or Executor override: closing
	// uses the header executor, or the default route of a legacy plan.
	primary, err := agents.RunRoute(config, plan, &format.Unit{}, args)
	if err != nil {
		return nil, err
	}
	return routeExecutorChain(config, plan, primary, args, unavailable)
}

func closeRouteLine(args Args, config map[string]any, plan *format.Plan, out io.Writer) error {
	if !args.Close || plan == nil {
		return nil
	}
	chain, err := closeExecutorChain(config, plan, args.RunArgs, nil)
	if err != nil {
		return err
	}
	parts := make([]string, 0, len(chain.routes))
	for _, route := range chain.routes {
		parts = append(parts, agents.RouteText(route))
	}
	fmt.Fprintln(out, "close: "+strings.Join(parts, "; "))
	return nil
}

// closeCLICommand hands the closing agent the absolute path of the running CLI:
// a PATH lookup inside the agent's shell can land on a foreign docflow (the
// inherited PATH is not fully under the runner's control).
func closeCLICommand(cli string) string {
	if cli == "" {
		return "docflow"
	}
	if runtime.GOOS == "windows" {
		return "& '" + strings.ReplaceAll(cli, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(cli, "'", `'"'"'`) + "'"
}

func closePrompt(plan *format.Plan, cli string) string {
	cli = closeCLICommand(cli)
	return fmt.Sprintf(`# Close %s %s

Read %s in full and follow the bsv-docflow-close skill.
Run %s close %s yourself, then fill the archive entry's Outcome,
Decisions, Gotchas and Follow-ups. Update documentation links, current state,
CHANGELOG and Backlog as the skill requires. Run %s lint.
These commands use PowerShell syntax on Windows and POSIX shell syntax on Unix.
Edit only docs/, CHANGELOG.md, README.md and README.ru.md.
Do not commit or rewrite Git history: the runner checks and commits the result.
The runner owns this closing operation; ignore the skill's commit step.

End with the same result JSON contract as a runner unit, every key present:
`+"```json\n"+`{"status":"done","handoff":["Closing outcome"],"decisions":[],"unverified":[],"reason":"","blocker":""}
`+"```\n"+`If blocked, use status "blocked", blocker "plan" or "human", and a concrete reason.
`, plan.Meta["id"], plan.Meta["title"], plan.Path, cli, plan.Meta["id"], cli)
}

// The closing agent invokes this binary's close subcommand, even when the
// runner was started by an absolute path outside the inherited PATH.
func closeEnvironment() ([]string, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(executable)
	path := os.Getenv("PATH")
	found := false
	for _, entry := range filepath.SplitList(path) {
		if entry == dir {
			found = true
		}
	}
	if !found {
		path = dir + string(os.PathListSeparator) + path
	}
	env := []string{}
	for _, entry := range os.Environ() {
		// Windows spells the variable Path; match any case or the old value
		// survives next to the new one and wins the child's lookup.
		if len(entry) >= 5 && strings.EqualFold(entry[:5], "PATH=") {
			continue
		}
		if strings.HasPrefix(entry, agentio.RunnerEnv+"=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "PATH="+path), nil
}

// NUL-delimited paths and disabled rename detection check both sides of a
// rename and preserve filenames containing spaces, quotes or newlines.
func closeChangedPaths(root, head string) ([]string, error) {
	tracked, err := gitx.Git(root, "diff", "--name-only", "--no-renames", "-z", head, "--")
	if err != nil {
		return nil, err
	}
	untracked, err := gitx.Git(root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	paths := []string{}
	seen := map[string]bool{}
	for _, path := range strings.Split(string(tracked)+string(untracked), "\x00") {
		if path != "" && !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func closeChecks(root string, plan *format.Plan, head string) ([]string, []string, error) {
	problems := []string{}
	if _, err := os.Lstat(filepath.Join(root, plan.Path)); !os.IsNotExist(err) {
		problems = append(problems, "plan file still exists: "+plan.Path)
	}
	archives, err := filepath.Glob(filepath.Join(root, "docs/archive", plan.Meta["id"]+"-*.md"))
	if err != nil {
		return nil, nil, err
	}
	archiveExists := false
	for _, path := range archives {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			archiveExists = true
		}
	}
	if !archiveExists {
		problems = append(problems, "archive entry missing: docs/archive/"+plan.Meta["id"]+"-*.md")
	}
	res := cycle.Self(root, "lint")
	if res.Code != 0 {
		problems = append(problems, "docflow lint failed:\n"+res.Stdout+res.Stderr)
	}
	paths, err := closeChangedPaths(root, head)
	if err != nil {
		return nil, nil, err
	}
	for _, path := range paths {
		if !strings.HasPrefix(path, "docs/") && path != "CHANGELOG.md" && path != "README.md" && path != "README.ru.md" {
			problems = append(problems, "changed path outside closing scope: "+path)
		}
	}
	return paths, problems, nil
}

func runClose(ctx context.Context, root string, args Args, config map[string]any, plan *format.Plan, p *Progress, totals *runTotals, unavailable map[string]time.Time) (code int, runErr error) {
	id := plan.Meta["id"]
	totals.mu.Lock()
	totals.unit, totals.activity = "", ""
	totals.mu.Unlock()
	chain, err := closeExecutorChain(config, plan, args.RunArgs, unavailable)
	if err != nil {
		return 2, err
	}
	var route agents.Route
	defer func() {
		totals.events.emit("close", "", map[string]any{"plan": id, "route": eventRoute(route), "result": eventStatus(code, runErr), "exit_code": eventExitCode(code, runErr)})
	}()
	status, err := gitx.Git(root, "status", "--porcelain")
	if err != nil {
		return 2, err
	}
	if len(status) != 0 {
		return 2, runError(id + ": close needs a clean working tree")
	}
	head := cycle.Head(root)
	if head == nil {
		return 2, runError(id + ": close needs a Git HEAD")
	}
	env, err := closeEnvironment()
	if err != nil {
		return 2, err
	}
	logs, err := runDir(root)
	if err != nil {
		return 2, err
	}
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return 2, err
	}
	schema := filepath.Join(logs, "schema.json")
	data, err := json.MarshalIndent(agents.ResultSchema, "", "  ")
	if err != nil {
		return 2, err
	}
	if err := os.WriteFile(schema, append(data, '\n'), 0o644); err != nil {
		return 2, err
	}
	executable, err := os.Executable()
	if err != nil {
		executable = ""
	}
	prompt, problem, session := closePrompt(plan, executable), "", ""
	stalled, opened := false, false
	beforeQuota := make(map[string]agentio.Quota)
	for _, name := range chain.routeAgents() {
		beforeQuota[name] = append(agentio.Quota(nil), totals.quota[name]...)
	}
	defer func() {
		unitQuotas(p, totals, chain.agentsUsed(), beforeQuota)
		p.close()
	}()
	var available bool
	route, available = chain.pick(time.Now())
	if first, ok := chain.firstRoute(); available && ok && routeIdentity(route) != routeIdentity(first) {
		totals.switches++
	}
	for attempt := 1; attempt <= args.Attempts; attempt++ {
		text, resume := prompt, ""
		if problem != "" {
			note := problem
			if stalled {
				note = stalledFeedback(problem)
			}
			if session != "" {
				text, resume = feedback(note), session
			} else {
				text += "\n## Previous attempt\n\n" + feedback(note)
			}
		}
		var run agentio.AgentRun
		var agent map[string]any
		var live *agentio.Liveness
		// timeout is the cap of the route in flight: the route changes inside the
		// retry loop, so the value is set with the profile and read again after the
		// call ends, where the cap report names it.
		var timeout time.Duration
		waitingKind := ""
		for limits, throttles := 0, 0; ; {
			for !available {
				until, found := chain.earliest()
				if !found {
					return 2, runError(id + ": close executor chain has no available route or reset time")
				}
				line := fmt.Sprintf("%s: all close executors unavailable until %s", id, until.Local().Format("15:04"))
				if args.NoWait || limits >= agentio.LimitTries {
					totals.stop = line
					p.line(line, true, "")
					return 7, nil
				}
				limits++
				if err := waitForExecutorChain(ctx, p, line, until); err != nil {
					return 7, err
				}
				previous := route
				route, available = chain.pick(time.Now())
				if available && routeIdentity(previous) != routeIdentity(route) {
					totals.switches++
					totals.events.emit("route_switch", "", map[string]any{"plan": id, "from": eventRoute(previous), "to": eventRoute(route), "kind": waitingKind, "close": true})
				}
			}
			agent = profile(config, route.Agent)
			if agent == nil {
				return 2, runError(id + ": close: no agent profile '" + route.Agent + "'")
			}
			timeout = agentCallTimeout(plan, agent)
			argv, stdin, err := agents.BuildCommand(agent, text, route.Model, schema, resume, route.Effort, format.PlanPermissions(plan))
			if err != nil {
				return 2, err
			}
			log, err := nextCallLog(logs, id+".close")
			if err != nil {
				return 2, err
			}
			rel, _ := filepath.Rel(root, log)
			rel = filepath.ToSlash(rel)
			// One block per closing: a retry or a route switch continues it.
			if opened {
				p.executor(route)
			} else {
				p.unit(id, "close plan", route, time.Now())
				opened = true
			}
			totals.events.emit("attempt", "", map[string]any{"plan": id, "n": attempt, "route": eventRoute(route), "log": rel, "close": true})
			live = agentio.NewLiveness(stringValue(agent["stream"]))
			totals.mu.Lock()
			totals.activity = ""
			totals.mu.Unlock()
			p.spin(func() string { return livenessLabel(id+" close", live) }, totals.lastActivity)
			run, err = agentio.CallAgent(root, argv, stdin, log, agentio.CallOptions{Context: ctx, Env: env, Timeout: timeout, Idle: agentIdleTimeout(agent), Liveness: live, OnStreamEvent: func(event agentio.Event) {
				parsed := agentio.StreamEvent(agent, event)
				if quota, ok := parsed["quota"].(agentio.Quota); ok {
					totals.mergeQuota(quotaName(route), quota)
				} else if activity, ok := parsed["activity"].(string); ok {
					totals.mu.Lock()
					totals.activity = activity
					totals.mu.Unlock()
				}
			}})
			p.stop()
			if err != nil {
				return 7, err
			}
			output := agentio.AgentOutput(agent, run.Stdout)
			found, _ := agentio.JSONPath(output, stringValue(agent["session"])).(string)
			if found != "" {
				session = found
			}
			if agent["stream"] == "codex" {
				thread, _ := agentio.JSONPath(output, "json:thread_id").(string)
				totals.mergeQuota(route.Agent, agentio.CodexQuota(thread))
			}
			refreshQuota(totals, route, agent)
			spent := agentio.RunCost(agent, run)
			totals.cost = agentio.Add(totals.cost, spent)
			if err := agentio.RecordCall(root, id, found, agentio.RunTokens(agent, run), spent); err != nil {
				return 2, err
			}
			refusal := agentio.Classify(agent, run, time.Now())
			if refusal == nil {
				break
			}
			if refusal.Kind == agentio.RefusalLimit && refusal.Reset == nil {
				refusal.Reset = quotaRefusalReset(route, agent)
			}
			if chain.enabled && (refusal.Kind == agentio.RefusalLimit || refusal.Kind == agentio.RefusalOutage) {
				if _, err := chain.mark(route, refusal, time.Now()); err != nil {
					return 2, err
				}
				previous := route
				next, ok := chain.pick(time.Now())
				available = ok
				waitingKind = refusal.Kind
				if ok {
					route = next
				}
				if !ok || routeIdentity(previous) != routeIdentity(route) {
					context, err := previousExecutorAttempt(root, previous, agent, refusal, run)
					if err != nil {
						return 2, err
					}
					prompt = closePrompt(plan, executable) + context
					session, text, resume = "", prompt, ""
					if ok {
						totals.switches++
						totals.events.emit("route_switch", "", map[string]any{"plan": id, "from": eventRoute(previous), "to": eventRoute(route), "kind": refusal.Kind, "close": true})
					}
					continue
				}
			}
			limits++
			refusal.Kind, throttles = retryRefusalKind(refusal.Kind, throttles)
			if args.NoWait || limits >= agentio.LimitTries {
				totals.stop = id + ": close: " + refusalLabel(refusal.Kind)
				p.line(totals.stop, true, "")
				return 7, nil
			}
			if err := waitRefusal(ctx, agent, refusal, limitProgress{p}); err != nil {
				return 7, err
			}
			if session != "" {
				text, resume = agentio.RefusalPrompt, session
			}
		}
		if auth := agentio.AuthReason(agent, run); auth != "" {
			totals.stop = id + ": close: " + route.Agent + " is not logged in: " + auth
			p.line(totals.stop, true, "")
			return 7, nil
		}
		result := agentio.ExtractResult(agent, run.Stdout)
		stalled = false
		capLine := ""
		switch {
		case run.Stalled:
			stalled = true
			problem = stallProblem(agentIdleTimeout(agent))
		case run.TimedOut:
			problem = "close agent timed out"
			capLine = capReport(fmt.Sprintf("close attempt %d", attempt), timeout, live, totals.lastActivity(), time.Now())
		case run.Code != 0:
			problem = fmt.Sprintf("close agent exited with code %d", run.Code)
		case agentio.JSONPath(agentio.AgentOutput(agent, run.Stdout), stringValue(agent["error"])) == true:
			problem = "close agent reported an error"
		default:
			problem = agentio.ResultProblem(result)
		}
		if problem != "" {
			if capLine != "" {
				// The cap ended a closing call that was still working: the attempt is
				// resumed on the same session, not reported as a problem.
				p.resume(capLine)
				totals.noteCap(id, timeout, route)
			} else {
				p.line(id+": close: "+problem, true, "")
			}
			continue
		}
		if result["status"] == "blocked" {
			totals.stop = id + ": close blocked: " + stringValue(result["reason"])
			p.line(totals.stop, true, "")
			return 3, nil
		}
		paths, problems, err := closeChecks(root, plan, *head)
		if err != nil {
			return 2, err
		}
		if len(problems) > 0 {
			totals.stop = id + ": close checks failed:\n- " + strings.Join(problems, "\n- ")
			p.line(totals.stop, true, "")
			return 2, nil
		}
		if _, err := gitx.Git(root, append([]string{"--literal-pathspecs", "add", "-A", "--"}, paths...)...); err != nil {
			return 2, err
		}
		if _, err := gitx.Git(root, append([]string{"--literal-pathspecs", "commit", "--only", "-m", id + ": close plan", "--"}, paths...)...); err != nil {
			return 2, err
		}
		sha, err := gitx.Git(root, "rev-parse", "--short", "HEAD")
		if err != nil {
			return 2, err
		}
		totals.events.emit("commit", "", map[string]any{"plan": id, "sha": strings.TrimSpace(string(sha)), "close": true})
		totals.unverified += len(resultStrings(result["unverified"]))
		totals.notification = id + ": closed"
		p.line(totals.notification, false, "")
		return 0, nil
	}
	totals.stop = id + ": close attempts exhausted: " + problem
	p.line(totals.stop, true, "")
	return 7, nil
}
