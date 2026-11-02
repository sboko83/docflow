// Command tui-live drives the two live terminal acceptance scenarios of P052
// (Pi through opencode-go and Codex) and writes a receipt outside the working
// tree. --check validates that receipt against the current checkout and
// binary without starting any agent.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"bsv-docflow/internal/runner"
)

const (
	receiptVersion   = 1
	receiptFile      = "docflow/tui-live/receipt.json"
	defaultMaxCalls  = 4
	defaultCallLimit = 300 * time.Second
	defaultMaxAge    = 7 * 24 * time.Hour
)

type scenarioSpec struct {
	Name   string
	Agent  string
	Model  string
	Effort string
}

var liveScenarios = []scenarioSpec{
	{Name: "opencode-go", Agent: "pi", Model: "opencode-go/deepseek-v4.1-flash", Effort: "low"},
	{Name: "codex", Agent: "codex", Model: "gpt-6-luna", Effort: "high"},
}

func (s scenarioSpec) Route() string {
	return s.Agent + " | " + s.Model + " | " + s.Effort
}

type receipt struct {
	Version   int               `json:"version"`
	CreatedAt time.Time         `json:"created_at"`
	OK        bool              `json:"ok"`
	Error     string            `json:"error,omitempty"`
	Host      hostInfo          `json:"host"`
	Scenarios []scenarioReceipt `json:"scenarios"`
}

type hostInfo struct {
	OS         string            `json:"os"`
	Arch       string            `json:"arch"`
	Checkout   string            `json:"checkout"`
	Head       string            `json:"head"`
	Binary     string            `json:"binary"`
	BinaryHash string            `json:"binary_sha256"`
	CLIs       map[string]string `json:"clis"`
}

type scenarioReceipt struct {
	Name        string      `json:"name"`
	Route       string      `json:"route"`
	Attempts    int         `json:"attempts"`
	Replans     int         `json:"replans"`
	Agent       string      `json:"agent"`
	Model       string      `json:"model"`
	Effort      string      `json:"effort"`
	Project     string      `json:"project"`
	StartedAt   time.Time   `json:"started_at"`
	EndedAt     time.Time   `json:"ended_at"`
	Calls       []call      `json:"calls"`
	Assignments []string    `json:"assignments"`
	Transitions []string    `json:"transitions"`
	ToolCall    bool        `json:"tool_call"`
	ToolCalls   []string    `json:"tool_calls"`
	Pause       pauseProof  `json:"pause"`
	Logs        []string    `json:"logs"`
	Independent independent `json:"independent_check"`
	TreeClean   bool        `json:"tree_clean"`
}

type call struct {
	Agent           string  `json:"agent"`
	Model           string  `json:"model"`
	Effort          string  `json:"effort"`
	Attempt         int     `json:"attempt"`
	StartedAt       string  `json:"started_at"`
	DurationSeconds float64 `json:"duration_seconds"`
	Log             string  `json:"log"`
}

type processInfo struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
}

type pauseProof struct {
	Requested         bool          `json:"requested"`
	CheckpointWritten bool          `json:"checkpoint_written"`
	Stage             string        `json:"stage"`
	AgentProcesses    []processInfo `json:"agent_processes"`
	LiveProcesses     int           `json:"live_processes_after_pause"`
	LockReleased      bool          `json:"lock_released"`
}

type independent struct {
	Command string `json:"command"`
	Exit    int    `json:"exit_code"`
	Output  string `json:"output"`
	OK      bool   `json:"ok"`
}

type options struct {
	checkout     string
	binary       string
	attempts     int
	replans      int
	maxCalls     int
	callLimit    time.Duration
	maxAge       time.Duration
	toolTimeout  time.Duration
	startTimeout time.Duration
	scenarios    string
	keep         bool
}

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "--run":
		err = runCommand(os.Args[2:], os.Stdout)
	case "--check":
		err = checkCommand(os.Args[2:], os.Stdout)
	default:
		usage(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tui-live:", err)
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: tools/tui-live.sh --run [--checkout DIR] [--binary PATH] [--attempts N] [--replans N] [--max-calls N] [--scenario name]")
	fmt.Fprintln(w, "       tools/tui-live.sh --check [--checkout DIR] [--binary PATH] [--attempts N] [--replans N]")
}

func runFlags(args []string) (*options, error) {
	flags := flag.NewFlagSet("--run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	opts := &options{attempts: 1, replans: 0, maxCalls: defaultMaxCalls, callLimit: defaultCallLimit, maxAge: defaultMaxAge, toolTimeout: 90 * time.Second, startTimeout: 30 * time.Second}
	flags.StringVar(&opts.checkout, "checkout", "", "checkout under test (default: current module)")
	flags.StringVar(&opts.binary, "binary", "", "docflow binary (default: build bin/docflow)")
	flags.IntVar(&opts.attempts, "attempts", 1, "attempts per unit passed to docflow tui")
	flags.IntVar(&opts.replans, "replans", 0, "auto-replans per phase passed to docflow tui")
	flags.IntVar(&opts.maxCalls, "max-calls", defaultsMaxCalls(), "live calls allowed per scenario")
	flags.DurationVar(&opts.callLimit, "call-timeout", defaultCallLimit, "maximum duration of one live call")
	flags.DurationVar(&opts.maxAge, "max-age", defaultMaxAge, "maximum receipt age for --check")
	flags.DurationVar(&opts.toolTimeout, "tool-timeout", 90*time.Second, "wait for a live tool call")
	flags.DurationVar(&opts.startTimeout, "start-timeout", 30*time.Second, "wait for the workspace and start dialog")
	flags.BoolVar(&opts.keep, "keep", false, "keep temporary projects for inspection")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, errors.New("unexpected arguments")
	}
	if opts.attempts < 1 {
		return nil, errors.New("--attempts must be at least 1")
	}
	if opts.replans < 0 {
		return nil, errors.New("--replans must not be negative")
	}
	if opts.maxCalls < 1 || opts.callLimit <= 0 || opts.maxAge <= 0 || opts.toolTimeout <= 0 || opts.startTimeout <= 0 {
		return nil, errors.New("limits must be positive")
	}
	return opts, nil
}

func defaultsMaxCalls() int { return defaultMaxCalls }

func checkFlags(args []string) (*options, error) {
	flags := flag.NewFlagSet("--check", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	opts := &options{attempts: 1, replans: 0, maxAge: defaultMaxAge}
	flags.StringVar(&opts.checkout, "checkout", "", "checkout whose receipt is checked")
	flags.StringVar(&opts.binary, "binary", "", "docflow binary to match")
	flags.IntVar(&opts.attempts, "attempts", 1, "attempts per unit recorded by the receipt")
	flags.IntVar(&opts.replans, "replans", 0, "auto-replans per phase recorded by the receipt")
	flags.DurationVar(&opts.maxAge, "max-age", defaultMaxAge, "maximum receipt age")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, errors.New("unexpected arguments")
	}
	return opts, nil
}

func runCommand(args []string, out io.Writer) error {
	opts, err := runFlags(args)
	if err != nil {
		return err
	}
	root, gitDir, head, err := checkoutState(opts.checkout)
	if err != nil {
		return err
	}
	bin := opts.binary
	if bin == "" {
		bin = filepath.Join(root, "bin", "docflow")
		if err := buildBinary(root, bin); err != nil {
			return err
		}
	}
	bin, err = filepath.Abs(bin)
	if err != nil {
		return err
	}
	binHash, err := fileHash(bin)
	if err != nil {
		return err
	}
	receiptPath := filepath.Join(gitDir, filepath.FromSlash(receiptFile))
	_ = os.Remove(receiptPath)

	r := receipt{
		Version:   receiptVersion,
		CreatedAt: time.Now().UTC(),
		OK:        true,
		Host: hostInfo{
			OS: runtimeOS(), Arch: runtimeArch(), Checkout: root, Head: head,
			Binary: bin, BinaryHash: binHash, CLIs: cliVersions(),
		},
	}
	for _, spec := range liveScenarios {
		scenario, err := runScenario(opts, spec, root, bin)
		if err != nil {
			r.OK = false
			r.Error = spec.Name + ": " + err.Error()
			r.Scenarios = append(r.Scenarios, scenario)
			writeReceipt(receiptPath, r)
			return fmt.Errorf("%s: %w", spec.Name, err)
		}
		r.Scenarios = append(r.Scenarios, scenario)
		if len(scenario.Calls) > opts.maxCalls {
			r.OK = false
			r.Error = fmt.Sprintf("%s: %d live calls exceed the limit of %d", spec.Name, len(scenario.Calls), opts.maxCalls)
			writeReceipt(receiptPath, r)
			return errors.New(r.Error)
		}
		fmt.Fprintf(out, "tui-live: %s: ok (%d calls)\n", spec.Name, len(scenario.Calls))
	}
	if err := validateReceipt(r, head, binHash, opts.attempts, opts.replans, opts.maxCalls, opts.callLimit, opts.maxAge, false); err != nil {
		r.OK = false
		r.Error = err.Error()
		writeReceipt(receiptPath, r)
		return err
	}
	if err := writeReceipt(receiptPath, r); err != nil {
		return err
	}
	fmt.Fprintf(out, "tui-live: receipt %s\n", receiptPath)
	return nil
}

func checkCommand(args []string, out io.Writer) error {
	opts, err := checkFlags(args)
	if err != nil {
		return err
	}
	root, gitDir, head, err := checkoutState(opts.checkout)
	if err != nil {
		return err
	}
	bin := opts.binary
	if bin == "" {
		bin = filepath.Join(root, "bin", "docflow")
	}
	bin, err = filepath.Abs(bin)
	if err != nil {
		return err
	}
	binHash, err := fileHash(bin)
	if err != nil {
		return fmt.Errorf("checked binary: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(receiptFile)))
	if err != nil {
		return fmt.Errorf("no live acceptance receipt: %w", err)
	}
	var r receipt
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("receipt is not JSON: %w", err)
	}
	if err := validateReceipt(r, head, binHash, opts.attempts, opts.replans, defaultMaxCalls, defaultCallLimit, opts.maxAge, true); err != nil {
		return err
	}
	fmt.Fprintf(out, "tui-live: ok: HEAD %s, binary %s, %d scenarios\n", head, binHash, len(r.Scenarios))
	return nil
}

func validateReceipt(r receipt, head, binHash string, attempts, replans, maxCalls int, callLimit, maxAge time.Duration, fresh bool) error {
	if r.Version != receiptVersion {
		return fmt.Errorf("receipt version %d, want %d", r.Version, receiptVersion)
	}
	if !r.OK {
		return fmt.Errorf("receipt records a failed run: %s", r.Error)
	}
	if fresh && time.Since(r.CreatedAt) > maxAge {
		return fmt.Errorf("receipt is older than %s", maxAge)
	}
	if r.Host.Head != head {
		return fmt.Errorf("receipt HEAD %s does not match checkout HEAD %s", short(r.Host.Head), short(head))
	}
	if r.Host.BinaryHash != binHash {
		return fmt.Errorf("receipt binary %s does not match checked binary %s", short(r.Host.BinaryHash), short(binHash))
	}
	if r.Host.OS == "" || r.Host.Arch == "" || len(r.Host.CLIs) == 0 {
		return errors.New("receipt does not name the host or CLI versions")
	}
	if r.Host.OS != runtimeOS() || r.Host.Arch != runtimeArch() {
		return fmt.Errorf("receipt host %s/%s does not match %s/%s", r.Host.OS, r.Host.Arch, runtimeOS(), runtimeArch())
	}
	if len(r.Scenarios) != len(liveScenarios) {
		return fmt.Errorf("receipt has %d scenarios, want %d", len(r.Scenarios), len(liveScenarios))
	}
	required := []string{
		"start_accepted", "tool_call_seen", "pause_checkpoint", "agent_stopped",
		"resumed", "unit1_committed", "stopped_after_unit1", "unit2_not_started",
		"exited", "reopened", "unit2_committed", "independent_check_passed", "tree_clean",
	}
	for i, spec := range liveScenarios {
		s := r.Scenarios[i]
		if s.Name != spec.Name || s.Agent != spec.Agent || s.Model != spec.Model || s.Effort != spec.Effort {
			return fmt.Errorf("scenario %d is %s/%s/%s, want %s", i+1, s.Agent, s.Model, s.Effort, spec.Route())
		}
		if s.Route != spec.Route() {
			return fmt.Errorf("%s: route %q, want %q", spec.Name, s.Route, spec.Route())
		}
		if s.Attempts != attempts || s.Replans != replans {
			return fmt.Errorf("%s: receipt limits %d attempts/%d replans, want %d/%d", spec.Name, s.Attempts, s.Replans, attempts, replans)
		}
		if len(s.Calls) == 0 {
			return fmt.Errorf("%s: no live calls", spec.Name)
		}
		if len(s.Calls) > maxCalls {
			return fmt.Errorf("%s: %d live calls exceed the limit of %d", spec.Name, len(s.Calls), maxCalls)
		}
		for _, c := range s.Calls {
			if c.Agent != spec.Agent || c.Model != spec.Model || c.Effort != spec.Effort {
				return fmt.Errorf("%s: call used %s/%s/%s, want %s", spec.Name, c.Agent, c.Model, c.Effort, spec.Route())
			}
			if c.DurationSeconds > callLimit.Seconds() {
				return fmt.Errorf("%s: call %d took %.0fs, limit %s", spec.Name, c.Attempt, c.DurationSeconds, callLimit)
			}
		}
		if !s.ToolCall || len(s.ToolCalls) == 0 {
			return fmt.Errorf("%s: no tool call was observed", spec.Name)
		}
		p := s.Pause
		if !p.Requested || !p.CheckpointWritten || p.Stage != "agent" || len(p.AgentProcesses) == 0 || p.LiveProcesses != 0 || !p.LockReleased {
			return fmt.Errorf("%s: pause is not proven (checkpoint=%v stage=%q processes=%d live=%d lock=%v)", spec.Name, p.CheckpointWritten, p.Stage, len(p.AgentProcesses), p.LiveProcesses, p.LockReleased)
		}
		if !s.Independent.OK {
			return fmt.Errorf("%s: independent check failed: %s", spec.Name, s.Independent.Output)
		}
		if !s.TreeClean {
			return fmt.Errorf("%s: temporary project tree is not clean", spec.Name)
		}
		if len(s.Logs) == 0 {
			return fmt.Errorf("%s: receipt lists no logs", spec.Name)
		}
		for _, path := range s.Logs {
			full := path
			if !filepath.IsAbs(full) {
				full = filepath.Join(s.Project, filepath.FromSlash(path))
			}
			if _, err := os.Stat(full); err != nil {
				return fmt.Errorf("%s: log %s: %w", spec.Name, path, err)
			}
		}
		have := map[string]bool{}
		for _, name := range s.Transitions {
			have[name] = true
		}
		for _, name := range required {
			if !have[name] {
				return fmt.Errorf("%s: missing confirmed transition %q", spec.Name, name)
			}
		}
	}
	return nil
}

func writeReceipt(path string, r receipt) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func checkoutState(explicit string) (root, gitDir, head string, err error) {
	root = explicit
	if root == "" {
		root, err = repoRoot()
		if err != nil {
			return "", "", "", err
		}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", "", "", err
	}
	dir, err := gitOutput(root, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", "", "", err
	}
	head, err = gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		return "", "", "", err
	}
	return root, strings.TrimSpace(dir), strings.TrimSpace(head), nil
}

func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module bsv-docflow") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find the bsv-docflow checkout above %s", wd)
		}
	}
}

func buildBinary(root, bin string) error {
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/docflow")
	cmd.Dir = root
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("build docflow: %w\n%s", err, out)
	}
	return nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func cliVersions() map[string]string {
	versions := map[string]string{}
	for _, name := range []string{"pi", "codex"} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, name, "--version")
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			versions[name] = "unavailable: " + oneLine(string(out)) + " " + oneLine(err.Error())
			continue
		}
		versions[name] = strings.TrimSpace(oneLine(string(out)))
	}
	return versions
}

// runScenario drives one live route in a real PTY. bin is the resolved
// binary under test: an explicitly passed one or the freshly built default.
func runScenario(opts *options, spec scenarioSpec, checkout, bin string) (rec scenarioReceipt, err error) {
	rec = scenarioReceipt{
		Name: spec.Name, Route: spec.Route(), Attempts: opts.attempts, Replans: opts.replans,
		Agent: spec.Agent, Model: spec.Model, Effort: spec.Effort,
		StartedAt: time.Now().UTC(),
	}
	transition := func(name string) { rec.Transitions = append(rec.Transitions, name) }
	project, err := createProject(checkout, spec)
	if err != nil {
		return rec, err
	}
	rec.Project = project
	if !opts.keep {
		defer os.RemoveAll(project)
	}
	env := terminalEnv()
	tuiArgs := []string{"--attempts", strconv.Itoa(opts.attempts), "--replans", strconv.Itoa(opts.replans)}
	term, err := startTerminal(bin, project, env, tuiArgs...)
	if err != nil {
		return rec, err
	}
	defer term.Close()
	var budgetHit atomic.Bool
	stopBudget := make(chan struct{})
	defer close(stopBudget)
	defer func() {
		if budgetHit.Load() {
			if err == nil {
				err = fmt.Errorf("%s: live call budget exceeded (%d calls, %s per call)", spec.Name, opts.maxCalls, opts.callLimit)
			} else {
				err = fmt.Errorf("%s: live call budget exceeded: %w", spec.Name, err)
			}
		}
	}()
	go monitorBudget(project, opts.maxCalls, opts.callLimit, term, &budgetHit, stopBudget)
	if err := term.WaitFor("Queue", opts.startTimeout); err != nil {
		return rec, fmt.Errorf("workspace did not load: %w", err)
	}
	// Expand the plan and open the first unit's details before starting.
	term.Send("\x1b[B")
	term.Send("\x1b[B")
	term.Send("\x1b[B")
	term.Send("\r")
	time.Sleep(150 * time.Millisecond)
	term.Send("s")
	if err := term.WaitFor("Start the current Queue run?", opts.startTimeout); err != nil {
		return rec, fmt.Errorf("start dialog: %w", err)
	}
	limitLine := runner.RunLimitsLine(opts.attempts, opts.replans)
	if err := term.WaitFor(limitLine, opts.startTimeout); err != nil {
		return rec, fmt.Errorf("start dialog does not show the run limits: %w", err)
	}
	if err := term.WaitFor(spec.Route(), opts.startTimeout); err != nil {
		return rec, fmt.Errorf("start dialog does not show the assignment: %w", err)
	}
	rec.Assignments = append(rec.Assignments, spec.Route())
	term.Send("\r")
	transition("start_accepted")

	tool, logPath, err := waitToolCall(project, spec.Agent, opts.toolTimeout)
	if err != nil {
		return rec, err
	}
	rec.ToolCall, rec.ToolCalls = true, append(rec.ToolCalls, tool)
	transition("tool_call_seen")

	procs, err := waitAgentProcesses(term.PID(), spec.Agent, 15*time.Second)
	if err != nil {
		return rec, err
	}
	rec.Pause.Requested = true
	rec.Pause.AgentProcesses = procs
	term.Send("p")
	cp, err := waitCheckpoint(project, func(c checkpoint) bool { return c.Stage == "agent" }, opts.startTimeout)
	if err != nil {
		return rec, fmt.Errorf("pause checkpoint: %w", err)
	}
	rec.Pause.CheckpointWritten, rec.Pause.Stage = true, cp.Stage
	transition("pause_checkpoint")
	if err := waitAgentGone(term.PID(), spec.Agent, 20*time.Second); err != nil {
		return rec, err
	}
	rec.Pause.LiveProcesses = 0
	if err := waitNoFile(filepath.Join(project, ".git", "docflow", "run", "run.lock"), 20*time.Second); err != nil {
		return rec, err
	}
	rec.Pause.LockReleased = true
	transition("agent_stopped")

	// Resume the paused call, then ask the runner to stop after that unit.
	term.Send("R")
	stopped := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		term.Send("S")
		time.Sleep(100 * time.Millisecond)
		if strings.Contains(term.Output(), "stopping after the current unit") {
			stopped = true
			break
		}
	}
	if !stopped {
		return rec, errors.New("Stop after unit was not accepted")
	}
	transition("resumed")
	if err := waitPlanStatus(project, "P001.1.1", "x", 2*time.Minute); err != nil {
		return rec, err
	}
	if err := waitNoFile(filepath.Join(project, ".git", "docflow", "run", "run.lock"), 2*time.Minute); err != nil {
		return rec, err
	}
	if !gitSubjectContains(project, "P001.1.1: write a") {
		return rec, errors.New("first unit has no commit")
	}
	firstRuns, _ := eventFiles(project)
	if eventsMentionUnitStart(firstRuns, "P001.1.2") {
		return rec, errors.New("the second unit started after Stop after unit")
	}
	transition("unit1_committed")
	transition("stopped_after_unit1")
	transition("unit2_not_started")
	if err := quitTerminal(term); err != nil {
		return rec, err
	}
	transition("exited")

	// Reopen the same project and finish the second unit.
	term2, err := startTerminal(bin, project, env, tuiArgs...)
	if err != nil {
		return rec, err
	}
	reopened := false
	deadline = time.Now().Add(opts.startTimeout)
	for time.Now().Before(deadline) {
		screen := term2.Output()
		if strings.Contains(screen, "Queue") || strings.Contains(screen, "Recover the saved run") {
			reopened = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !reopened {
		term2.Close()
		return rec, errors.New("reopened workspace did not load")
	}
	transition("reopened")
	term2.Send("s")
	if err := term2.WaitFor("Start the current Queue run?", opts.startTimeout); err != nil {
		term2.Close()
		return rec, fmt.Errorf("second start dialog: %w", err)
	}
	if err := term2.WaitFor(limitLine, opts.startTimeout); err != nil {
		term2.Close()
		return rec, fmt.Errorf("second start dialog does not show the run limits: %w", err)
	}
	term2.Send("\r")
	if err := waitPlanStatus(project, "P001.1.2", "x", 3*time.Minute); err != nil {
		term2.Close()
		return rec, err
	}
	if err := waitNoFile(filepath.Join(project, ".git", "docflow", "run", "run.lock"), 2*time.Minute); err != nil {
		term2.Close()
		return rec, err
	}
	if !gitSubjectContains(project, "P001.1.2: write b") {
		term2.Close()
		return rec, errors.New("second unit has no commit")
	}
	transition("unit2_committed")
	if err := quitTerminal(term2); err != nil {
		term2.Close()
		return rec, err
	}
	term2.Close()

	rec.Independent = independentCheck(project)
	if !rec.Independent.OK {
		return rec, fmt.Errorf("independent check failed: %s", rec.Independent.Output)
	}
	transition("independent_check_passed")
	rec.TreeClean = gitClean(project)
	if !rec.TreeClean {
		clean, _ := gitOutput(project, "status", "--porcelain")
		return rec, fmt.Errorf("temporary project is dirty:\n%s", clean)
	}
	transition("tree_clean")
	rec.Calls, rec.Logs, err = collectCalls(project, spec)
	if err != nil {
		return rec, err
	}
	if len(rec.Calls) == 0 {
		return rec, errors.New("no agent calls in the run journals")
	}
	rec.Logs, err = archiveLogs(checkout, spec, project, rec.Logs)
	if err != nil {
		return rec, err
	}
	_ = logPath
	rec.EndedAt = time.Now().UTC()
	return rec, nil
}

// monitorBudget stops a live run as soon as the journal shows more calls than
// allowed or one call runs past its time limit. The receipt validation repeats
// the same check, so a monitor that misses the last write still fails --run.
func monitorBudget(project string, maxCalls int, callLimit time.Duration, term *terminal, hit *atomic.Bool, stop <-chan struct{}) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			calls, active := journalBudget(project)
			if calls > maxCalls || active > callLimit {
				hit.Store(true)
				term.Send("x")
				return
			}
		}
	}
}

// journalBudget returns the number of attempt events and the age of calls
// that have not produced their closing stream event.
func journalBudget(project string) (int, time.Duration) {
	files, _ := eventFiles(project)
	calls := 0
	now := float64(time.Now().UnixNano()) / 1e9
	var active float64
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		open := map[string]float64{}
		for _, line := range strings.Split(string(data), "\n") {
			var event map[string]any
			if json.Unmarshal([]byte(line), &event) != nil {
				continue
			}
			typ := str(event["event"])
			unit := str(event["unit"])
			key := str(event["run"]) + "/" + unit
			if typ == "attempt" {
				calls++
				if ts, ok := number(event["ts"]); ok {
					open[key] = ts
				}
			}
			if typ == "stream" || typ == "state" {
				delete(open, key)
			}
		}
		for _, ts := range open {
			if age := now - ts; age > active {
				active = age
			}
		}
	}
	return calls, time.Duration(active * float64(time.Second))
}

// archiveLogs copies every stream and verify log named by the journals into
// the checkout git directory, so the receipt stays auditable after the
// temporary project is removed.
func archiveLogs(checkout string, spec scenarioSpec, project string, paths []string) ([]string, error) {
	_, gitDir, _, err := checkoutState(checkout)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(gitDir, "docflow", "tui-live", "logs", spec.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for _, path := range paths {
		src := path
		if !filepath.IsAbs(src) {
			src = filepath.Join(project, filepath.FromSlash(src))
		}
		if seen[src] {
			continue
		}
		seen[src] = true
		data, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("archive %s: %w", path, err)
		}
		dst := filepath.Join(dir, filepath.Base(src))
		for n := 2; ; n++ {
			if _, err := os.Stat(dst); os.IsNotExist(err) {
				break
			}
			dst = filepath.Join(dir, fmt.Sprintf("%s.%d", filepath.Base(src), n))
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return nil, err
		}
		out = append(out, dst)
	}
	sort.Strings(out)
	return out, nil
}

// quitTerminal leaves the workspace, answering the live-run exit dialog if the
// runner has not published Finished yet. Enter outside a dialog is harmless.
func quitTerminal(term *terminal) error {
	term.Send("q")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(term.Output(), "A run is still active") {
			term.Send("\r")
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return term.WaitExit(20 * time.Second)
}

func createProject(checkout string, spec scenarioSpec) (string, error) {
	dir, err := os.MkdirTemp("", "docflow-tui-live-")
	if err != nil {
		return "", err
	}
	absDir, _ := filepath.Abs(dir)
	rel, err := filepath.Rel(checkout, absDir)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		os.RemoveAll(dir)
		return "", errors.New("temporary project is inside the checkout")
	}
	files := map[string]string{
		"go.mod":                  "module liveaccept\n\ngo 1.20\n",
		"README.md":               "Temporary live acceptance project.\n",
		"live_test.go":            independentTest,
		"docs/docflow.md":         "---\ndocflow: 3\n---\n## Queue\n- P001\n\n## Frozen\n\n## Backlog\n",
		"docs/plans/P001-live.md": planText(spec),
	}
	for path, text := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.name", "tui-live"}, {"config", "user.email", "tui-live@localhost"},
		{"config", "commit.gpgsign", "false"}, {"add", "-A"}, {"commit", "-qm", "init"},
	} {
		if _, err := gitOutput(dir, args...); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}
	return dir, nil
}

const independentTest = `package liveaccept

import (
	"os"
	"strings"
	"testing"
)

func TestFiles(t *testing.T) {
	for name, want := range map[string]string{"a.txt": "a", "b.txt": "b"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// The unit asks for a single line; one trailing line break still leaves
		// one line, and every agent CLI terminates the file it writes. A second
		// line, empty or not, is a different answer.
		got := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
		if got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}
`

func planText(spec scenarioSpec) string {
	return `---
id: P001
title: Live terminal acceptance
status: active
created: 2026-11-02
executor: ` + spec.Route() + `
permissions: auto
---
## Goal

Write two files through the terminal runner and verify them independently.

## Constraints

Keep the working tree clean after each unit.

## Context

Temporary project created by tools/tui-live.

## Traps

A pause must stop the agent process before Paused is shown.

## Decisions

Deterministic files over generated code — the independent test reads them — rejected generated output.

## Phase 1: Files

### [ ] P001.1.1 Write a
Read:
- docs/docflow.md
- README.md
Do:
- Create a.txt containing the single line a.
Verify: ` + "`test -f a.txt`" + `
Commit: P001.1.1: write a

### [ ] P001.1.2 Write b
Read:
- docs/docflow.md
- README.md
Do:
- Create b.txt containing the single line b.
Verify: ` + "`test -f b.txt`" + `
Commit: P001.1.2: write b
`
}

func terminalEnv() []string {
	env := os.Environ()
	drop := map[string]bool{"DOCFLOW_RUNNER_UNIT": true, "DOCFLOW_AGENTS": true}
	out := make([]string, 0, len(env)+8)
	for _, item := range env {
		if key, _, ok := strings.Cut(item, "="); ok && drop[key] {
			continue
		}
		out = append(out, item)
	}
	return append(out,
		"TERM=xterm-256color", "NO_COLOR=1", "DOCFLOW_NOTIFY=0", "DOCFLOW_SKILLS_CHECK=0",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
	)
}

type checkpoint struct {
	Stage  string `json:"stage"`
	Unit   string `json:"unit"`
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

func waitCheckpoint(project string, ok func(checkpoint) bool, timeout time.Duration) (checkpoint, error) {
	path := filepath.Join(project, ".git", "docflow", "tui", "checkpoint.json")
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			var cp checkpoint
			if err := json.Unmarshal(data, &cp); err == nil && ok(cp) {
				return cp, nil
			} else if err == nil && !ok(cp) {
				last = fmt.Errorf("stage %q", cp.Stage)
			}
		} else {
			last = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return checkpoint{}, fmt.Errorf("checkpoint not reached: %v", last)
}

func waitPlanStatus(project, unit, status string, timeout time.Duration) error {
	path := filepath.Join(project, "docs", "plans", "P001-live.md")
	want := "### [" + status + "] " + unit
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), want) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	return fmt.Errorf("%s did not reach [%s]:\n%s", unit, status, data)
}

func waitNoFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("%s still exists", path)
}

func waitToolCall(project, agent string, timeout time.Duration) (string, string, error) {
	runDir := filepath.Join(project, ".git", "docflow", "run")
	markers := []string{"tool_execution_start"}
	switch agent {
	case "codex":
		markers = []string{`"item.started"`, `"command_execution"`}
	case "opencode":
		markers = []string{`"type":"tool_use"`, `"type": "tool_use"`}
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		files, _ := filepath.Glob(filepath.Join(runDir, "*.out"))
		for _, path := range files {
			data, err := readHead(path, 1<<20)
			if err != nil {
				continue
			}
			text := string(data)
			hit := true
			for _, marker := range markers {
				if !strings.Contains(text, marker) {
					hit = false
					break
				}
			}
			if hit {
				return describeTool(text, agent), path, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", "", fmt.Errorf("no tool call within %s", timeout)
}

func describeTool(text, agent string) string {
	line := ""
	switch agent {
	case "codex":
		line = `"command_execution"`
	default:
		line = `"tool_execution_start"`
	}
	for _, candidate := range strings.Split(text, "\n") {
		if strings.Contains(candidate, line) {
			return oneLine(candidate)
		}
	}
	return agent + " tool call"
}

func readHead(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

func waitAgentProcesses(rootPID int, agent string, timeout time.Duration) ([]processInfo, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if procs := agentProcesses(rootPID, agent); len(procs) > 0 {
			return procs, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, fmt.Errorf("no live %s process under the terminal", agent)
}

func waitAgentGone(rootPID int, agent string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(agentProcesses(rootPID, agent)) == 0 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("the %s process is still running after Pause", agent)
}

func independentCheck(project string) independent {
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	exit := 0
	if err != nil {
		exit = 1
		if code, ok := err.(*exec.ExitError); ok {
			exit = code.ExitCode()
		}
	}
	return independent{Command: "go test ./...", Exit: exit, Output: strings.TrimSpace(string(out)), OK: exit == 0}
}

func collectCalls(project string, spec scenarioSpec) ([]call, []string, error) {
	files, err := eventFiles(project)
	if err != nil {
		return nil, nil, err
	}
	type pending struct {
		index int
	}
	seen := map[string]pending{}
	var calls []call
	logs := map[string]bool{}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			var event map[string]any
			if json.Unmarshal(scanner.Bytes(), &event) != nil {
				continue
			}
			typ, _ := event["event"].(string)
			unit, _ := event["unit"].(string)
			runID, _ := event["run"].(string)
			key := runID + "/" + unit
			ts, _ := number(event["ts"])
			switch typ {
			case "attempt":
				route, _ := event["route"].(map[string]any)
				entry := call{
					Agent: str(route["agent"]), Model: str(route["model"]), Effort: str(route["effort"]),
					Attempt: int(value(event["n"])), StartedAt: epochText(ts),
					Log: str(event["log"]),
				}
				seen[key] = pending{index: len(calls)}
				calls = append(calls, entry)
				if entry.Log != "" {
					logs[entry.Log] = true
				}
			case "stream", "state", "run_end":
				if p, ok := seen[key]; ok && calls[p.index].DurationSeconds == 0 {
					calls[p.index].DurationSeconds = mathMax(0, ts-calls[p.index].startSeconds())
				}
				if log := str(event["log"]); log != "" {
					logs[log] = true
				}
				for _, side := range []string{"stdout", "stderr"} {
					if ref, ok := event[side].(map[string]any); ok {
						if p := str(ref["path"]); p != "" {
							logs[p] = true
						}
					}
				}
			case "verify":
				for _, side := range []string{"stdout", "stderr"} {
					if ref, ok := event[side].(map[string]any); ok {
						if p := str(ref["path"]); p != "" {
							logs[p] = true
						}
					}
				}
			}
			if log := str(event["log"]); log != "" {
				logs[log] = true
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, nil, err
		}
	}
	for i := range calls {
		if calls[i].DurationSeconds == 0 {
			calls[i].DurationSeconds = 0
		}
	}
	list := make([]string, 0, len(logs))
	for path := range logs {
		list = append(list, path)
	}
	sort.Strings(list)
	return calls, list, nil
}

func (c call) startSeconds() float64 {
	t, err := time.Parse(time.RFC3339Nano, c.StartedAt)
	if err != nil {
		return 0
	}
	return float64(t.UnixNano()) / 1e9
}

func eventFiles(project string) ([]string, error) {
	dir := filepath.Join(project, ".git", "docflow", "run")
	files, err := filepath.Glob(filepath.Join(dir, "*.events.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func eventsMentionUnitStart(files []string, unit string) bool {
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			var event map[string]any
			if json.Unmarshal([]byte(line), &event) != nil {
				continue
			}
			if str(event["event"]) == "unit_start" && str(event["unit"]) == unit {
				return true
			}
		}
	}
	return false
}

func gitSubjectContains(dir, subject string) bool {
	out, err := gitOutput(dir, "log", "--format=%s")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == subject {
			return true
		}
	}
	return false
}

func gitClean(dir string) bool {
	out, err := gitOutput(dir, "status", "--porcelain")
	return err == nil && strings.TrimSpace(out) == ""
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

func epochText(ts float64) string {
	sec := int64(ts)
	nsec := int64((ts - float64(sec)) * 1e9)
	return time.Unix(sec, nsec).UTC().Format(time.RFC3339Nano)
}

func number(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	}
	return 0, false
}

func value(value any) float64 { n, _ := number(value); return n }

func str(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func mathMax(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func runtimeOS() string   { return runtime.GOOS }
func runtimeArch() string { return runtime.GOARCH }
