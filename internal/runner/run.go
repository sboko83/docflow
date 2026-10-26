package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/gitx"
	"bsv-docflow/internal/lint"
	"bsv-docflow/internal/skills"
)

type Args struct {
	agents.RunArgs
	Attempts, Replans                                                     int
	MaxUnits                                                              *int
	NoWait, NoNotify, DryRun, All, PrintConfig, Yes, Verbose, Stop, Close bool
}

type unitRow struct {
	id       string
	seconds  float64
	cost     *float64
	in, out  *int
	attempts int
}

type runTotals struct {
	start                      time.Time
	wall                       float64
	cost                       *float64
	rows                       []unitRow
	planID, stop, notification string
	unverified, replans        int
	switches                   int
	mu                         sync.Mutex
	unit, activity             string
	quota, quotaStart          map[string]agentio.Quota
	names                      []string
	events                     *eventStream
}

func (t *runTotals) mergeQuota(name string, quota agentio.Quota) {
	if name == localQuota || len(quota) == 0 {
		return
	}
	windows := make([]map[string]any, 0, len(quota))
	for _, w := range quota {
		windows = append(windows, map[string]any{"label": w.Label, "used": w.Used, "reset": w.Reset})
	}
	t.events.emit("quota", t.unit, map[string]any{"name": name, "windows": windows})
	if _, ok := t.quota[name]; !ok {
		t.names = append(t.names, name)
	}
	for _, w := range quota {
		q := t.quota[name]
		q.Set(w.Label, w.Used, w.Reset)
		t.quota[name] = q
		start := t.quotaStart[name]
		found := false
		for _, old := range start {
			if old.Label == w.Label {
				found = true
				break
			}
		}
		if !found {
			start.Set(w.Label, w.Used, w.Reset)
			t.quotaStart[name] = start
		}
	}
}

func (t *runTotals) namedQuotas() []NamedQuota {
	result := make([]NamedQuota, 0, len(t.names))
	for _, name := range t.names {
		result = append(result, NamedQuota{name, t.quota[name]})
	}
	return result
}

// Run drives only the current plan. Cycle commands run as subprocesses of this
// binary so their order checks and persisted state stay identical to manual use.
func Run(root string, args Args, stdout, stderr io.Writer, skillDirectory string) (code int, runErr error) {
	if args.PrintConfig {
		_, err := io.WriteString(stdout, agents.DefaultAgents)
		return 0, err
	}
	if args.Stop {
		return requestStop(root, stdout, stderr)
	}
	_ = os.Unsetenv(agentio.RunnerEnv)
	if args.Attempts < 1 {
		return 2, runError("run: --attempts must be at least 1")
	}
	if args.Replans < 0 {
		return 2, runError("run: --replans must not be negative")
	}
	for _, flag := range []struct {
		name string
		set  bool
	}{{"plan", args.Plan != ""}, {"all", args.All}} {
		if flag.set && !args.DryRun {
			return 2, runError("run: --" + flag.name + " needs --dry-run; a run takes the current unit of the Queue")
		}
	}
	sel, err := runSelection(root, args)
	if err != nil {
		return 2, err
	}
	config, err := agents.RunConfig(sel.Plan, &args.RunArgs)
	if err != nil {
		return 2, err
	}
	if args.DryRun {
		return dryRun(root, args, config, sel, stdout, stderr)
	}
	p := newProgress(stdout, stderr, args.Verbose)
	defer p.stop()
	now := time.Now()
	totals := &runTotals{start: now, wall: epoch(now), quota: map[string]agentio.Quota{}, quotaStart: map[string]agentio.Quota{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, stopSignals...)
	defer signal.Stop(signals)
	var stopped os.Signal
	var signalMu sync.Mutex
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case s := <-signals:
			signalMu.Lock()
			stopped = s
			signalMu.Unlock()
			cancel()
		case <-finished:
		}
	}()
	release, err := runLock(root)
	if err != nil {
		return 2, err
	}
	defer release()
	stop, err := stopFile(root)
	if err != nil {
		return 2, err
	}
	_ = os.Remove(stop)
	input := newRunInput(os.Stdin)
	if sel.Kind == "blocked" {
		needs, reason := cycle.BlockedNeeds(sel.Unit)
		unblocked, cancelled := askUnblock(ctx, root, sel.Unit.ID, needs, reason, args.Yes, p, input, stdout)
		if cancelled {
			if code := signalStopCode(p, totals, &signalMu, &stopped, sel.Unit.ID+" stays [!]"); code != 0 {
				return code, nil
			}
			return clierrors.EXIT_NOTHING, nil
		}
		if unblocked {
			sel, err = runSelection(root, args)
			if err != nil {
				return 2, err
			}
			config, err = agents.RunConfig(sel.Plan, &args.RunArgs)
			if err != nil {
				return 2, err
			}
		}
	}
	if sel.Kind == "unit" || sel.Kind == "close" {
		warnings, err := skills.SkillsWarnings(skillDirectory, root, agents.PlanAgents(config, sel.Plan, args.RunArgs))
		if err != nil {
			return 2, err
		}
		for _, line := range warnings {
			p.line(line, false, "yellow")
		}
		if code := runPrerequisites(root, sel.Plan, p); code != 0 {
			notify("a prerequisite failed", args.NoNotify, stderr)
			return code, nil
		}
		if code := confirmRunStartWithIO(sel.Plan, args.Yes, p, input, stdout); code != 0 {
			return code, nil
		}
	}
	totals.events = newEventStream(root, now, stderr)
	fields := map[string]any{"plan": "", "units": 0}
	if sel.Plan != nil {
		fields["plan"], fields["units"] = sel.Plan.Meta["id"], len(sel.Plan.Units)
	}
	totals.events.emit("run_start", "", fields)
	defer func() {
		reason := totals.stop
		if reason == "" {
			reason = totals.notification
		}
		if runErr != nil {
			reason = runErr.Error()
		}
		if reason == "" {
			reason = fmt.Sprintf("exit %d", code)
		}
		totals.events.emit("run_end", "", map[string]any{"reason": reason, "exit_code": eventExitCode(code, runErr)})
		totals.events.close()
	}()
	if isTerminal(input.stream) {
		p.line("run: s + Enter stops after the current unit; Ctrl-C stops now", false, "")
		restore := noEcho(input.stream)
		defer restore()
		go watchKeys(root, input.stream, p, nil, func() string { totals.mu.Lock(); defer totals.mu.Unlock(); return totals.unit })
	}
	defer os.Remove(stop)
	code, err = runLoop(ctx, root, args, config, p, totals)
	if code := signalStopCode(p, totals, &signalMu, &stopped, "the current unit stays [~]"); code != 0 {
		return code, nil
	}
	if totals.notification != "" {
		notify(totals.notification, args.NoNotify, stderr)
	}
	return code, err
}

// signalStopCode is the runner's stop-by-signal ending, shared by the two
// places a signal can reach the run: the unblock question at the start and the
// unit loop. It reports the signal once, names the unit's state in that clause
// (the start has not prompted it, the loop has), and returns the signal's exit
// code (Ctrl-C: the 130 of SIGINT); 0 when no signal arrived and the run goes
// on.
func signalStopCode(p *Progress, totals *runTotals, signalMu *sync.Mutex, stopped *os.Signal, state string) int {
	signalMu.Lock()
	s := *stopped
	signalMu.Unlock()
	if s == nil {
		return 0
	}
	totals.stop = "stopped by " + signalName(s)
	p.stop()
	p.line("run: stopped by "+signalName(s)+"; "+state, true, "")
	if totals.planID != "" {
		runSummary(p, totals)
	}
	return 128 + signalNumber(s)
}

func runLoop(ctx context.Context, root string, args Args, config map[string]any, p *Progress, totals *runTotals) (int, error) {
	planID, done := "", 0
	replans := map[string]int{}
	unavailable := make(map[string]time.Time)
	end := func(id, why string) (int, error) {
		return runEnd(ctx, root, id, args, config, p, totals, why, unavailable)
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		selectionArgs := args
		selectionArgs.RunArgs = agents.RunArgs{}
		selectionArgs.Close = args.Close && planID == ""
		sel, err := runSelection(root, selectionArgs)
		if err != nil {
			return 2, err
		}
		stop, err := stopFile(root)
		if err != nil {
			return 2, err
		}
		if _, err := os.Stat(stop); planID != "" && err == nil {
			p.line("run: stopped on request", false, "")
			return end(planID, "stopped on request")
		}
		maxed := args.MaxUnits != nil && done >= *args.MaxUnits
		if planID != "" && (sel.Plan == nil || sel.Plan.Meta["id"] != planID || sel.Kind == "nothing" || maxed) {
			why := "plan finished"
			if maxed {
				why = fmt.Sprintf("--max-units %d", *args.MaxUnits)
			}
			return end(planID, why)
		}
		if sel.Kind == "blocked" {
			p.line(sel.Unit.ID+" is blocked", true, "")
			if needs, _ := cycle.BlockedNeeds(sel.Unit); needs == "human" {
				p.line(unblockHint(sel.Unit.ID), false, "")
			}
			totals.notification = sel.Unit.ID + " is blocked"
			if planID != "" {
				runSummary(p, totals)
			}
			return 3, nil
		}
		if sel.Kind == "nothing" || (sel.Kind != "close" && args.MaxUnits != nil && *args.MaxUnits == 0) {
			p.line("nothing to do", true, "")
			totals.notification = "nothing to do"
			return 1, nil
		}
		if planID == "" {
			planID = sel.Plan.Meta["id"]
			totals.planID = planID
			p.start(planID, sel.Plan.Meta["title"], totals.wall, format.PlanPermissions(sel.Plan) == "full")
			if err := startQuota(config, sel.Plan, args, p, totals, unavailable); err != nil {
				return 2, err
			}
			p.close()
		}
		if sel.Kind == "close" {
			return end(planID, "plan finished")
		}
		totals.mu.Lock()
		totals.unit = sel.Unit.ID
		totals.mu.Unlock()
		chain, err := unitExecutorChain(config, sel.Plan, sel.Unit, args.RunArgs, unavailable)
		if err != nil {
			return 2, err
		}
		if chain.enabled {
			for _, skipped := range markQuotaRoutes(chain.routes, totals.quota, unavailable, time.Now()) {
				p.line(fmt.Sprintf("%s: quota exhausted until %s, skipped", skipped.agent, skipped.until.Local().Format("15:04")), false, "")
			}
		}
		before := make(map[string]agentio.Quota)
		for _, name := range chain.routeAgents() {
			before[name] = append(agentio.Quota(nil), totals.quota[name]...)
		}
		code, trigger, err := runUnit(ctx, root, args, config, chain, sel.Unit.ID, p, totals)
		unitQuotas(p, totals, chain.agentsUsed(), before)
		if err != nil {
			return code, err
		}
		if code != 0 {
			fixed, err := runReplan(ctx, root, args, config, sel.Plan, sel.Unit.ID, trigger, p, totals, replans, unavailable)
			if err != nil {
				return *fixed, err
			}
			if fixed != nil && *fixed == 0 {
				totals.stop = ""
				continue
			}
			if fixed != nil {
				code = *fixed
			}
			runSummary(p, totals)
			totals.notification = totals.stop
			if totals.notification == "" {
				totals.notification = stopNotify[code]
			}
			if totals.notification == "" {
				totals.notification = fmt.Sprintf("%s: exit %d", sel.Unit.ID, code)
			}
			return code, nil
		}
		done++
	}
}

func startQuota(config map[string]any, plan *format.Plan, args Args, p *Progress, totals *runTotals, unavailable map[string]time.Time) error {
	units := make([]*format.Unit, 0)
	for _, u := range plan.Units {
		if !doneStatus(u.Status) {
			units = append(units, u)
		}
	}
	if args.MaxUnits != nil {
		n := *args.MaxUnits
		if n < 0 {
			n += len(units)
			if n < 0 {
				n = 0
			}
		}
		if n < len(units) {
			units = units[:n]
		}
	}
	names := []string{}
	routes := []agents.Route{}
	add := func(name string) {
		if name == localQuota {
			return
		}
		// zai has no agent profile: the quota belongs to the z.ai
		// subscription behind pi, so only the profile check is skipped.
		if name != zaiQuota && profile(config, name)["quota"] == nil {
			return
		}
		for _, n := range names {
			if n == name {
				return
			}
		}
		names = append(names, name)
	}
	for _, unit := range units {
		chain, err := unitExecutorChain(config, plan, unit, args.RunArgs, unavailable)
		if err != nil {
			return err
		}
		routes = append(routes, chain.routes...)
		for _, route := range chain.routes {
			add(quotaName(route))
		}
	}
	if args.Close {
		chain, err := closeExecutorChain(config, plan, args.RunArgs, unavailable)
		if err != nil {
			return err
		}
		routes = append(routes, chain.routes...)
		for _, route := range chain.routes {
			add(quotaName(route))
		}
	}
	// The replan agent runs only after a block, but its quota still matters
	// for the run; a bad replan route is reported when replan starts.
	if len(units) > 0 && plan.Meta["replan"] != "" && args.Replans >= 1 {
		if chain, err := replanExecutorChain(config, plan, args.RunArgs, unavailable); err == nil {
			routes = append(routes, chain.routes...)
			for _, route := range chain.routes {
				add(quotaName(route))
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	quotas := make([]agentio.Quota, len(names))
	var wait sync.WaitGroup
	p.spin("quota", nil)
	for i, name := range names {
		wait.Add(1)
		go func(i int, name string) {
			defer wait.Done()
			if name == zaiQuota {
				quotas[i] = agentio.ProbeZaiQuota(agentio.QuotaProbeTimeout)
				return
			}
			quotas[i] = agentio.ProbeQuota(profile(config, name), agentio.QuotaProbeTimeout)
		}(i, name)
	}
	wait.Wait()
	p.stop()
	shown := []NamedQuota{}
	quotaByAgent := make(map[string]agentio.Quota, len(names))
	for i, name := range names {
		totals.mergeQuota(name, quotas[i])
		quotaByAgent[name] = quotas[i]
		shown = append(shown, NamedQuota{name, quotas[i]})
	}
	p.quotas(shown, nil, "", false)
	if args.Executors {
		for _, skipped := range markQuotaRoutes(routes, quotaByAgent, unavailable, time.Now()) {
			p.line(fmt.Sprintf("%s: quota exhausted until %s, skipped", skipped.agent, skipped.until.Local().Format("15:04")), false, "")
		}
	}
	return nil
}

func runEnd(ctx context.Context, root, id string, args Args, config map[string]any, p *Progress, totals *runTotals, why string, unavailable map[string]time.Time) (int, error) {
	plan, err := cycle.FindPlan(root, id)
	if err != nil {
		return 2, err
	}
	ready, err := planReady(root, plan)
	if err != nil {
		return 2, err
	}
	if ready {
		stopped := why == "stopped on request"
		why = id + ": ready to close"
		p.line(why, false, "")
		if args.Close && !stopped {
			code, err := runClose(ctx, root, args, config, plan, p, totals, unavailable)
			runSummary(p, totals)
			return code, err
		}
	}
	totals.notification = why
	runSummary(p, totals)
	return 0, nil
}

func runSummary(p *Progress, totals *runTotals) {
	for _, row := range totals.rows {
		parts := []string{row.id, agentio.Duration(row.seconds)}
		if row.cost != nil {
			parts = append(parts, fmt.Sprintf("$%.2f", *row.cost))
		}
		if text := tokenText(row.in, row.out); text != "" {
			parts = append(parts, "tokens "+text)
		}
		parts = append(parts, fmt.Sprintf("%d attempt%s", row.attempts, plural(row.attempts)))
		p.detail(strings.Join(parts, ", "))
	}
	n := len(totals.rows)
	text := fmt.Sprintf("run: %d unit%s, %s–%s (%s)", n, plural(n), clock(totals.wall), clock(epoch(time.Now())), agentio.Duration(time.Since(totals.start).Seconds()))
	if totals.cost != nil {
		text += fmt.Sprintf(", $%.2f", *totals.cost)
	}
	if totals.unverified > 0 {
		text += fmt.Sprintf(", %d unverified", totals.unverified)
	}
	if totals.replans > 0 {
		text += fmt.Sprintf(", %d replan%s", totals.replans, plural(totals.replans))
	}
	if totals.switches > 0 || totals.replans > 0 {
		word := "switches"
		if totals.switches == 1 {
			word = "switch"
		}
		text += fmt.Sprintf(", %d %s", totals.switches, word)
	}
	p.summary(text, totals.namedQuotas(), totals.quotaStart)
}

func feedback(problem string) string {
	return "Runner: " + problem + "\nFix it and finish with the result JSON.\nFix source issues; do not disable hooks, signing or checks, or change Git configuration to bypass a failure.\n"
}

func previousExecutorAttempt(root string, route agents.Route, agent map[string]any, refusal *agentio.Refusal, run agentio.AgentRun) (string, error) {
	diff, err := gitx.Git(root, "diff", "--stat", "HEAD")
	if err != nil {
		return "", err
	}
	status, err := gitx.Git(root, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	var tree []string
	if text := strings.TrimRight(string(diff), "\r\n"); text != "" {
		tree = append(tree, strings.Split(text, "\n")...)
	}
	for _, line := range strings.Split(strings.TrimRight(string(status), "\r\n"), "\n") {
		if strings.HasPrefix(line, "?? ") {
			tree = append(tree, line[3:])
		}
	}
	if len(tree) == 0 {
		tree = []string{"(no tracked diff or untracked paths)"}
	}
	if len(tree) > 40 {
		more := len(tree) - 39
		tree = append(tree[:39], fmt.Sprintf("… %d more", more))
	}

	var messages []string
	for _, message := range agentio.AssistantMessages(agent, run.Stdout) {
		refusalText := strings.TrimSpace(refusal.Text)
		if refusalText != "" && (strings.Contains(refusalText, message) || strings.Contains(message, refusalText)) {
			continue
		}
		messages = append(messages, message)
	}
	if len(messages) > 3 {
		messages = messages[len(messages)-3:]
	}

	var prompt strings.Builder
	fmt.Fprintf(&prompt, "\n## Previous attempt\n\nThe previous executor %s stopped after a provider %s refusal. The working tree may contain uncommitted work; continue it.\n\n### Working tree\n\n%s\n", routeName(route), refusal.Kind, strings.Join(tree, "\n"))
	if len(messages) > 0 {
		prompt.WriteString("\n### Last messages\n\n")
		for _, message := range messages {
			fmt.Fprintf(&prompt, "- %s\n", agentio.Clip(message, 600))
		}
	}
	return prompt.String(), nil
}

func runUnit(ctx context.Context, root string, args Args, config map[string]any, chain *executorChain, uid string, p *Progress, totals *runTotals) (code int, trigger string, runErr error) {
	plan, unit, err := cycle.FindUnit(root, uid)
	if err != nil {
		return 2, "", err
	}
	start := time.Now()
	var cost *float64
	defer func() {
		fields := map[string]any{"status": eventStatus(code, runErr), "seconds": time.Since(start).Seconds()}
		if cost != nil {
			fields["cost"] = *cost
		}
		totals.events.emit("unit_end", uid, fields)
	}()
	route, ok := chain.pick(time.Now())
	for !ok {
		until, found := chain.earliest()
		if !found {
			return 2, "", fmt.Errorf("%s: executor chain has no available route or reset time", uid)
		}
		line := fmt.Sprintf("%s: all executors unavailable until %s", uid, until.Local().Format("15:04"))
		if args.NoWait {
			totals.stop = line
			p.line(line+"; the unit stays [~]", true, "")
			return 7, "", nil
		}
		if err := waitForExecutorChain(ctx, p, line, until); err != nil {
			return 7, "", err
		}
		route, ok = chain.pick(time.Now())
	}
	initialRoute, _ := chain.firstRoute()
	if chain.enabled && routeIdentity(route) != routeIdentity(initialRoute) {
		totals.switches++
	}
	name := route.Agent
	agent := profile(config, name)
	totals.events.emit("unit_start", uid, map[string]any{"route": eventRoute(route)})
	p.unit(uid, unit.Title, route)
	defer func() { p.elapsed(uid, time.Since(start).Seconds()) }()
	committing, err := cycle.CommitPending(root, plan, unit)
	if err != nil {
		return 2, "", err
	}
	problem := ""
	if committing {
		p.line(uid+": resuming pending commit", false, "")
		var result cycle.Result
		for _, step := range []string{"verify", "commit"} {
			result = cycle.Self(root, step, uid)
			if step == "verify" {
				totals.events.emit("verify", uid, map[string]any{"exit_code": result.Code})
			} else if result.Code == 0 {
				totals.events.emit("commit", uid, map[string]any{"sha": firstWord(result.Stdout)})
			}
			if result.Code != 0 {
				if result.Code != 4 {
					writeFailure(p, result)
					totals.stop = uid + ": pending commit recovery failed"
					return result.Code, "", nil
				}
				problem = "docflow " + step + " failed:\n" + result.Stdout + result.Stderr
				break
			}
		}
		if problem == "" {
			p.result(uid, "done", firstWord(result.Stdout)+" (recovered commit)")
			totals.rows = append(totals.rows, unitRow{id: uid})
			return 0, "", nil
		}
	} else {
		result := cycle.Self(root, "prompt", uid)
		if result.Code != 0 {
			p.output(result.Stderr)
			trigger := ""
			if result.Code == 2 {
				p.line(uid+": the plan no longer matches the tree; replan the unit, then run again", true, "")
				totals.stop = uid + ": the plan no longer matches the tree; replan the unit"
				trigger = strings.SplitN(strings.TrimSpace(result.Stderr), "\n", 2)[0]
			}
			return result.Code, trigger, nil
		}
		plan, unit, err = cycle.FindUnit(root, uid)
		if err != nil {
			return 2, "", err
		}
	}
	mode := format.PlanPermissions(plan)
	prompt, err := runnerPrompt(root, plan, unit, agent, mode)
	if err != nil {
		return 2, "", err
	}
	if committing {
		prompt += "\n## Pending commit\nThe unit implementation is complete; repair only the failed check or hook. Do not redo the unit or modify repositories already committed by this unit.\n"
	}
	logs, err := runDir(root)
	if err != nil {
		return 2, "", err
	}
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return 2, "", err
	}
	schema := filepath.Join(logs, "schema.json")
	data, err := json.MarshalIndent(agents.ResultSchema, "", "  ")
	if err != nil {
		return 2, "", err
	}
	if err := os.WriteFile(schema, append(data, '\n'), 0o644); err != nil {
		return 2, "", err
	}
	heads, err := cycle.Heads(root, lint.ProjectRepos)
	if err != nil {
		return 2, "", err
	}
	index := 0
	for i, u := range plan.Units {
		if u.ID == uid {
			index = i + 1
		}
	}
	verify := "-"
	if field := unit.Field("Verify"); field != nil {
		verify = field.Value
	}
	p.detail(fmt.Sprintf("unit %d/%d of the plan, Verify: %s", index, len(plan.Units), verify))
	totals.mu.Lock()
	totals.activity = ""
	totals.mu.Unlock()
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
	session, last := "", "agent"
	var unitIn, unitOut *int
	timeout := agentCallTimeout(plan, agent)
	for attempt := 1; attempt <= args.Attempts; attempt++ {
		text, resume := prompt, ""
		if problem != "" {
			if session != "" {
				text, resume = feedback(problem), session
			} else {
				text = prompt + "\n## Previous attempt\n\n" + feedback(problem)
			}
		}
		var run agentio.AgentRun
		for limits, throttles := 0, 0; ; {
			argv, stdin, err := agents.BuildCommand(agent, text, route.Model, schema, resume, route.Effort, mode)
			if err != nil {
				return 2, "", err
			}
			log, err := nextRunCallLog(logs, uid, false)
			if err != nil {
				return 2, "", err
			}
			rel, _ := filepath.Rel(root, log)
			p.detail(fmt.Sprintf("attempt %d, log %s", attempt, rel))
			totals.events.emit("attempt", uid, map[string]any{"n": attempt, "route": eventRoute(route), "log": rel})
			p.spin(func() string { return uid + " " + agentio.Duration(time.Since(start).Seconds()) }, func() string { totals.mu.Lock(); defer totals.mu.Unlock(); return totals.activity })
			run, err = agentio.CallAgent(root, argv, stdin, log, agentio.CallOptions{Timeout: timeout, OnStreamEvent: onEvent, Context: ctx, Env: append(os.Environ(), agentio.RunnerEnv+"="+uid)})
			p.stop()
			if err != nil {
				return 7, "", err
			}
			output := agentio.AgentOutput(agent, run.Stdout)
			found, _ := agentio.JSONPath(output, stringValue(agent["session"])).(string)
			if agent["stream"] == "codex" {
				thread, _ := agentio.JSONPath(output, "json:thread_id").(string)
				totals.mergeQuota(name, agentio.CodexQuota(thread))
			}
			// The pi stream carries no quota; a zai route is re-probed after
			// every call, so the unit delta and mid-run exhaustion show up.
			if quotaName(route) == zaiQuota {
				totals.mergeQuota(zaiQuota, agentio.ProbeZaiQuota(agentio.QuotaProbeTimeout))
			}
			if found != "" {
				session = found
			}
			spent, tokens := agentio.RunCost(agent, run), agentio.RunTokens(agent, run)
			outTokens, turns := agentio.RunOutputTokens(agent, run), agentio.RunTurns(agent, run)
			unitIn, unitOut = addInt(unitIn, tokens), addInt(unitOut, outTokens)
			shown := tokenText(tokens, outTokens)
			if shown != "" {
				shown = "tokens " + shown
			}
			if turns != nil {
				if shown != "" {
					shown += ", "
				}
				shown += fmt.Sprintf("%d turns", *turns)
			}
			if shown != "" {
				p.detail(shown)
			}
			if err := agentio.RecordCall(root, uid, found, tokens, spent); err != nil {
				return 2, "", err
			}
			cost, totals.cost = agentio.Add(cost, spent), agentio.Add(totals.cost, spent)
			moved, err := cycle.HeadMoved(root, heads, plan, uid, lint.ProjectRepos)
			if err != nil {
				return 2, "", err
			}
			if moved != nil && moved.Kind == "stop" {
				totals.stop = uid + ": " + moved.Reason
				p.line(totals.stop, true, "")
				return 5, "", nil
			}
			if moved != nil {
				p.line(fmt.Sprintf("%s: HEAD moved by %d commit(s): %s", uid, len(moved.Commits), strings.Join(moved.Commits, "; ")), false, "yellow")
				heads, err = cycle.Heads(root, lint.ProjectRepos)
				if err != nil {
					return 2, "", err
				}
			}
			_, current, err := cycle.FindUnit(root, uid)
			if err != nil {
				return 2, "", err
			}
			want := "~"
			if committing {
				want = "x"
			}
			if current.Status != want {
				totals.stop = fmt.Sprintf("%s: the agent changed the unit to [%s]", uid, current.Status)
				p.line(totals.stop, true, "")
				return 5, "", nil
			}
			refusal := agentio.Classify(agent, run, time.Now())
			if refusal == nil {
				break
			}
			if refusal.Kind == agentio.RefusalLimit && refusal.Reset == nil && quotaName(route) == zaiQuota {
				refusal.Reset = zaiRefusalReset()
			}
			if chain.enabled && (refusal.Kind == agentio.RefusalLimit || refusal.Kind == agentio.RefusalOutage) {
				now := time.Now()
				until, err := chain.mark(route, refusal, now)
				if err != nil {
					return 2, "", err
				}
				next, available := chain.pick(now)
				if available && routeIdentity(next) != routeIdentity(route) {
					previousRoute := route
					totals.events.emit("route_switch", uid, map[string]any{"from": eventRoute(route), "to": eventRoute(next), "kind": refusal.Kind})
					p.line(fmt.Sprintf("%s: %s: %s until %s; switching to %s", uid, routeName(route), refusal.Kind, until.Local().Format("15:04"), routeName(next)), false, "")
					p.executor(next)
					route = next
					totals.switches++
					name, agent = route.Agent, profile(config, route.Agent)
					timeout = agentCallTimeout(plan, agent)
					previous, err := previousExecutorAttempt(root, previousRoute, profile(config, previousRoute.Agent), refusal, run)
					if err != nil {
						return 2, "", err
					}
					prompt, err = runnerPrompt(root, plan, unit, agent, mode)
					if err != nil {
						return 2, "", err
					}
					prompt += previous
					session = ""
					text, resume = prompt, ""
					continue
				}
				if !available {
					for !available {
						earliest, found := chain.earliest()
						if !found {
							return 2, "", fmt.Errorf("%s: executor chain is exhausted without a reset time", uid)
						}
						line := fmt.Sprintf("%s: all executors unavailable until %s", uid, earliest.Local().Format("15:04"))
						if args.NoWait || limits+1 >= agentio.LimitTries {
							totals.stop = line
							if !args.NoWait {
								totals.stop += fmt.Sprintf(" (%d in a row)", limits+1)
							}
							p.line(totals.stop+"; the unit stays [~]", true, "")
							return 7, "", nil
						}
						limits++
						if err := waitForExecutorChain(ctx, p, line, earliest); err != nil {
							return 7, "", err
						}
						next, available = chain.pick(time.Now())
					}
					if routeIdentity(next) != routeIdentity(route) {
						previousRoute := route
						totals.events.emit("route_switch", uid, map[string]any{"from": eventRoute(route), "to": eventRoute(next), "kind": refusal.Kind})
						p.line(uid+": switching to "+routeName(next)+" after the provider refusal wait", false, "")
						p.executor(next)
						route = next
						totals.switches++
						name, agent = route.Agent, profile(config, route.Agent)
						timeout = agentCallTimeout(plan, agent)
						previous, err := previousExecutorAttempt(root, previousRoute, profile(config, previousRoute.Agent), refusal, run)
						if err != nil {
							return 2, "", err
						}
						prompt, err = runnerPrompt(root, plan, unit, agent, mode)
						if err != nil {
							return 2, "", err
						}
						prompt += previous
						session = ""
						text, resume = prompt, ""
					} else if session != "" {
						text, resume = agentio.RefusalPrompt, session
					}
					continue
				}
			}
			limits++
			refusal.Kind, throttles = retryRefusalKind(refusal.Kind, throttles)
			if args.NoWait || limits >= agentio.LimitTries {
				why := fmt.Sprintf("%d in a row", limits)
				if args.NoWait {
					why = "--no-wait"
				}
				totals.stop = uid + ": " + refusalLabel(refusal.Kind) + " (" + why + ")"
				p.line(totals.stop+"; the unit stays [~]", true, "")
				return 7, "", nil
			}
			if err := waitRefusal(ctx, agent, refusal, limitProgress{p}); err != nil {
				return 7, "", err
			}
			if session != "" {
				text, resume = agentio.RefusalPrompt, session
			}
		}
		if auth := agentio.AuthReason(agent, run); auth != "" {
			totals.stop = uid + ": " + name + " is not logged in"
			p.line(totals.stop+": "+auth+"; log in to "+name+" and run again; the unit stays [~]", true, "")
			return 7, "", nil
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
			last = "agent"
			if result == nil {
				if reason := agentio.FailureReason(agent, run); reason != "" {
					problem += ": " + reason
				}
			}
			p.line(fmt.Sprintf("%s: attempt %d: %s", uid, attempt, problem), true, "")
			continue
		}
		if result["status"] == "blocked" {
			reason := stringValue(result["reason"])
			if committing {
				totals.stop = uid + ": pending commit needs action: " + reason
				p.line(totals.stop, false, "red")
				return 3, "", nil
			}
			needs := "human"
			if value, ok := result["blocker"].(string); ok {
				needs = value
			}
			res := cycle.Self(root, "block", uid, "--needs="+needs, "--reason="+reason)
			if res.Code != 0 {
				writeFailure(p, res)
				return res.Code, "", nil
			}
			totals.stop = uid + ": blocked (needs " + needs + "): " + reason
			p.line(totals.stop, false, "red")
			if needs == "human" {
				p.line(unblockHint(uid), false, "")
			}
			trigger := ""
			if needs == "plan" {
				trigger = totals.stop
			}
			return 3, trigger, nil
		}
		var res cycle.Result
		verified, err := cycle.VerifiedCurrent(root, uid, lint.ProjectRepos)
		if err != nil {
			return 2, "", err
		}
		if verified {
			totals.events.emit("verify", uid, map[string]any{"exit_code": 0, "reused": true})
			p.line(uid+": verify reused (tree unchanged since the agent's verify)", false, "")
		} else {
			p.spin(uid+" verify", nil)
			res = cycle.Self(root, "verify", uid)
			p.stop()
			totals.events.emit("verify", uid, map[string]any{"exit_code": res.Code})
			if res.Code == 4 {
				last, problem = "verify", "docflow verify failed:\n"+strings.TrimSpace(res.Stdout)
				kind := "verify"
				for _, k := range []string{"preflight", "gate"} {
					if strings.Contains(res.Stdout, ": "+k+" failed (exit") {
						kind = k
						break
					}
				}
				p.line(fmt.Sprintf("%s: attempt %d: %s failed", uid, attempt, kind), true, "")
				continue
			}
			if res.Code != 0 {
				writeFailure(p, res)
				return res.Code, "", nil
			}
		}
		step := []string{"done", uid}
		unverified, repeated := dropInheritedUnverified(plan, unit, resultStrings(result["unverified"]))
		if repeated > 0 {
			p.detail(fmt.Sprintf("unverified: %d repeated from Depends dropped", repeated))
		}
		for _, h := range resultStrings(result["handoff"]) {
			step = append(step, "--handoff="+h)
		}
		for _, u := range unverified {
			step = append(step, "--handoff="+unverifiedLabel+u)
		}
		for _, d := range resultStrings(result["decisions"]) {
			step = append(step, "--decision="+d)
		}
		if !committing {
			res = cycle.Self(root, step...)
			if res.Code != 0 {
				writeFailure(p, res)
				return res.Code, "", nil
			}
			committing = true
		}
		res = cycle.Self(root, "commit", uid)
		if res.Code == 4 {
			heads, err = cycle.Heads(root, lint.ProjectRepos)
			if err != nil {
				return 2, "", err
			}
			last, problem = "verify", "docflow commit hook failed:\n"+res.Stdout+res.Stderr
			p.line(fmt.Sprintf("%s: attempt %d: commit hook failed", uid, attempt), true, "")
			continue
		}
		if res.Code != 0 {
			writeFailure(p, res)
			return res.Code, "", nil
		}
		seconds := time.Since(start).Seconds()
		totals.events.emit("commit", uid, map[string]any{"sha": firstWord(res.Stdout)})
		summary := fmt.Sprintf("%d attempt%s", attempt, plural(attempt))
		if !p.fancy {
			summary += ", " + agentio.Summary(seconds, cost)
		} else if cost != nil {
			summary += fmt.Sprintf(", $%.2f", *cost)
		}
		if routeIdentity(route) != routeIdentity(initialRoute) {
			summary += " via " + routeName(route)
		}
		p.result(uid, "done", fmt.Sprintf("%s (%s)", firstWord(res.Stdout), summary))
		for _, u := range unverified {
			p.unverified(u)
		}
		totals.unverified += len(unverified)
		totals.rows = append(totals.rows, unitRow{uid, seconds, cost, unitIn, unitOut, attempt})
		if args.Verbose {
			if err := unitDetail(root, p, totals); err != nil {
				return 2, "", err
			}
		}
		return 0, "", nil
	}
	state := "stays [~]"
	if committing {
		state = "has a pending commit"
	}
	totals.stop = fmt.Sprintf("%s: gave up after %d attempts", uid, args.Attempts)
	p.line(totals.stop+"; the unit "+state, true, "")
	if last == "verify" {
		return 4, "", nil
	}
	return 7, "", nil
}

func unitQuotas(p *Progress, totals *runTotals, names []string, before map[string]agentio.Quota) {
	if len(names) == 0 {
		return
	}
	quotas := make([]NamedQuota, 0, len(names))
	for _, name := range names {
		quotas = append(quotas, NamedQuota{Name: name, Quota: totals.quota[name]})
	}
	p.quotas(quotas, before, "", true)
}

func unitQuota(p *Progress, totals *runTotals, name string, before agentio.Quota) {
	unitQuotas(p, totals, []string{name}, map[string]agentio.Quota{name: before})
}

func unitDetail(root string, p *Progress, totals *runTotals) error {
	plan, err := cycle.FindPlan(root, strings.SplitN(totals.rows[len(totals.rows)-1].id, ".", 2)[0])
	if err != nil {
		return err
	}
	left := 0
	for _, u := range plan.Units {
		if !doneStatus(u.Status) {
			left++
		}
	}
	if left > 0 {
		seconds := 0.0
		for _, r := range totals.rows {
			seconds += r.seconds
		}
		p.detail(fmt.Sprintf("eta ~%s for %d unit%s", agentio.Duration(seconds/float64(len(totals.rows))*float64(left)), left, plural(left)))
	}
	return nil
}

func dryRun(root string, args Args, config map[string]any, sel cycle.Selection, stdout, stderr io.Writer) (int, error) {
	if sel.Kind == "close" {
		fmt.Fprintf(stdout, "plan: %s %s (ready to close)\npermissions: %s\n", sel.Plan.Meta["id"], sel.Plan.Meta["title"], format.PlanPermissions(sel.Plan))
		return 0, closeRouteLine(args, config, sel.Plan, stdout)
	}
	if args.All {
		code, err := dryRunAll(args, config, sel.Plan, stdout, stderr)
		if code == 0 && err == nil {
			err = closeRouteLine(args, config, sel.Plan, stdout)
		}
		return code, err
	}
	if sel.Kind == "blocked" {
		fmt.Fprintln(stderr, sel.Unit.ID+" is blocked")
		return 3, nil
	}
	if sel.Kind == "nothing" {
		fmt.Fprintln(stderr, "nothing to do")
		return 1, nil
	}
	unit := sel.Unit
	route, err := agents.RunRoute(config, sel.Plan, unit, args.RunArgs)
	if err != nil {
		return 2, err
	}
	mode := format.PlanPermissions(sel.Plan)
	prompt, err := runnerPrompt(root, sel.Plan, unit, profile(config, route.Agent), mode)
	if err != nil {
		return 2, err
	}
	logs, err := runDir(root)
	if err != nil {
		return 2, err
	}
	argv, stdin, err := agents.BuildCommand(profile(config, route.Agent), prompt, route.Model, filepath.Join(logs, "schema.json"), "", route.Effort, mode)
	if err != nil {
		return 2, err
	}
	marker := fmt.Sprintf("<prompt: %d bytes>", len(prompt))
	fmt.Fprintf(stdout, "unit: %s %s\nagent: %s\nmodel: %s\neffort: %s\nfrom: %s\npermissions: %s\n", unit.ID, unit.Title, route.Agent, orDash(route.Model), orDash(route.Effort), agents.RouteSource(sel.Plan, unit), mode)
	printPrerequisites(sel.Plan, stdout)
	for i, a := range argv {
		argv[i] = shellQuote(strings.ReplaceAll(a, prompt, marker))
	}
	fmt.Fprintln(stdout, "argv: "+strings.Join(argv, " "))
	text := "none"
	if stdin != nil {
		text = "prompt " + marker
	}
	fmt.Fprintln(stdout, "stdin: "+text)
	return 0, closeRouteLine(args, config, sel.Plan, stdout)
}

func dryRunAll(args Args, config map[string]any, plan *format.Plan, stdout, stderr io.Writer) (int, error) {
	units := []*format.Unit{}
	if plan != nil {
		for _, u := range plan.Units {
			if !doneStatus(u.Status) {
				units = append(units, u)
			}
		}
	}
	if len(units) == 0 {
		fmt.Fprintln(stderr, "nothing to do")
		return 1, nil
	}
	if err := agents.CheckExecutors(plan); err != nil {
		return 2, err
	}
	chains, err := chainLines(plan)
	if err != nil {
		return 2, runError("run: " + err.Error())
	}
	fmt.Fprintf(stdout, "plan: %s %s\npermissions: %s\n", plan.Meta["id"], plan.Meta["title"], format.PlanPermissions(plan))
	printPrerequisites(plan, stdout)
	for _, unit := range units {
		route, err := agents.RunRoute(config, plan, unit, args.RunArgs)
		if err != nil {
			return 2, err
		}
		fmt.Fprintf(stdout, "%s [%s] %s: %s (%s)\n", unit.ID, unit.Status, unit.Title, agents.RouteText(route), agents.RouteSource(plan, unit))
	}
	for _, line := range chains {
		fmt.Fprintln(stdout, line)
	}
	return 0, nil
}

// chainLines renders the fallback, role, and replan chains of the header,
// in that order, as dry-run lines; absent keys produce no line.
func chainLines(plan *format.Plan) ([]string, error) {
	keys := append([]string{"fallback"}, append(lint.RoleKeys(), "replan")...)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		line, err := chainLine(plan, key)
		if err != nil {
			return nil, err
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func chainLine(plan *format.Plan, key string) (string, error) {
	value, present := plan.Meta[key]
	if !present {
		return "", nil
	}
	if !lint.ExecutorMode(plan) {
		return "", fmt.Errorf("%s needs executor mode", key)
	}
	routes, err := agents.ChainRoutes(value)
	if err != nil {
		return "", fmt.Errorf("%s: %s", key, err)
	}
	if len(routes) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(routes))
	for _, route := range routes {
		model, effort := route.Model, route.Effort
		if model == "" {
			model = "-"
		}
		if effort == "" {
			effort = "-"
		}
		parts = append(parts, strings.Join([]string{route.Agent, model, effort}, " "))
	}
	return key + ": " + strings.Join(parts, "; "), nil
}

type limitProgress struct{ p *Progress }

func (l limitProgress) Line(s string, stderr bool) { l.p.line(s, stderr, "") }
func (l limitProgress) Spin(s string)              { l.p.spin(s, nil) }
func (l limitProgress) Stop()                      { l.p.stop() }
func runError(s string) error                      { return clierrors.DocflowError{Message: s, Code: 2} }
func doneStatus(s string) bool                     { return s == "x" || s == "-" }
func profile(config map[string]any, name string) map[string]any {
	return config["agents"].(map[string]any)[name].(map[string]any)
}
func stringValue(v any) string  { s, _ := v.(string); return s }
func epoch(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return "?"
}
func writeFailure(p *Progress, r cycle.Result) { p.output(r.Stdout + r.Stderr) }
func oneLine(s string) string                  { return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ") }
func addInt(a, b *int) *int {
	if b == nil {
		return a
	}
	n := *b
	if a != nil {
		n += *a
	}
	return &n
}

// unverifiedPunct is the trailing punctuation an `Unverified:` item may end
// with: a period, the colon before the missing check, the dash of
// "… — missing check".
const unverifiedPunct = " .,;:!?\u2014-"

// inheritedUnitID matches a lower-cased unit id inside a parenthetical: it
// names the unit the item was copied from.
var inheritedUnitID = regexp.MustCompile(`p\d+(\.\d+)+`)

// isInheritanceTail reports whether the text inside a trailing parenthetical
// marks the item as copied from another unit: the English "inherited" or a
// unit id, which is also how "унаследовано из P001.1.1" marks it.
func isInheritanceTail(text string) bool {
	return strings.HasPrefix(text, "inherited") || inheritedUnitID.MatchString(text)
}

// unverifiedKey normalises an `Unverified:` item text so a copy of a
// dependency's item matches the item of its own unit: case, spacing and the
// trailing punctuation go, with them the inheritance tail an agent writes —
// "(inherited from P001.1.1)", "(унаследовано из P001.1.1)". Only a trailing
// parenthetical that marks inheritance is cut: one naming the item's own
// detail ("layout on iPhone (small)") stays in the key on both sides, so two
// such items stay distinct.
func unverifiedKey(text string) string {
	key := strings.TrimRight(strings.ToLower(oneLine(text)), unverifiedPunct)
	if end := len(key) - 1; end > 0 && key[end] == ')' {
		if open := matchingParen(key, end); open > 0 && isInheritanceTail(key[open+1:end]) {
			key = strings.TrimRight(strings.TrimSpace(key[:open]), unverifiedPunct)
		}
	}
	return key
}

// matchingParen returns the index of the "(" whose ")" sits at the end of the
// key, or -1 when the tail is not a whole parenthetical: an agent's own
// parenthesis inside the tail ("(inherited from P001.1.1 (draft))") then
// belongs to the tail's text, the whole tail is the inheritance marker.
func matchingParen(key string, end int) int {
	for depth, i := 0, end; i >= 0; i-- {
		switch key[i] {
		case ')':
			depth++
		case '(':
			if depth--; depth == 0 {
				if i > 0 && key[i-1] == ' ' {
					return i
				}
				return -1
			}
		}
	}
	return -1
}

// dropInheritedUnverified keeps the result JSON's unverified items that are
// not copies of an item already open in the Handoff of a direct Depends unit,
// and counts the copies it drops. An agent inherits its dependency's open item
// and the copy stays open unit after unit; the unit that changed the code owns
// its own risks. The dependency Handoff is plan text, read through the parsed
// plan the way `prompt` renders `## Handoff from Depends`.
func dropInheritedUnverified(plan *format.Plan, unit *format.Unit, values []string) (kept []string, dropped int) {
	inherited := map[string]bool{}
	depends, _ := format.UnitDepends(plan, unit)
	for _, id := range depends {
		dep := plan.Unit(id)
		if dep == nil {
			continue
		}
		field := dep.Field("Handoff")
		if field == nil {
			continue
		}
		for _, item := range field.Items {
			text, ok := strings.CutPrefix(item.Text, agents.Unverified)
			if !ok {
				continue
			}
			if key := unverifiedKey(text); key != "" {
				inherited[key] = true
			}
		}
	}
	kept = []string{}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		text := oneLine(value)
		if inherited[unverifiedKey(text)] {
			dropped++
			continue
		}
		kept = append(kept, text)
	}
	return kept, dropped
}

func resultStrings(v any) []string {
	result := []string{}
	if values, ok := v.([]any); ok {
		for _, v := range values {
			result = append(result, stringValue(v))
		}
	}
	return result
}
func tokenText(in, out *int) string {
	parts := []string{}
	if in != nil {
		parts = append(parts, count(*in)+" in")
	}
	if out != nil {
		parts = append(parts, count(*out)+" out")
	}
	return strings.Join(parts, " / ")
}

var shellSafe = regexp.MustCompile(`^[\w@%+=:,./-]+$`)

func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
