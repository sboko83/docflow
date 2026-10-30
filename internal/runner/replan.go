package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/gitx"
	"bsv-docflow/internal/lint"
)

func rawPlan(plan *format.Plan, start, end int) string {
	return strings.Join(plan.Raw[start-1:end], "")
}

func replanSpace(r rune) bool { return unicode.IsSpace(r) || (r >= '\x1c' && r <= '\x1f') }

func replanPrompt(plan *format.Plan, uid, trigger string, dirty []string) (string, error) {
	unit := plan.Unit(uid)
	if unit == nil {
		return "", runError(fmt.Sprintf("%s: no unit %s", plan.Path, uid))
	}
	text := fmt.Sprintf("# Replan %s %s\n\nPlan: %s %s (%s)\n\n## Trigger\n%s\n\n", uid, unit.Title, plan.Meta["id"], plan.Meta["title"], plan.Path, strings.TrimFunc(trigger, replanSpace))
	if field := unit.Field("Handoff"); field != nil {
		text += fmt.Sprintf("## Handoff of %s\n\n%s\n\n", uid, strings.TrimRightFunc(rawPlan(plan, field.Line+1, field.End), replanSpace))
	}
	text += "## Tree\n"
	if len(dirty) == 0 {
		text += "clean: no uncommitted work outside the plan\n"
	} else {
		text += strings.Join(dirty, "\n") + "\n"
	}
	return text + "\n" + replanRules, nil
}

func checkReplan(before, after, uid string) []string {
	old, err := format.ParsePlan(before, "plan")
	if err != nil {
		return []string{"the plan no longer parses: " + err.Error()}
	}
	new, err := format.ParsePlan(after, "plan")
	if err != nil {
		return []string{"the plan no longer parses: " + err.Error()}
	}
	var problems []string
	if !reflect.DeepEqual(old.Meta, new.Meta) {
		problems = append(problems, "plan header changed")
	}
	for _, name := range []string{"Goal", "Constraints", "Prerequisites"} {
		a, b := old.Section(name), new.Section(name)
		if (a == nil) != (b == nil) || (a != nil && b != nil && rawPlan(old, a.Line, a.End) != rawPlan(new, b.Line, b.End)) {
			problems = append(problems, "section "+name+" changed")
		}
	}
	newUnits := make(map[string]*format.Unit, len(new.Units))
	for _, u := range new.Units {
		newUnits[u.ID] = u
	}
	for _, u := range old.Units {
		v := newUnits[u.ID]
		if v == nil {
			gone := "unit " + u.ID + " removed"
			if doneStatus(u.Status) {
				gone += " ([" + u.Status + "])"
			}
			problems = append(problems, gone)
			continue
		}
		if doneStatus(u.Status) {
			if v.Status != u.Status || rawPlan(old, u.Line, u.End) != rawPlan(new, v.Line, v.End) {
				problems = append(problems, fmt.Sprintf("unit %s [%s] changed", u.ID, u.Status))
			}
			continue
		}
		// A unit open when replan runs must not become [x]: the runner skips
		// a [x] unit and its check would vanish without a trace, so replan
		// ends the run instead of recording it - the guard against an agent
		// silencing a check. Reference units read the same rule.
		if v.Status == "x" {
			problems = append(problems, "unit "+u.ID+" marked [x] by replan")
			continue
		}
		role := anchorRole(u)
		if role == "" {
			continue
		}
		if v.Status == "-" {
			problems = append(problems, fmt.Sprintf("unit %s (%s) skipped", u.ID, role))
			continue
		}
		for _, name := range []string{"Verify", "Do", "Role"} {
			if fieldText(old, u, name) != fieldText(new, v, name) {
				problems = append(problems, fmt.Sprintf("unit %s (%s): %s changed", u.ID, role, name))
			}
		}
	}
	if target := newUnits[uid]; target != nil && target.Status == "!" {
		problems = append(problems, "unit "+uid+" is still [!]")
	}
	rep := &lint.Report{}
	lint.Plan(rep, new)
	for _, issue := range rep.Errors() {
		problems = append(problems, fmt.Sprintf("lint: %d: %s", issue.Line, issue.Text))
	}
	return problems
}

// anchorRole returns the role key of a unit whose checks replan must not
// touch: review and acceptance units are the reference checks of the plan,
// the guard against an agent weakening its own work. The status marker and
// Depends: of such a unit do change - replan lifts the block and adds the
// fix units it depends on.
func anchorRole(u *format.Unit) string {
	field := u.Field("Role")
	if field == nil {
		return ""
	}
	switch field.Value {
	case "review", "acceptance":
		return field.Value
	}
	return ""
}

// fieldText returns the raw lines of a unit field; a missing field reads as
// "" so adding, removing or rewriting an anchor field is one problem.
func fieldText(plan *format.Plan, u *format.Unit, key string) string {
	field := u.Field(key)
	if field == nil {
		return ""
	}
	return strings.TrimFunc(rawPlan(plan, field.Line, field.End), replanSpace)
}

// replanCheckChanges lists the checks a replan moved on the units it may
// edit: every unit of the old plan that has no reference role and is not
// `[x]`/`[-]` reports a changed `Verify:` value and a move into `[-]`. The
// reference units read as anchors in checkReplan - their changes stop the
// replan, so they need no record here. docflow writes the items into the
// plan's ## Decisions, prints them in the run and puts them in the replan
// event: the agent does not record what it weakened, docflow does.
func replanCheckChanges(before, after string) []string {
	old, err := format.ParsePlan(before, "plan")
	if err != nil {
		return nil
	}
	new, err := format.ParsePlan(after, "plan")
	if err != nil {
		return nil
	}
	units := make(map[string]*format.Unit, len(new.Units))
	for _, u := range new.Units {
		units[u.ID] = u
	}
	var items []string
	for _, u := range old.Units {
		if doneStatus(u.Status) || anchorRole(u) != "" {
			continue
		}
		v := units[u.ID]
		if v == nil {
			continue
		}
		if was, now := fieldPlainValue(old, u, "Verify"), fieldPlainValue(new, v, "Verify"); was != now {
			items = append(items, fmt.Sprintf("%s Verify: %s -> %s", u.ID, was, now))
		}
		if v.Status == "-" {
			items = append(items, u.ID+" skipped")
		}
	}
	return items
}

// fieldPlainValue returns a field's trimmed value; units that replan may
// edit are compared by value, the anchor units by raw text in checkReplan.
func fieldPlainValue(plan *format.Plan, u *format.Unit, key string) string {
	field := u.Field(key)
	if field == nil {
		return ""
	}
	return strings.TrimSpace(field.Value)
}

// recordReplanChecks appends the checks replan moved to the plan's
// ## Decisions section: ## Decisions reaches the review and acceptance
// prompts and the git history, so the record survives a run and outlives it.
func recordReplanChecks(planPath string, items []string) error {
	text, err := os.ReadFile(planPath)
	if err != nil {
		return err
	}
	plan, err := format.ParsePlan(string(text), planPath)
	if err != nil {
		return err
	}
	lines := make([]string, len(items))
	for i, item := range items {
		lines[i] = "Replan changed check: " + item
	}
	if err := plan.AppendSectionItems("Decisions", lines); err != nil {
		return err
	}
	return plan.Save(planPath)
}

func replanTreeProblems(root, before, after, uid string, dirty []string) []string {
	if before == after {
		return []string{"the plan is unchanged"}
	}
	plan, err := format.ParsePlan(after, "plan")
	if err != nil {
		return nil
	}
	unit := plan.Unit(uid)
	if unit == nil || doneStatus(unit.Status) {
		return nil
	}
	var problems []string
	refs := format.ContextRefs(plan)
	if read := unit.Field("Read"); read != nil {
		refs = append(refs, read.Items...)
	}
	for _, item := range refs {
		if _, err := format.RefSize(root, item.Text); err != nil {
			problems = append(problems, fmt.Sprintf("unit %s: %s", uid, err))
		}
	}
	if unit.Status == " " && len(dirty) > 0 {
		problems = append(problems, "unit "+uid+" is [ ] but the tree has uncommitted work; [~] resumes on it")
	}
	return problems
}

func restorePlan(path, text string) error {
	current, err := os.ReadFile(path)
	if err == nil && string(current) == text {
		return nil
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

func runReplan(ctx context.Context, root string, args Args, config map[string]any, plan *format.Plan, uid, trigger string, p *Progress, totals *runTotals, replans map[string]int, unavailable map[string]time.Time) (*int, error) {
	if trigger == "" || plan.Meta["replan"] == "" || args.Replans < 1 {
		return nil, nil
	}
	if !replanAllowed(replans, uid, args.Replans) {
		p.line(uid+": replan limit reached", true, "")
		return nil, nil
	}
	replans[uid]++
	code, err := replanUnit(ctx, root, args, config, uid, trigger, p, totals, unavailable)
	return &code, err
}

// replanAllowed applies the run's replan limits: at most two per unit and
// perPhase per phase. A phase review that finds a defect spends a replan by
// design, so a run-wide budget would stop a long plan on its review rounds;
// the phase budget still stops a review loop that does not converge.
func replanAllowed(replans map[string]int, uid string, perPhase int) bool {
	if replans[uid] >= 2 {
		return false
	}
	count := 0
	for id, n := range replans {
		if unitPhase(id) == unitPhase(uid) {
			count += n
		}
	}
	return count < perPhase
}

// unitPhase returns the phase part of a unit id: P001.2.3 -> P001.2.
func unitPhase(uid string) string {
	if i := strings.LastIndex(uid, "."); i > 0 {
		return uid[:i]
	}
	return uid
}

func replanUnit(ctx context.Context, root string, args Args, config map[string]any, uid, trigger string, p *Progress, totals *runTotals, unavailable map[string]time.Time) (code int, runErr error) {
	var route agents.Route
	var checkChanges []string
	defer func() {
		items := checkChanges
		if items == nil {
			items = []string{}
		}
		totals.events.emit("replan", uid, map[string]any{"route": eventRoute(route), "result": eventStatus(code, runErr), "exit_code": code, "check_changes": items})
	}()
	plan, _, err := cycle.FindUnit(root, uid)
	if err != nil {
		return 2, err
	}
	chain, err := replanExecutorChain(config, plan, args.RunArgs, unavailable)
	if err != nil {
		totals.stop = fmt.Sprintf("%s: replan: %s", uid, err)
		p.line(totals.stop, true, "")
		return 2, nil
	}
	if chain.enabled {
		for _, skipped := range markQuotaRoutes(chain.routes, totals.quota, unavailable, time.Now()) {
			p.line(fmt.Sprintf("%s: quota exhausted until %s, skipped", skipped.agent, skipped.until.Local().Format("15:04")), false, "")
		}
	}
	route, available := chain.pick(time.Now())
	for !available {
		earliest, found := chain.earliest()
		if !found {
			totals.stop = uid + ": replan executor chain has no available route or reset time"
			p.line(totals.stop+"; the plan is unchanged", true, "")
			return 7, nil
		}
		line := fmt.Sprintf("%s: replan executors unavailable until %s", uid, earliest.Local().Format("15:04"))
		if args.NoWait {
			totals.stop = line
			p.line(line+"; the plan is unchanged", true, "")
			return 7, nil
		}
		if err := waitForExecutorChain(ctx, p, line, earliest); err != nil {
			return 7, err
		}
		route, available = chain.pick(time.Now())
	}
	if chain.enabled {
		first, _ := chain.firstRoute()
		if routeIdentity(route) != routeIdentity(first) {
			totals.switches++
		}
	}
	name, model, effort := route.Agent, route.Model, route.Effort
	agent := profile(config, name)
	if agent == nil {
		totals.stop = fmt.Sprintf("%s: replan: no agent profile '%s'", uid, name)
		p.line(totals.stop, true, "")
		return 2, nil
	}
	repos, err := lint.ProjectRepos(root)
	if err != nil {
		return 2, err
	}
	dirty, err := gitx.DirtyFiles(root, plan.Path, repos)
	if err != nil {
		return 2, err
	}
	prompt, err := replanPrompt(plan, uid, trigger, dirty)
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
	start := time.Now()
	beforeQuota := make(map[string]agentio.Quota)
	for _, routeAgent := range chain.routeAgents() {
		beforeQuota[routeAgent] = append(agentio.Quota(nil), totals.quota[routeAgent]...)
	}
	defer func() {
		unitQuotas(p, totals, chain.agentsUsed(), beforeQuota)
		p.unitEnd(uid)
	}()
	p.unit(uid, "replan", route, start)
	planPath := filepath.Join(root, plan.Path)
	before, err := os.ReadFile(planPath)
	if err != nil {
		return 2, err
	}
	tree, err := cycle.Fingerprint(root, plan.Path, lint.ProjectRepos)
	if err != nil {
		return 2, err
	}
	head := cycle.Head(root)
	beforeUnits := len(plan.Units)
	onEvent := func(obj agentio.Event) {
		event := agentio.StreamEvent(agent, obj)
		if quota, ok := event["quota"].(agentio.Quota); ok {
			totals.mergeQuota(name, quota)
		} else if activity, ok := event["activity"].(string); ok {
			totals.mu.Lock()
			totals.activity = activity
			totals.mu.Unlock()
		}
	}
	stop := func(code int, why, line, style string) (int, error) {
		p.line(line, style == "", style)
		totals.stop = why
		return code, restorePlan(planPath, string(before))
	}
	session, problem := "", ""
	stalled := false
	timeout := agentCallTimeout(plan, agent)
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
		// live tracks the call in flight: the spinner reads it every frame, the
		// report reads it after the call ends.
		var live *agentio.Liveness
		for limits, throttles := 0, 0; ; {
			argv, stdin, err := agents.BuildCommand(agent, text, model, schema, resume, effort, format.PlanPermissions(plan))
			if err != nil {
				return 2, err
			}
			log, err := nextRunCallLog(logs, uid, true)
			if err != nil {
				return 2, err
			}
			rel, _ := filepath.Rel(root, log)
			rel = filepath.ToSlash(rel)
			p.detail(fmt.Sprintf("replan attempt %d, log %s", attempt, rel))
			totals.events.emit("attempt", uid, map[string]any{"n": attempt, "route": eventRoute(route), "log": rel, "replan": true})
			live = agentio.NewLiveness(stringValue(agent["stream"]))
			p.spin(func() string {
				return livenessLabel(uid+" replan "+agentio.Duration(time.Since(start).Seconds()), live)
			}, func() string { totals.mu.Lock(); defer totals.mu.Unlock(); return totals.activity })
			run, err = agentio.CallAgent(root, argv, stdin, log, agentio.CallOptions{Timeout: timeout, OnStreamEvent: onEvent, Context: ctx, Env: append(os.Environ(), agentio.RunnerEnv+"="+uid), Idle: agentIdleTimeout(agent), Liveness: live})
			p.stop()
			if err != nil {
				return 7, err
			}
			out := agentio.AgentOutput(agent, run.Stdout)
			if agent["stream"] == "codex" {
				thread, _ := agentio.JSONPath(out, "json:thread_id").(string)
				totals.mergeQuota(name, agentio.CodexQuota(thread))
			}
			// The stream carries quota for claude and codex only; the
			// others are re-probed so the delta and mid-run exhaustion show up.
			refreshQuota(totals, route, agent)
			found, _ := agentio.JSONPath(out, stringValue(agent["session"])).(string)
			if found != "" {
				session = found
			}
			spent, tokens := agentio.RunCost(agent, run), agentio.RunTokens(agent, run)
			if err := agentio.RecordCall(root, uid, found, tokens, spent); err != nil {
				return 2, err
			}
			totals.cost = agentio.Add(totals.cost, spent)
			refusal := agentio.Classify(agent, run, time.Now())
			if refusal == nil {
				break
			}
			if refusal.Kind == agentio.RefusalLimit && refusal.Reset == nil {
				refusal.Reset = quotaRefusalReset(route, agent)
			}
			if chain.enabled && (refusal.Kind == agentio.RefusalLimit || refusal.Kind == agentio.RefusalOutage) {
				now := time.Now()
				until, err := chain.mark(route, refusal, now)
				if err != nil {
					return 2, err
				}
				if len(chain.routes) > 1 {
					next, available := chain.pick(now)
					if available && routeIdentity(next) != routeIdentity(route) {
						previous := route
						totals.events.emit("route_switch", uid, map[string]any{"from": eventRoute(route), "to": eventRoute(next), "kind": refusal.Kind, "replan": true})
						p.line(fmt.Sprintf("%s: replan %s: %s until %s; switching to %s", uid, routeName(route), refusal.Kind, until.Local().Format("15:04"), routeName(next)), false, "")
						p.executor(next)
						route = next
						name, model, effort = route.Agent, route.Model, route.Effort
						agent = profile(config, name)
						if agent == nil {
							return stop(2, uid+": replan: no agent profile '"+name+"'", uid+": replan: no agent profile '"+name+"'; the plan is restored", "")
						}
						timeout = agentCallTimeout(plan, agent)
						prompt, err = replanPrompt(plan, uid, trigger, dirty)
						if err != nil {
							return 2, err
						}
						previousAttempt, err := previousExecutorAttempt(root, previous, profile(config, previous.Agent), refusal, run)
						if err != nil {
							return stop(2, err.Error(), uid+": replan: "+err.Error()+"; the plan is restored", "")
						}
						prompt += previousAttempt
						session = ""
						text, resume = prompt, ""
						totals.switches++
						continue
					}
					if !available {
						for !available {
							earliest, found := chain.earliest()
							if !found {
								return stop(2, uid+": replan executor chain is exhausted without a reset time", uid+": replan executor chain is exhausted without a reset time; the plan is restored", "")
							}
							line := fmt.Sprintf("%s: all replan executors unavailable until %s", uid, earliest.Local().Format("15:04"))
							if args.NoWait || limits+1 >= agentio.LimitTries {
								why := line
								if !args.NoWait {
									why += fmt.Sprintf(" (%d in a row)", limits+1)
								}
								return stop(7, why, why+"; the plan is restored", "")
							}
							limits++
							if err := waitForExecutorChain(ctx, p, line, earliest); err != nil {
								return 7, err
							}
							next, available = chain.pick(time.Now())
						}
						if routeIdentity(next) != routeIdentity(route) {
							previous := route
							totals.events.emit("route_switch", uid, map[string]any{"from": eventRoute(route), "to": eventRoute(next), "kind": refusal.Kind, "replan": true})
							p.line(uid+": replan switching to "+routeName(next)+" after the provider refusal wait", false, "")
							p.executor(next)
							route = next
							name, model, effort = route.Agent, route.Model, route.Effort
							agent = profile(config, name)
							if agent == nil {
								return stop(2, uid+": replan: no agent profile '"+name+"'", uid+": replan: no agent profile '"+name+"'; the plan is restored", "")
							}
							timeout = agentCallTimeout(plan, agent)
							prompt, err = replanPrompt(plan, uid, trigger, dirty)
							if err != nil {
								return 2, err
							}
							previousAttempt, err := previousExecutorAttempt(root, previous, profile(config, previous.Agent), refusal, run)
							if err != nil {
								return stop(2, err.Error(), uid+": replan: "+err.Error()+"; the plan is restored", "")
							}
							prompt += previousAttempt
							session = ""
							text, resume = prompt, ""
							totals.switches++
						} else if session != "" {
							text, resume = agentio.RefusalPrompt, session
						}
						continue
					}
				}
			}
			limits++
			refusal.Kind, throttles = retryRefusalKind(refusal.Kind, throttles)
			if args.NoWait || limits >= agentio.LimitTries {
				why := fmt.Sprintf("%d in a row", limits)
				if args.NoWait {
					why = "--no-wait"
				}
				line := uid + ": replan " + refusalLabel(refusal.Kind) + " (" + why + ")"
				return stop(7, line, line+"; the plan is restored", "")
			}
			if err := waitRefusal(ctx, agent, refusal, limitProgress{p}); err != nil {
				return 7, err
			}
			if session != "" {
				text, resume = agentio.RefusalPrompt, session
			}
		}
		if auth := agentio.AuthReason(agent, run); auth != "" {
			return stop(7, uid+": "+name+" is not logged in", uid+": "+name+" is not logged in: "+auth+"; the plan is restored", "")
		}
		var result map[string]any
		capLine := ""
		stalled = false
		switch {
		case run.Stalled:
			stalled = true
			problem = stallProblem(agentIdleTimeout(agent))
		case run.TimedOut:
			problem = fmt.Sprintf("timed out after %v s", timeout.Seconds())
			capLine = capReport(fmt.Sprintf("replan attempt %d", attempt), timeout, live, totals.lastActivity(), time.Now())
		case run.Code != 0:
			problem = fmt.Sprintf("the agent exited with code %d", run.Code)
		case agentio.JSONPath(agentio.AgentOutput(agent, run.Stdout), stringValue(agent["error"])) == true:
			problem = "the agent reported an error"
		default:
			result = agentio.ExtractResult(agent, run.Stdout)
			problem = agentio.ResultProblem(result)
		}
		if problem != "" {
			if result == nil {
				if reason := agentio.FailureReason(agent, run); reason != "" {
					problem += ": " + reason
				}
			}
			if capLine != "" {
				// The cap ended a replan call that was still working: the attempt
				// is resumed on the same session, not reported as a problem.
				p.resume(capLine)
				totals.noteCap(uid, timeout, route)
			} else {
				p.line(fmt.Sprintf("%s: replan attempt %d: %s", uid, attempt, problem), true, "")
			}
			continue
		}
		if result["status"] == "blocked" {
			line := uid + ": replan blocked (needs human): " + stringValue(result["reason"])
			return stop(3, line, line, "red")
		}
		var problems []string
		if !reflect.DeepEqual(cycle.Head(root), head) {
			problems = append(problems, "HEAD moved during the replan")
		}
		afterTree, err := cycle.Fingerprint(root, plan.Path, lint.ProjectRepos)
		if err != nil {
			return 2, err
		}
		if afterTree != tree {
			problems = append(problems, "the tree changed outside the plan file")
		}
		after, err := os.ReadFile(planPath)
		if err != nil {
			return 2, err
		}
		problems = append(problems, checkReplan(string(before), string(after), uid)...)
		problems = append(problems, replanTreeProblems(root, string(before), string(after), uid, dirty)...)
		if len(problems) > 0 {
			if err := restorePlan(planPath, string(before)); err != nil {
				return 2, err
			}
			p.line(uid+": replan rejected; the plan is restored", true, "")
			for _, item := range problems {
				p.line("  - "+item, true, "")
			}
			totals.stop = uid + ": replan rejected"
			return 3, nil
		}
		// The checks replan moved on the units it may edit are recorded by
		// docflow, not by the agent: ## Decisions, the run output, the run
		// summary and the replan event all carry them.
		if changes := replanCheckChanges(string(before), string(after)); len(changes) > 0 {
			if err := recordReplanChecks(planPath, changes); err != nil {
				return stop(2, uid+": replan: "+err.Error(), uid+": replan: "+err.Error()+"; the plan is restored", "")
			}
			checkChanges = changes
			for _, item := range changes {
				p.line(uid+": replan changed check: "+item, false, "yellow")
			}
			totals.checkChanges += len(changes)
		}
		sha, err := cycle.CommitReplan(root, plan.Path, uid)
		if err != nil {
			p.line(fmt.Sprintf("%s: replan commit failed: %s", uid, err), true, "")
			totals.stop = uid + ": replan commit failed"
			var e clierrors.DocflowError
			if errors.As(err, &e) {
				return e.Code, nil
			}
			return 2, err
		}
		new, _ := format.ParsePlan(string(after), plan.Path)
		totals.events.emit("commit", uid, map[string]any{"sha": sha, "replan": true})
		p.result(uid, "replanned", fmt.Sprintf("%s (+%d units)", sha, len(new.Units)-beforeUnits))
		totals.replans++
		return 0, nil
	}
	line := fmt.Sprintf("%s: replan gave up after %d attempts", uid, args.Attempts)
	return stop(7, line, line+"; the plan is restored", "")
}
