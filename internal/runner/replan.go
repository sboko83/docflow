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
		} else if doneStatus(u.Status) && (v.Status != u.Status || rawPlan(old, u.Line, u.End) != rawPlan(new, v.Line, v.End)) {
			problems = append(problems, fmt.Sprintf("unit %s [%s] changed", u.ID, u.Status))
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

func runReplan(ctx context.Context, root string, args Args, config map[string]any, plan *format.Plan, uid, trigger string, p *Progress, totals *runTotals, replans map[string]int) (*int, error) {
	if trigger == "" || plan.Meta["replan"] == "" || args.Replans < 1 {
		return nil, nil
	}
	count := 0
	for _, n := range replans {
		count += n
	}
	if replans[uid] >= 2 || count >= args.Replans {
		p.line(uid+": replan limit reached", true, "")
		return nil, nil
	}
	replans[uid]++
	code, err := replanUnit(ctx, root, args, config, uid, trigger, p, totals)
	return &code, err
}

func replanUnit(ctx context.Context, root string, args Args, config map[string]any, uid, trigger string, p *Progress, totals *runTotals) (int, error) {
	plan, _, err := cycle.FindUnit(root, uid)
	if err != nil {
		return 2, err
	}
	name, model, parsedEffort, err := lint.ParseExecutor(plan.Meta["replan"])
	if err != nil {
		totals.stop = fmt.Sprintf("%s: replan: %s", uid, err)
		p.line(totals.stop, true, "")
		return 2, nil
	}
	effort := ""
	if parsedEffort != nil {
		effort = *parsedEffort
	}
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
	p.unit(uid, "replan", agents.Route{Agent: name, Model: model, Effort: effort})
	start := time.Now()
	beforeQuota := append(agentio.Quota(nil), totals.quota[name]...)
	defer func() {
		p.elapsed(uid, time.Since(start).Seconds())
		unitQuota(p, totals, name, beforeQuota)
	}()
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
	timeout := agentCallTimeout(plan, agent)
	for attempt := 1; attempt <= args.Attempts; attempt++ {
		text, resume := prompt, ""
		if problem != "" {
			if session != "" {
				text, resume = feedback(problem), session
			} else {
				text += "\n## Previous attempt\n\n" + feedback(problem)
			}
		}
		var run agentio.AgentRun
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
			p.detail(fmt.Sprintf("replan attempt %d, log %s", attempt, rel))
			p.spin(func() string { return uid + " replan " + agentio.Duration(time.Since(start).Seconds()) }, func() string { totals.mu.Lock(); defer totals.mu.Unlock(); return totals.activity })
			run, err = agentio.CallAgent(root, argv, stdin, log, agentio.CallOptions{Timeout: timeout, OnStreamEvent: onEvent, Context: ctx, Env: append(os.Environ(), agentio.RunnerEnv+"="+uid)})
			p.stop()
			if err != nil {
				return 7, err
			}
			out := agentio.AgentOutput(agent, run.Stdout)
			if agent["stream"] == "codex" {
				thread, _ := agentio.JSONPath(out, "json:thread_id").(string)
				totals.mergeQuota(name, agentio.CodexQuota(thread))
			}
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
		switch {
		case run.TimedOut:
			problem = fmt.Sprintf("timed out after %v s", timeout.Seconds())
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
			p.line(fmt.Sprintf("%s: replan attempt %d: %s", uid, attempt, problem), true, "")
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
		p.result(uid, "replanned", fmt.Sprintf("%s (+%d units)", sha, len(new.Units)-beforeUnits))
		totals.replans++
		return 0, nil
	}
	line := fmt.Sprintf("%s: replan gave up after %d attempts", uid, args.Attempts)
	return stop(7, line, line+"; the plan is restored", "")
}
