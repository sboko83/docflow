//go:build darwin || linux

package tui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/runner"
	"bsv-docflow/internal/testshim"
)

const (
	keyEnter = "\r"
	keyEsc   = "\x1b"
	keyEnd   = "\x1b[F"
	keyDown  = "\x1b[B"
	keyUp    = "\x1b[A"
)

// acceptanceCLI is the binary built once for the terminal acceptance
// scenarios. Every scenario drives this binary in a real PTY with fake agent
// CLIs that speak the codex, pi and claude stream formats.
var (
	acceptanceCLI      string
	acceptanceBuildErr error
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "docflow-acceptance-*")
	if err != nil {
		acceptanceBuildErr = err
	} else {
		acceptanceCLI = filepath.Join(dir, "docflow")
		build := exec.Command("go", "build", "-o", acceptanceCLI, "./cmd/docflow")
		build.Dir = acceptanceRoot()
		build.Env = os.Environ()
		if out, err := build.CombinedOutput(); err != nil {
			acceptanceBuildErr = fmt.Errorf("build CLI: %w\n%s", err, out)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func acceptanceRoot() string {
	root := os.Getenv("DOCFLOW_ACCEPTANCE_ROOT")
	if root != "" {
		return root
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return filepath.Dir(filepath.Dir(wd))
}

func acceptanceBinary(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("acceptance builds the CLI")
	}
	if acceptanceBuildErr != nil {
		t.Fatal(acceptanceBuildErr)
	}
	if acceptanceCLI == "" {
		t.Fatal("acceptance CLI was not built")
	}
	return acceptanceCLI
}

// acceptPlan describes one plan of the temporary project.
type acceptPlan struct {
	ID     string
	Title  string
	Status string
	Meta   map[string]string
	Verify string
	Units  []acceptUnit
}

type acceptUnit struct {
	ID     string
	Title  string
	Do     []string
	Verify string
	Commit string
	Role   string
	Status string
}

// acceptProject is one temporary checkout with fake agent CLIs.
type acceptProject struct {
	t        *testing.T
	Root     string
	Bin      string
	Scenario string
	Home     string
	Env      []string
}

func newAcceptProject(t *testing.T, plans []acceptPlan) *acceptProject {
	t.Helper()
	p := &acceptProject{t: t, Root: t.TempDir(), Home: t.TempDir()}
	write := func(rel, text string) {
		t.Helper()
		full := filepath.Join(p.Root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var queue, backlog strings.Builder
	for _, plan := range plans {
		queue.WriteString("- " + plan.ID + "\n")
		status := plan.Status
		if status == "" {
			status = "active"
		}
		var planText strings.Builder
		fmt.Fprintf(&planText, "---\nid: %s\ntitle: %s\nstatus: %s\ncreated: 2026-11-01\n", plan.ID, plan.Title, status)
		for key, value := range plan.Meta {
			fmt.Fprintf(&planText, "%s: %s\n", key, value)
		}
		planText.WriteString("---\n\n## Goal\n\nTerminal acceptance goal.\n\n## Phase 1: Work\n\n")
		for _, unit := range plan.Units {
			unitStatus := unit.Status
			if unitStatus == "" {
				unitStatus = " "
			}
			fmt.Fprintf(&planText, "### [%s] %s %s\n", unitStatus, unit.ID, unit.Title)
			if unit.Role != "" {
				fmt.Fprintf(&planText, "Role: %s\n", unit.Role)
			}
			planText.WriteString("Do:\n")
			for _, step := range unit.Do {
				planText.WriteString("- " + step + "\n")
			}
			verify := unit.Verify
			if verify == "" {
				verify = plan.Verify
			}
			if verify == "" {
				verify = "true"
			}
			commit := unit.Commit
			if commit == "" {
				commit = unit.ID + ": work"
			}
			fmt.Fprintf(&planText, "Verify: `%s`\nCommit: %s\n\n", verify, commit)
		}
		write("docs/plans/"+plan.ID+"-accept.md", planText.String())
	}
	backlog.WriteString("\n## Frozen\n\n## Backlog\n")
	write("docs/docflow.md", "---\ndocflow: 3\n---\n## Queue\n"+queue.String()+backlog.String())
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.name", "t"}, {"config", "user.email", "t@localhost"},
		{"config", "commit.gpgsign", "false"}, {"add", "-A"}, {"commit", "-qm", "init"},
	} {
		command := exec.Command("git", args...)
		command.Dir = p.Root
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	p.Scenario = filepath.Join(t.TempDir(), "steps")
	if err := os.MkdirAll(p.Scenario, 0o755); err != nil {
		t.Fatal(err)
	}
	p.Bin = installFakeCLIs(t)
	baseEnv := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "DOCFLOW_RUNNER=") || strings.HasPrefix(entry, "DOCFLOW_RUNNER_UNIT=") {
			// The acceptance test itself may run under the docflow runner;
			// its child commands must not inherit the runner lock.
			continue
		}
		baseEnv = append(baseEnv, entry)
	}
	p.Env = append(baseEnv,
		"TERM=xterm-256color", "NO_COLOR=1",
		"PATH="+p.Bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_AGENT_SCENARIO="+p.Scenario,
		"DOCFLOW_NOTIFY=0", "DOCFLOW_SKILLS_CHECK=0",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"HOME="+p.Home,
		"XDG_CONFIG_HOME="+filepath.Join(p.Home, ".config"),
	)
	return p
}

// installFakeCLIs puts codex, pi and claude streams on PATH. A plan in
// executor mode always uses the built-in profiles, so the acceptance fake is
// the CLI itself, not a custom agent profile: the argv, model and effort
// arguments are the ones the product builds.
func installFakeCLIs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	fake, err := filepath.Abs("../agentio/testdata/fake_agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := `#!/bin/bash
if [ "$1" = "app-server" ]; then
  while IFS= read -r line; do
    case "$line" in
      *'"id":1'*) printf '%s\n' '{"id":1,"result":{}}' ;;
      *'"id":2'*) printf '%s\n' '{"id":2,"result":{}}'; exit 0 ;;
    esac
  done
  exit 0
fi
if [ "$1" = "-p" ] && [ "$2" = "/usage" ]; then exit 0; fi
exec ` + shellSingle(testshim.Bash(t)) + ` ` + shellSingle(filepath.ToSlash(fake)) + ` "$@"
`
	for _, name := range []string{"codex", "pi", "claude"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(wrapper), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// writeStep installs one call step of the shell fake agent. Every step is
// started at its call number, so a scenario can answer a refusal, a pause and
// a resumed call independently.
func (p *acceptProject) writeStep(n int, lines ...string) {
	p.t.Helper()
	if err := os.WriteFile(filepath.Join(p.Scenario, fmt.Sprintf("%d.sh", n)), []byte(strings.Join(lines, "")), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

func (p *acceptProject) calls() []map[string]any {
	p.t.Helper()
	data, err := os.ReadFile(p.Scenario + ".log")
	if err != nil {
		return nil
	}
	var calls []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var call map[string]any
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			p.t.Fatalf("fake call line %q: %v", line, err)
		}
		calls = append(calls, call)
	}
	return calls
}

// fakeCallLines reads the complete call records. A paused call can be killed
// mid-write, so an incomplete trailing line is skipped here; waitFakeCalls
// waits for the record before a test pauses that call.
func (p *acceptProject) fakeCallLines() []map[string]any {
	data, err := os.ReadFile(p.Scenario + ".log")
	if err != nil {
		return nil
	}
	var calls []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var call map[string]any
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			continue
		}
		calls = append(calls, call)
	}
	return calls
}

func (p *acceptProject) waitFakeCalls(n int, timeout time.Duration) {
	p.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(p.fakeCallLines()) >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	p.t.Fatalf("fake call %d was not recorded; log:\n%s", n, mustRead(p.Scenario+".log"))
}

func mustRead(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}

func (p *acceptProject) callCount() int {
	_, err := os.Stat(p.Scenario + ".n")
	if err != nil {
		return 0
	}
	n := 0
	data, err := os.ReadFile(p.Scenario + ".n")
	if err == nil {
		n, _ = strconv.Atoi(strings.TrimSpace(string(data)))
	}
	return n
}

func (p *acceptProject) path(rel string) string {
	return filepath.Join(p.Root, filepath.FromSlash(rel))
}

func (p *acceptProject) planText(id string) string {
	p.t.Helper()
	data, err := os.ReadFile(p.path("docs/plans/" + id + "-accept.md"))
	if err != nil {
		p.t.Fatalf("read plan %s: %v", id, err)
	}
	return string(data)
}

func (p *acceptProject) git(args ...string) string {
	p.t.Helper()
	command := exec.Command("git", append([]string{"-C", p.Root}, args...)...)
	out, err := command.CombinedOutput()
	if err != nil {
		p.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (p *acceptProject) clean() bool { return p.git("status", "--porcelain") == "" }

func shellText(lines ...string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, "printf '%s\\n' "+shellSingle(line)+"\n")
	}
	return out
}

func shellSingle(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}

func doneResult() string {
	return `{"status":"done","handoff":["work done"],"decisions":[],"unverified":[],"reason":"","blocker":""}`
}

func blockedResult() string {
	return `{"status":"blocked","blocker":"human","reason":"needs a person","handoff":[]}`
}

// codexStep returns shell lines for a codex-stream call. It can hold a tool
// open with a sleeping child process, which is the state a Pause must kill.
func codexStep(o codexStepOptions) []string {
	lines := []string{}
	if o.ChildSleep > 0 {
		lines = append(lines, "( sleep "+strconv.Itoa(o.ChildSleep)+"; printf late > late.txt ) &\n")
	}
	lines = append(lines, shellText(
		`{"type":"thread.started","thread_id":"`+o.Session+`"}`,
		`{"type":"turn.started"}`,
	)...)
	if o.Tool {
		lines = append(lines, shellText(
			`{"type":"item.started","item":{"id":"c1","type":"command_execution","command":"`+o.Command+`"}}`,
		)...)
	}
	if o.Partial != "" {
		lines = append(lines, "printf '%s' "+shellSingle(o.Partial)+"\n")
	}
	if o.WriteBeforeSleep != "" {
		lines = append(lines, "printf '%s' "+shellSingle(o.WriteBeforeSleep)+" > "+o.WritePath+"\n")
	}
	if o.Sleep > 0 {
		lines = append(lines, "sleep "+strconv.Itoa(o.Sleep)+"\n")
	}
	if o.Tool {
		lines = append(lines, shellText(
			`{"type":"item.completed","item":{"id":"c1","type":"command_execution","aggregated_output":"`+o.Result+`","exit_code":0}}`,
		)...)
	}
	if o.Stderr != "" {
		lines = append(lines, "printf '%s\\n' "+shellSingle(o.Stderr)+" >&2\n")
	}
	if o.WriteAfterSleep != "" {
		lines = append(lines, "printf '%s' "+shellSingle(o.WriteAfterSleep)+" > "+o.WritePath+"\n")
	}
	lines = append(lines, shellText(
		`{"type":"item.completed","item":{"type":"agent_message","text":`+strconv.Quote(doneResult())+`}}`,
		`{"type":"turn.completed","usage":{"input_tokens":`+strconv.Itoa(o.InputTokens)+`,"output_tokens":`+strconv.Itoa(o.OutputTokens)+`}}`,
	)...)
	return lines
}

type codexStepOptions struct {
	Session          string
	Command          string
	Result           string
	Tool             bool
	Sleep            int
	ChildSleep       int
	Partial          string
	WriteBeforeSleep string
	WriteAfterSleep  string
	WritePath        string
	Stderr           string
	InputTokens      int
	OutputTokens     int
}

func piStep(o piStepOptions) []string {
	lines := []string{}
	if o.ChildSleep > 0 {
		lines = append(lines, "( sleep "+strconv.Itoa(o.ChildSleep)+"; printf late > late.txt ) &\n")
	}
	lines = append(lines, shellText(`{"type":"session","id":"`+o.Session+`"}`, `{"type":"agent_start"}`, `{"type":"turn_start"}`)...)
	if o.Tool {
		lines = append(lines, shellText(`{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"`+o.Command+`"}}`)...)
	}
	if o.Sleep > 0 {
		lines = append(lines, "sleep "+strconv.Itoa(o.Sleep)+"\n")
	}
	if o.Tool {
		lines = append(lines, shellText(`{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","result":"`+o.Result+`","isError":false}`)...)
	}
	if o.Write != "" {
		lines = append(lines, "printf '%s' "+shellSingle(o.Write)+" > "+o.WritePath+"\n")
	}
	text := strconv.Quote(doneResult())
	lines = append(lines, shellText(
		`{"type":"message_end","message":{"role":"assistant","usage":{"input":`+strconv.Itoa(o.InputTokens)+`,"output":`+strconv.Itoa(o.OutputTokens)+`},"content":[{"type":"text","text":`+text+`}]}}`,
		`{"type":"agent_settled","aborted":false}`,
	)...)
	return lines
}

type piStepOptions struct {
	Session      string
	Command      string
	Result       string
	Tool         bool
	Sleep        int
	ChildSleep   int
	Write        string
	WritePath    string
	InputTokens  int
	OutputTokens int
}

func claudeStep(o claudeStepOptions) []string {
	lines := []string{}
	if o.Limit {
		lines = append(lines, shellText(
			`{"type":"system","subtype":"init","session_id":"`+o.Session+`"}`,
			`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":`+strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)+`}}`,
			`{"type":"result","is_error":true,"result":"usage limit reached","session_id":"`+o.Session+`"}`,
		)...)
		lines = append(lines, "exit 1\n")
		return lines
	}
	lines = append(lines, shellText(`{"type":"system","subtype":"init","session_id":"`+o.Session+`"}`)...)
	if o.Write != "" {
		lines = append(lines, "printf '%s' "+shellSingle(o.Write)+" > "+o.WritePath+"\n")
	}
	lines = append(lines, shellText(
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"working"}]}}`,
		`{"type":"result","is_error":false,"result":"ok","session_id":"`+o.Session+`","total_cost_usd":0.01,"usage":{"input_tokens":0,"output_tokens":0},"structured_output":`+doneResult()+`}`,
	)...)
	return lines
}

type claudeStepOptions struct {
	Session   string
	Limit     bool
	Write     string
	WritePath string
}

// acceptTUI is one interactive terminal running the built CLI.
type acceptTUI struct {
	t       *testing.T
	cmd     *exec.Cmd
	master  *os.File
	project *acceptProject
	term    *acceptTerminal

	mu      sync.Mutex
	raw     bytes.Buffer
	done    chan struct{}
	err     error
	readErr error
}

func startAcceptTUI(t *testing.T, p *acceptProject, width, height int, args ...string) *acceptTUI {
	t.Helper()
	master, slave := openTestPTY(t)
	if err := setTestWinsize(slave, width, height); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(acceptanceBinary(t), append([]string{"tui"}, args...)...)
	cmd.Dir = p.Root
	cmd.Env = append([]string{}, p.Env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The child holds its own copy of the slave. Linux reports EIO on the
	// master only after every slave descriptor is closed, so a copy kept here
	// would block the reader below after the TUI exits.
	_ = slave.Close()
	s := &acceptTUI{t: t, cmd: cmd, master: master, project: p, term: newAcceptTerminal(width, height), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				s.mu.Lock()
				s.raw.Write(buf[:n])
				s.term.feed(buf[:n])
				s.mu.Unlock()
			}
			if err != nil {
				if errors.Is(err, syscall.EAGAIN) {
					// Outside the runtime poller a non-blocking read
					// reports EAGAIN instead of blocking; wait for data
					// instead of treating it as EOF.
					time.Sleep(10 * time.Millisecond)
					continue
				}
				s.mu.Lock()
				s.readErr = err
				s.mu.Unlock()
				return
			}
		}
	}()
	return s
}

func (s *acceptTUI) output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.raw.String()
}

func (s *acceptTUI) screen() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.term.String()
}

func (s *acceptTUI) waitFor(text string, timeout time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(s.screen(), text) || strings.Contains(s.output(), text) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.mu.Lock()
	readErr := s.readErr
	s.mu.Unlock()
	s.t.Fatalf("terminal output does not contain %q (readErr=%v)\nscreen:\n%s\nraw:\n%s", text, readErr, s.screen(), s.output())
}

func (s *acceptTUI) waitForAfter(marker, text string, timeout time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		screen := s.screen()
		if !strings.Contains(screen, marker) && strings.Contains(screen, text) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.t.Fatalf("screen still contains %q or lost %q\n%s", marker, text, s.screen())
}

func (s *acceptTUI) send(key string) {
	s.t.Helper()
	if _, err := s.master.WriteString(key); err != nil {
		s.t.Fatal(err)
	}
	delay := 120 * time.Millisecond
	if key == keyEsc {
		// A lone ESC needs the decoder's sequence timeout before the next key
		// can be read as a key of its own.
		delay = 400 * time.Millisecond
	}
	time.Sleep(delay)
}

func (s *acceptTUI) resize(width, height int) {
	s.t.Helper()
	if err := setTestWinsize(s.master, width, height); err != nil {
		s.t.Fatal(err)
	}
	s.mu.Lock()
	s.term.resize(width, height)
	s.mu.Unlock()
	_ = s.cmd.Process.Signal(syscall.SIGWINCH)
}

func (s *acceptTUI) waitPath(path string, present bool, timeout time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := os.Stat(path)
		if present == (err == nil) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	var journal strings.Builder
	for _, file := range s.project.journalFiles() {
		data, _ := os.ReadFile(file)
		fmt.Fprintf(&journal, "\n--- %s ---\n%s", file, data)
	}
	for _, rel := range []string{".git/docflow/run/P001.1.1.1.out", ".git/docflow/run/P001.1.1.1.out.stderr"} {
		data, err := os.ReadFile(s.project.path(rel))
		fmt.Fprintf(&journal, "\n--- %s (err=%v) ---\n%s", rel, err, data)
	}
	s.t.Fatalf("file %s present=%v not reached\nplan:\n%s\njournal:%s\noutput:\n%s", path, present, s.project.planText("P001"), journal.String(), s.output())
}

func (s *acceptTUI) waitExit(timeout time.Duration) {
	s.t.Helper()
	exited := make(chan error, 1)
	go func() { exited <- s.cmd.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			s.t.Fatalf("tui exited with %v\n%s", err, s.output())
		}
	case <-time.After(timeout):
		_ = s.cmd.Process.Kill()
		s.t.Fatalf("tui did not exit\n%s", s.output())
	}
	s.assertTerminalRestored()
	_ = s.master.Close()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		// A blocking read on a PTY master is not interrupted by Close; a
		// descendant still holding the slave would hang the suite here.
		s.t.Fatal("terminal reader did not stop after the TUI exited")
	}
}

func (s *acceptTUI) assertTerminalRestored() {
	s.t.Helper()
	out := s.output()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		out = s.output()
		if strings.Contains(out, "\x1b[?1049l") && strings.Contains(out, "\x1b[?25h") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(out, "\x1b[?1049l") {
		s.t.Fatalf("alternate screen was not left\n%q", out)
	}
	if !strings.Contains(out, "\x1b[?25h") {
		s.t.Fatalf("cursor was not restored\n%q", out)
	}
	if !testEchoEnabled(s.t, s.master) {
		s.t.Fatal("terminal echo was not restored")
	}
}

func (s *acceptTUI) quit() {
	s.t.Helper()
	s.send("q")
	// A run that has not published its final state yet opens the exit dialog;
	// confirm stopping it so the test does not hang on a race with run_end.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.screen(), "A run is still active") {
			s.send(keyEnter)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.waitExit(20 * time.Second)
}

func (s *acceptTUI) selectUnit(id string) {
	s.t.Helper()
	// Esc returns from the details pane to the tree; it is a no-op there.
	s.send(keyEsc)
	s.send("/")
	s.send(id)
	s.send(keyEnter)
	s.send(keyEnd)
	s.send(keyEnter)
}

// waitFile waits until a path appears or disappears.
func waitFile(t *testing.T, path string, present bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := os.Stat(path)
		if present == (err == nil) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("file %s present=%v not reached", path, present)
}

func waitFileContains(t *testing.T, path, text string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), text) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("file %s never contained %q", path, text)
}

func (p *acceptProject) runDir() string {
	return p.path(".git/docflow/run")
}

func (p *acceptProject) journalFiles() []string {
	p.t.Helper()
	files, err := filepath.Glob(filepath.Join(p.runDir(), "*.events.jsonl"))
	if err != nil {
		p.t.Fatal(err)
	}
	return files
}

func readJournalLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			// A live journal can end in the middle of a line.
			continue
		}
		records = append(records, record)
	}
	return records
}

func (p *acceptProject) newestJournal() (string, []map[string]any) {
	p.t.Helper()
	return p.newestJournalExcept("")
}

// newestJournalExcept picks the newest journal by modification time. Run ids
// are second-based and a resumed run may add a "-N" suffix that sorts before
// the base name, so the file name alone is not the creation order.
func (p *acceptProject) newestJournalExcept(except string) (string, []map[string]any) {
	p.t.Helper()
	files := p.journalFiles()
	newest := ""
	var newestTime time.Time
	for _, file := range files {
		if file == except {
			continue
		}
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		if newest == "" || info.ModTime().After(newestTime) {
			newest, newestTime = file, info.ModTime()
		}
	}
	if newest == "" {
		return "", nil
	}
	return newest, readJournalLines(p.t, newest)
}

func (p *acceptProject) waitJournal(pred func(records []map[string]any) bool, timeout time.Duration) string {
	p.t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		if path, records := p.newestJournal(); path != "" {
			last = path
			if pred(records) {
				return path
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	path, records := p.newestJournal()
	p.t.Fatalf("journal condition not reached (last %s)\n%v", path, records)
	return last
}

func eventNames(records []map[string]any) []string {
	names := make([]string, 0, len(records))
	for _, record := range records {
		if name, ok := record["event"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

func hasEvent(records []map[string]any, name string) bool {
	for _, record := range records {
		if record["event"] == name {
			return true
		}
	}
	return false
}

func eventByType(records []map[string]any, name string) []map[string]any {
	var out []map[string]any
	for _, record := range records {
		if record["event"] == name {
			out = append(out, record)
		}
	}
	return out
}

func numberField(record map[string]any, key string) (int, bool) {
	value, ok := record[key].(float64)
	return int(value), ok
}

func (p *acceptProject) checkpoint() (runner.Checkpoint, bool) {
	p.t.Helper()
	cp, err := runner.ReadCheckpoint(p.Root)
	if err != nil {
		return runner.Checkpoint{}, false
	}
	return cp, true
}

func (p *acceptProject) waitCheckpointStage(stage string, timeout time.Duration) runner.Checkpoint {
	p.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cp, ok := p.checkpoint(); ok && cp.Stage == stage {
			return cp
		}
		time.Sleep(20 * time.Millisecond)
	}
	cp, ok := p.checkpoint()
	p.t.Fatalf("checkpoint stage %q not reached (checkpoint=%+v ok=%v)", stage, cp, ok)
	return runner.Checkpoint{}
}

func (p *acceptProject) waitPlanStatus(unit, status string, timeout time.Duration) {
	p.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(p.planText("P001"), "["+status+"] "+unit) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	p.t.Fatalf("%s never reached [%s]:\n%s", unit, status, p.planText("P001"))
}

func (p *acceptProject) removeRunState() {
	p.t.Helper()
	_ = os.RemoveAll(p.path(".git/docflow/tui"))
	_ = os.RemoveAll(p.runDir())
}

// TestAcceptanceWorkspaceNavigation covers the read-only part of the workflow:
// the tree and expanded unit card, the assignment editor before a start, a
// narrow terminal, a resize, Unicode text and NO_COLOR, and terminal restore.
func TestAcceptanceWorkspaceNavigation(t *testing.T) {
	plans := []acceptPlan{
		{
			ID: "P001", Title: "Терминал — 日本", Status: "active",
			Meta: map[string]string{"executor": "codex | model-a | high"},
			Units: []acceptUnit{
				{ID: "P001.1.1", Title: "First unit", Do: []string{"write a.txt"}, Verify: "test -f a.txt", Commit: "P001.1.1: first"},
				{ID: "P001.1.2", Title: "Second unit", Do: []string{"write b.txt"}, Verify: "test -f b.txt", Commit: "P001.1.2: second"},
			},
		},
		{ID: "P002", Title: "Draft plan", Status: "draft", Units: []acceptUnit{{ID: "P002.1.1", Title: "Draft unit", Verify: "true"}}},
	}
	p := newAcceptProject(t, plans)
	tui := startAcceptTUI(t, p, 120, 40)
	tui.waitFor("Queue (2)", 20*time.Second)
	tui.waitFor("Терминал — 日本", 10*time.Second)
	if strings.Contains(tui.output(), "\x1b[38;2;") {
		t.Fatal("NO_COLOR=1 still emitted a true-colour SGR sequence")
	}

	tui.selectUnit("P001.1.2")
	tui.waitFor("Second unit", 10*time.Second)
	for _, want := range []string{"Status:", "Goal", "Do", "- write b.txt", "Verify", "`test -f b.txt`", "Depends"} {
		tui.waitFor(want, 10*time.Second)
	}

	tui.send("?")
	tui.waitFor("Keyboard and workspace", 10*time.Second)
	tui.waitFor("stop after unit", 5*time.Second)
	tui.send(keyEsc)

	tui.send("m")
	tui.waitFor("Models and executors", 10*time.Second)
	for _, want := range []string{"P001.1.1", "P001.1.2", "codex | model-a | high"} {
		tui.waitFor(want, 10*time.Second)
	}
	tui.send(keyEsc)

	tui.resize(20, 6)
	tui.waitFor("Terminal too small", 10*time.Second)
	tui.waitFor("Need at least 32x10", 5*time.Second)
	tui.resize(100, 30)
	tui.waitForAfter("Terminal too small", "Queue", 10*time.Second)
	tui.resize(40, 12)
	tui.waitForAfter("Queue", "P001", 10*time.Second)
	tui.quit()
}

// TestAcceptancePauseChildProcessAndChangedAssignment drives the core
// interactive path: a running tool is read from its live call log, Pause kills
// the child process group and leaves no activity behind, the assignment is
// changed while paused, Resume starts a new session without spending an
// attempt, and the finished unit exposes its verify log and diff.
func TestAcceptancePauseChildProcessAndChangedAssignment(t *testing.T) {
	plans := []acceptPlan{{
		ID: "P001", Title: "Interactive run", Status: "active",
		Meta: map[string]string{"executor": "codex | model-a | high"},
		Units: []acceptUnit{{
			ID: "P001.1.1", Title: "Live control",
			Do: []string{"write a.txt"}, Verify: "printf 'VERIFY-MARKER-OK\\n'; test -f a.txt",
			Commit: "P001.1.1: live control",
		}},
	}}
	p := newAcceptProject(t, plans)
	p.writeStep(0, codexStep(codexStepOptions{
		Session: "sess-a", Tool: true, Command: "TOOL-RUNNING-MARKER", Result: "TOOL-RESULT-MARKER",
		Sleep: 10, ChildSleep: 4, WriteAfterSleep: "alpha", WritePath: "a.txt",
		Partial: `{"partial":"PARTIAL-TAIL-MARKER"`,
		Stderr:  "STDERR-MARKER", InputTokens: 10, OutputTokens: 4,
	})...)
	p.writeStep(1, codexStep(codexStepOptions{
		Session: "sess-b", Tool: true, Command: "SECOND-CALL-MARKER", Result: "SECOND-RESULT-MARKER",
		WriteAfterSleep: "alpha", WritePath: "a.txt", Stderr: "STDERR-MARKER",
		InputTokens: 11, OutputTokens: 5,
	})...)
	tui := startAcceptTUI(t, p, 120, 40)
	tui.waitFor("Queue", 20*time.Second)
	tui.send("s")
	tui.waitFor("Start the current Queue run?", 20*time.Second)
	tui.waitFor("codex | model-a | high", 5*time.Second)
	tui.send(keyEnter)
	tui.waitFor("starting the current Queue run", 10*time.Second)
	tui.waitPath(p.Scenario+".n", true, 20*time.Second)
	p.waitFakeCalls(1, 20*time.Second)

	// The running tool is the newest entry; its page reads the live call log
	// before the call has returned.
	tui.selectUnit("P001.1.1")
	tui.waitFor("TOOL-RUNNING-MARKER", 20*time.Second)
	// The incomplete line is on disk before the page is opened, so the live
	// read sees the current tail rather than a finished result.
	waitFileContains(t, p.path(".git/docflow/run/P001.1.1.1.out"), "PARTIAL-TAIL-MARKER", 10*time.Second)
	tui.send("/")
	tui.send("TOOL-RUNNING")
	tui.send(keyEnter)
	tui.send(keyEnter)
	tui.waitFor("TOOL-RUNNING-MARKER", 10*time.Second)
	// The live page also reads the incomplete JSON line at the current tail,
	// so a call still writing is not mistaken for a finished result.
	tui.waitFor("PARTIAL-TAIL-MARKER", 10*time.Second)
	if _, err := os.Stat(p.path("a.txt")); !os.IsNotExist(err) {
		t.Fatalf("the fake tool wrote a.txt before the pause: %v", err)
	}

	tui.send("p")
	cp := p.waitCheckpointStage("agent", 20*time.Second)
	if cp.Model != "model-a" || cp.Effort != "high" || cp.Session != "sess-a" {
		t.Fatalf("checkpoint route/session = %q|%q|%q|%q", cp.Agent, cp.Model, cp.Effort, cp.Session)
	}
	waitFile(t, p.path(".git/docflow/run/run.lock"), false, 20*time.Second)

	// A paused run writes nothing more: the journal, the call log and the
	// work tree stay exactly as the checkpoint left them, and the child that
	// was going to write late.txt is dead with the call's process group.
	journal := p.journalFiles()
	if len(journal) != 1 {
		t.Fatalf("journals after pause = %v", journal)
	}
	journalBefore, err := os.ReadFile(journal[0])
	if err != nil {
		t.Fatal(err)
	}
	logBefore, err := os.ReadFile(p.path(".git/docflow/run/P001.1.1.1.out"))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	journalAfter, _ := os.ReadFile(journal[0])
	logAfter, _ := os.ReadFile(p.path(".git/docflow/run/P001.1.1.1.out"))
	if !bytes.Equal(journalBefore, journalAfter) || !bytes.Equal(logBefore, logAfter) {
		t.Fatal("the paused runner kept writing events or call-log bytes")
	}
	time.Sleep(3 * time.Second)
	if _, err := os.Stat(p.path("late.txt")); !os.IsNotExist(err) {
		t.Fatalf("a child tool survived the pause and wrote late.txt: %v", err)
	}
	records := readJournalLines(t, journal[0])
	if len(eventByType(records, "state")) == 0 || !hasEvent(records, "run_end") {
		t.Fatalf("paused journal has no state/run_end: %v", eventNames(records))
	}
	end := eventByType(records, "run_end")
	if len(end) != 1 || end[0]["reason"] != "paused" || end[0]["exit_code"] != float64(0) || end[0]["paused"] != true {
		t.Fatalf("paused run_end = %v", end)
	}

	// Change the paused unit's assignment through the editor and resume. The
	// workspace does not replace the pause notice with a recovery dialog for
	// its own run; R uses the checkpoint already on disk.
	tui.send("m")
	tui.waitFor("Models and executors", 10*time.Second)
	tui.send(keyEnter)
	tui.waitFor("Enter confirms and saves", 5*time.Second)
	tui.send("\x15")
	tui.send("codex | model-a2 | low")
	tui.send(keyEnter)
	tui.waitFor("saved", 10*time.Second)
	tui.waitFor("Resume will start a new session", 10*time.Second)
	tui.send(keyEsc)
	configPath, err := runner.ConfigurationPath(p.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("the editor did not save the overlay: %v", err)
	}
	status := p.git("status", "--porcelain")
	if strings.Contains(status, "configuration") || strings.Contains(status, "docflow/tui") {
		t.Fatalf("the model edit left a file in the working tree:\n%s", status)
	}
	for _, line := range strings.Split(status, "\n") {
		if line == "" {
			continue
		}
		if !strings.HasSuffix(line, "docs/plans/P001-accept.md") {
			t.Fatalf("the paused run changed an unexpected file: %q", line)
		}
	}
	if strings.Contains(p.planText("P001"), "model-a2") {
		t.Fatal("the model edit rewrote the plan")
	}
	if _, err := os.Stat(filepath.Join(p.Home, ".config", "docflow", "roster.toml")); !os.IsNotExist(err) {
		t.Fatalf("the model edit wrote a personal roster: %v", err)
	}

	tui.send("R")
	// The resumed run is short: on a fast machine the whole run lock window
	// can close before the first poll, so the run is observed through its
	// outcome, not the transient lock.
	p.waitPlanStatus("P001.1.1", "x", 30*time.Second)
	waitFile(t, p.path(".git/docflow/run/run.lock"), false, 5*time.Second)

	calls := p.calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2 (paused call and one resumed call): %v", len(calls), calls)
	}
	argv, _ := calls[1]["argv"].([]any)
	var argvText []string
	for _, arg := range argv {
		argvText = append(argvText, fmt.Sprint(arg))
	}
	joined := strings.Join(argvText, " ")
	if !strings.Contains(joined, "-m model-a2") || !strings.Contains(joined, "model_reasoning_effort=low") {
		t.Fatalf("resumed call did not use the changed assignment: %v", argvText)
	}
	if strings.Contains(joined, "sess-a") {
		t.Fatalf("a changed assignment resumed the old session: %v", argvText)
	}
	stdin, _ := calls[1]["stdin"].(string)
	if !strings.Contains(stdin, "## Recovery") || !strings.Contains(stdin, "A new session starts with the current tree") {
		t.Fatalf("the new session prompt does not explain the lost session:\n%s", stdin)
	}
	// The paused attempt is continued, not spent: the resumed journal has one
	// attempt with n=1 and no n=2.
	_, resumed := p.newestJournalExcept(journal[0])
	attempts := eventByType(resumed, "attempt")
	if len(attempts) != 1 {
		t.Fatalf("resumed attempts = %v", attempts)
	}
	if n, _ := numberField(attempts[0], "n"); n != 1 {
		t.Fatalf("resume spent an extra attempt: n=%v", attempts[0]["n"])
	}
	if len(eventByType(resumed, "route_switch")) != 0 {
		t.Fatalf("a resume on a changed assignment reported a fallback: %v", eventByType(resumed, "route_switch"))
	}

	// The finished unit keeps its call history, its verify result and the
	// working-tree diff.
	tui.selectUnit("P001.1.1")
	tui.waitFor("work done", 10*time.Second)
	tui.send("v")
	tui.waitFor("VERIFY-MARKER-OK", 10*time.Second)
	tui.send(keyEsc)
	if err := os.WriteFile(p.path("a.txt"), []byte("modified-by-person"), 0o644); err != nil {
		t.Fatal(err)
	}
	tui.send("d")
	tui.waitFor("modified-by-person", 10*time.Second)
	tui.waitFor("-alpha", 5*time.Second)
	tui.send(keyEsc)
	tui.send("/")
	tui.send("SECOND-RESULT")
	tui.send(keyEnter)
	tui.send(keyEnter)
	tui.waitFor("SECOND-RESULT-MARKER", 10*time.Second)
	tui.send(keyEsc)
	tui.send("/")
	tui.send("STDERR-MARKER")
	tui.send(keyEnter)
	tui.send(keyEnter)
	tui.waitFor("STDERR-MARKER", 10*time.Second)
	tui.send(keyEsc)

	// Reopening after the completed unit offers the checkpoint as completed,
	// not as resumable work.
	tui.quit()
	tui = startAcceptTUI(t, p, 120, 40)
	tui.waitFor("already complete", 20*time.Second)
	tui.send("R")
	tui.waitFor("already complete; press s to start a new run", 10*time.Second)
	tui.send("q")
	tui.waitExit(20 * time.Second)
}

// TestAcceptanceStopAfterUnitAndStopNow keeps the two stops distinct: S ends
// the run after the current unit without starting the next one, x stops the
// live call at once with no resumable checkpoint, and neither leaves a child
// process or the checkout lock behind.
func TestAcceptanceStopAfterUnitAndStopNow(t *testing.T) {
	t.Run("stop after unit", func(t *testing.T) {
		plans := []acceptPlan{{
			ID: "P001", Title: "Two units", Status: "active",
			Meta: map[string]string{"executor": "codex | model-a | high"},
			Units: []acceptUnit{
				{ID: "P001.1.1", Title: "First", Do: []string{"write a.txt"}, Verify: "test -f a.txt", Commit: "P001.1.1: first"},
				{ID: "P001.1.2", Title: "Second", Do: []string{"write b.txt"}, Verify: "test -f b.txt", Commit: "P001.1.2: second"},
			},
		}}
		p := newAcceptProject(t, plans)
		p.writeStep(0, codexStep(codexStepOptions{
			Session: "sess-first", Sleep: 3, ChildSleep: 6, WriteAfterSleep: "alpha", WritePath: "a.txt",
		})...)
		p.writeStep(1, codexStep(codexStepOptions{
			Session: "sess-second", WriteAfterSleep: "beta", WritePath: "b.txt",
		})...)
		tui := startAcceptTUI(t, p, 120, 40)
		tui.waitFor("Queue", 20*time.Second)
		tui.send("s")
		tui.waitFor("Start the current Queue run?", 20*time.Second)
		tui.send(keyEnter)
		tui.waitPath(p.Scenario+".n", true, 20*time.Second)
		tui.send("S")
		p.waitPlanStatus("P001.1.1", "x", 30*time.Second)
		waitFile(t, p.path(".git/docflow/run/run.lock"), false, 30*time.Second)
		time.Sleep(4 * time.Second)
		if p.callCount() != 1 {
			t.Fatalf("stop after unit started the next unit: %d calls", p.callCount())
		}
		if _, err := os.Stat(p.path("b.txt")); !os.IsNotExist(err) {
			t.Fatalf("the second unit ran after Stop after unit: %v", err)
		}
		if !strings.Contains(p.planText("P001"), "[ ] P001.1.2") {
			t.Fatalf("the second unit was touched:\n%s", p.planText("P001"))
		}
		_, records := p.newestJournal()
		if len(eventByType(records, "unit_start")) != 1 {
			t.Fatalf("stop after unit started another unit: %v", eventNames(records))
		}
		end := eventByType(records, "run_end")
		if len(end) != 1 || end[0]["exit_code"] != float64(0) || end[0]["paused"] == true {
			t.Fatalf("stop after unit run_end = %v", end)
		}
		tui.quit()
	})

	t.Run("stop now", func(t *testing.T) {
		plans := []acceptPlan{{
			ID: "P001", Title: "Stop now", Status: "active",
			Meta: map[string]string{"executor": "codex | model-a | high"},
			Units: []acceptUnit{
				{ID: "P001.1.1", Title: "First", Do: []string{"write a.txt"}, Verify: "test -f a.txt", Commit: "P001.1.1: first"},
				{ID: "P001.1.2", Title: "Second", Do: []string{"write b.txt"}, Verify: "test -f b.txt", Commit: "P001.1.2: second"},
			},
		}}
		p := newAcceptProject(t, plans)
		p.writeStep(0, codexStep(codexStepOptions{
			Session: "sess-now", Sleep: 10, ChildSleep: 8, WriteAfterSleep: "alpha", WritePath: "a.txt",
		})...)
		tui := startAcceptTUI(t, p, 120, 40)
		tui.waitFor("Queue", 20*time.Second)
		tui.send("s")
		tui.waitFor("Start the current Queue run?", 20*time.Second)
		tui.send(keyEnter)
		tui.waitPath(p.Scenario+".n", true, 20*time.Second)
		tui.send("x")
		waitFile(t, p.path(".git/docflow/run/run.lock"), false, 30*time.Second)
		time.Sleep(1 * time.Second)
		if _, err := os.Stat(p.path(".git/docflow/tui/checkpoint.json")); !os.IsNotExist(err) {
			t.Fatalf("Stop now wrote a resumable checkpoint: %v", err)
		}
		if _, err := os.Stat(p.path("a.txt")); !os.IsNotExist(err) {
			t.Fatalf("the stopped agent kept working: %v", err)
		}
		time.Sleep(8 * time.Second)
		if _, err := os.Stat(p.path("a.txt")); !os.IsNotExist(err) {
			t.Fatalf("a child of the stopped agent survived: %v", err)
		}
		_, records := p.newestJournal()
		unitEnd := eventByType(records, "unit_end")
		if len(unitEnd) != 1 || unitEnd[0]["status"] != "stopped" {
			t.Fatalf("Stop now unit_end = %v", unitEnd)
		}
		end := eventByType(records, "run_end")
		if len(end) != 1 || end[0]["reason"] != "stopped" || end[0]["exit_code"] != float64(0) || end[0]["paused"] == true {
			t.Fatalf("Stop now run_end = %v", end)
		}
		if !strings.Contains(p.planText("P001"), "[~] P001.1.1") {
			t.Fatalf("Stop now did not leave the interrupted unit in progress:\n%s", p.planText("P001"))
		}
		tui.quit()
	})
}

// TestAcceptanceAssignmentsFallbackAndLimits checks that the start dialog
// shows the assigned chain and the effective limits, that a provider limit
// switches to the fallback without spending an attempt, that the actual argv
// carries the model and effort, and that a zero usage is shown as unknown
// rather than as a measured zero.
func TestAcceptanceAssignmentsFallbackAndLimits(t *testing.T) {
	plans := []acceptPlan{{
		ID: "P001", Title: "Fallback run", Status: "active",
		Meta: map[string]string{
			"executor": "codex | model-a | high",
			"fallback": "claude | model-b | low",
		},
		Units: []acceptUnit{{
			ID: "P001.1.1", Title: "Fallback unit", Do: []string{"write a.txt"},
			Verify: "test -f a.txt", Commit: "P001.1.1: fallback",
		}},
	}}
	p := newAcceptProject(t, plans)
	p.writeStep(0,
		shellText(`{"type":"thread.started","thread_id":"sess-limit"}`, `{"type":"turn.started"}`, `{"type":"error","message":"usage limit reached"}`)...,
	)
	p.writeStep(0, "exit 1\n")
	// The first call is written in two steps above; the fake agent runs only
	// 0.sh for call 0, so build the whole refusal in one file instead.
	p.writeStep(0,
		append(shellText(
			`{"type":"thread.started","thread_id":"sess-limit"}`,
			`{"type":"turn.started"}`,
			`{"type":"error","message":"usage limit reached"}`,
		), "exit 1\n")...,
	)
	// The claude result carries zero usage: the editor must show it as
	// unknown rather than as a measured zero.
	p.writeStep(1, claudeStep(claudeStepOptions{Session: "sess-fallback", Write: "alpha", WritePath: "a.txt"})...)

	tui := startAcceptTUI(t, p, 120, 40, "--attempts", "2", "--replans", "0")
	tui.waitFor("Queue", 20*time.Second)
	tui.send("s")
	tui.waitFor("Start the current Queue run?", 20*time.Second)
	tui.waitFor("attempts 2 · auto-replan off", 5*time.Second)
	tui.waitFor("P001.1.1: codex | model-a | high ; claude | model-b | low", 5*time.Second)
	tui.send(keyEnter)
	p.waitPlanStatus("P001.1.1", "x", 40*time.Second)
	waitFile(t, p.path(".git/docflow/run/run.lock"), false, 30*time.Second)

	calls := p.calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want the refused codex call and the claude fallback: %v", len(calls), calls)
	}
	first := fmt.Sprint(calls[0]["argv"])
	second := fmt.Sprint(calls[1]["argv"])
	if !strings.Contains(first, "-m model-a") || !strings.Contains(first, "model_reasoning_effort=high") {
		t.Fatalf("assigned codex argv = %s", first)
	}
	if !strings.Contains(second, "--model model-b") || !strings.Contains(second, "--effort low") {
		t.Fatalf("fallback claude argv = %s", second)
	}
	if !strings.Contains(fmt.Sprint(calls[1]["stdin"]), "## Previous attempt") {
		t.Fatalf("the fallback did not receive the refused attempt context:\n%s", calls[1]["stdin"])
	}
	_, records := p.newestJournal()
	switches := eventByType(records, "route_switch")
	if len(switches) != 1 || switches[0]["kind"] != "limit" {
		t.Fatalf("route_switch = %v", switches)
	}
	to, _ := switches[0]["to"].(map[string]any)
	if to["agent"] != "claude" || to["model"] != "model-b" || to["effort"] != "low" {
		t.Fatalf("route_switch target = %v", to)
	}
	attempts := eventByType(records, "attempt")
	if len(attempts) != 2 {
		t.Fatalf("attempts = %v", attempts)
	}
	if len(eventByType(records, "quota")) != 0 {
		t.Fatalf("an unavailable quota was journaled as measured: %v", eventByType(records, "quota"))
	}
	for _, attempt := range attempts {
		if n, _ := numberField(attempt, "n"); n != 1 {
			t.Fatalf("a fallback spent an attempt: %v", attempt)
		}
	}
	end := eventByType(records, "run_end")
	if len(end) != 1 || end[0]["exit_code"] != float64(0) || end[0]["paused"] == true {
		t.Fatalf("run_end = %v", end)
	}
	if !strings.Contains(p.planText("P001"), "executor: codex | model-a | high") {
		t.Fatalf("the fallback rewrote the plan assignment:\n%s", p.planText("P001"))
	}
	if !p.clean() {
		t.Fatalf("the fallback left the tree dirty:\n%s", p.git("status", "--porcelain"))
	}

	tui.selectUnit("P001.1.1")
	tui.waitFor("claude | model-b | low", 10*time.Second)
	tui.waitFor("tokens in unknown", 10*time.Second)
	tui.quit()
}

// TestAcceptanceStartRefusals covers the starts that must not run anything:
// a live foreign runner, a declined full-permission confirmation, a draft
// plan, a corrupted checkpoint and a checkpoint for a completed unit.
func TestAcceptanceStartRefusals(t *testing.T) {
	t.Run("foreign runner", func(t *testing.T) {
		plans := []acceptPlan{{
			ID: "P001", Title: "Foreign runner", Status: "active",
			Meta:  map[string]string{"executor": "codex | model-a | high"},
			Units: []acceptUnit{{ID: "P001.1.1", Title: "Unit", Do: []string{"write a.txt"}, Verify: "test -f a.txt"}},
		}}
		p := newAcceptProject(t, plans)
		tui := startAcceptTUI(t, p, 120, 40)
		tui.waitFor("Queue", 20*time.Second)

		runDir := p.runDir()
		if err := os.MkdirAll(runDir, 0o755); err != nil {
			t.Fatal(err)
		}
		sleeper := exec.Command("sleep", "60")
		if err := sleeper.Start(); err != nil {
			t.Fatal(err)
		}
		lock := filepath.Join(runDir, "run.lock")
		pid := sleeper.Process.Pid
		t.Cleanup(func() {
			_ = sleeper.Process.Kill()
			_, _ = sleeper.Process.Wait()
			_ = os.Remove(lock)
		})
		if err := os.WriteFile(lock, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		tui.send("s")
		tui.waitFor("another runner is active (pid "+strconv.Itoa(pid)+")", 10*time.Second)
		if p.callCount() != 0 || len(p.journalFiles()) != 0 {
			t.Fatalf("a foreign runner still started a call or a journal: calls=%d journals=%v", p.callCount(), p.journalFiles())
		}
		if !strings.Contains(p.planText("P001"), "status: active") {
			t.Fatal("the refused start changed the plan")
		}
		tui.send("a")
		tui.waitFor("viewing is allowed, lifecycle changes are refused", 10*time.Second)
		tui.quit()
	})

	t.Run("full consent declined", func(t *testing.T) {
		plans := []acceptPlan{{
			ID: "P001", Title: "Full access", Status: "active",
			Meta:  map[string]string{"executor": "codex | model-a | high", "permissions": "full"},
			Units: []acceptUnit{{ID: "P001.1.1", Title: "Unit", Do: []string{"write a.txt"}, Verify: "test -f a.txt"}},
		}}
		p := newAcceptProject(t, plans)
		tui := startAcceptTUI(t, p, 120, 40)
		tui.waitFor("Queue", 20*time.Second)
		tui.send("s")
		tui.waitFor("Start the current Queue run?", 20*time.Second)
		tui.send("n")
		tui.waitFor("start declined; nothing ran", 10*time.Second)
		time.Sleep(500 * time.Millisecond)
		if p.callCount() != 0 {
			t.Fatalf("a declined full-permission start called an agent: %d calls", p.callCount())
		}
		if len(p.journalFiles()) != 0 {
			t.Fatalf("a declined start wrote a journal: %v", p.journalFiles())
		}
		if _, err := os.Stat(p.path(".git/docflow/tui/checkpoint.json")); !os.IsNotExist(err) {
			t.Fatalf("a declined start wrote a checkpoint: %v", err)
		}
		if _, err := os.Stat(p.path(".git/docflow/run/run.lock")); !os.IsNotExist(err) {
			t.Fatalf("a declined start left the checkout lock: %v", err)
		}
		tui.quit()
	})

	t.Run("draft plan", func(t *testing.T) {
		plans := []acceptPlan{{
			ID: "P001", Title: "Draft only", Status: "draft",
			Units: []acceptUnit{{ID: "P001.1.1", Title: "Draft unit", Verify: "true"}},
		}}
		p := newAcceptProject(t, plans)
		tui := startAcceptTUI(t, p, 120, 40)
		tui.waitFor("Queue", 20*time.Second)
		tui.send("s")
		tui.waitFor("nothing to run", 20*time.Second)
		tui.send(keyEnter)
		tui.waitFor("run finished", 20*time.Second)
		time.Sleep(300 * time.Millisecond)
		if p.callCount() != 0 {
			t.Fatalf("a draft plan started an agent: %d calls", p.callCount())
		}
		if !strings.Contains(p.planText("P001"), "status: draft") {
			t.Fatal("starting the Queue changed the draft plan")
		}
		if _, err := os.Stat(p.path(".git/docflow/run/run.lock")); !os.IsNotExist(err) {
			t.Fatalf("the draft start left the checkout lock: %v", err)
		}
		tui.quit()
	})

	t.Run("corrupted checkpoint", func(t *testing.T) {
		plans := []acceptPlan{{
			ID: "P001", Title: "Broken checkpoint", Status: "active",
			Meta:  map[string]string{"executor": "codex | model-a | high"},
			Units: []acceptUnit{{ID: "P001.1.1", Title: "Unit", Verify: "true"}},
		}}
		p := newAcceptProject(t, plans)
		path := p.path(".git/docflow/tui/checkpoint.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		tui := startAcceptTUI(t, p, 120, 40)
		tui.waitFor("Queue", 20*time.Second)
		tui.waitFor("Broken checkpoint", 10*time.Second)
		tui.send("R")
		tui.waitFor("no resumable checkpoint", 20*time.Second)
		if p.callCount() != 0 {
			t.Fatalf("a corrupted checkpoint started an agent: %d calls", p.callCount())
		}
		tui.send("q")
		tui.waitExit(20 * time.Second)
	})

	t.Run("completed checkpoint", func(t *testing.T) {
		plans := []acceptPlan{{
			ID: "P001", Title: "Completed", Status: "active",
			Meta:  map[string]string{"executor": "codex | model-a | high"},
			Units: []acceptUnit{{ID: "P001.1.1", Title: "Done unit", Status: "x", Verify: "true", Commit: "P001.1.1: done"}},
		}}
		p := newAcceptProject(t, plans)
		planText := p.planText("P001")
		head := p.git("rev-parse", "HEAD")
		if err := runner.WriteCheckpoint(p.Root, runner.Checkpoint{
			Plan: "P001", Unit: "P001.1.1", Head: head, PlanHash: runner.PlanFingerprint([]byte(planText)),
			Stage: "agent", Agent: "codex", Model: "model-a", Effort: "high", Calls: 1,
		}); err != nil {
			t.Fatal(err)
		}
		tui := startAcceptTUI(t, p, 120, 40)
		tui.waitFor("Recover the saved run", 20*time.Second)
		tui.waitFor("already complete", 10*time.Second)
		tui.send("R")
		tui.waitFor("already complete; press s to start a new run", 10*time.Second)
		if p.callCount() != 0 {
			t.Fatalf("a completed checkpoint started an agent: %d calls", p.callCount())
		}
		tui.quit()
	})
}

// TestAcceptanceHumanBlockAndNote stops a run on a human block, types the note
// into the dialog, unblocks through the existing CLI command and starts the
// run again. The plan must record the note and finish the unit.
func TestAcceptanceHumanBlockAndNote(t *testing.T) {
	plans := []acceptPlan{{
		ID: "P001", Title: "Human block", Status: "active",
		Meta: map[string]string{"executor": "codex | model-a | high"},
		Units: []acceptUnit{{
			ID: "P001.1.1", Title: "Blocked unit", Do: []string{"write a.txt"},
			Verify: "test -f a.txt", Commit: "P001.1.1: unblocked",
		}},
	}}
	p := newAcceptProject(t, plans)
	p.writeStep(0, shellText(
		`{"type":"thread.started","thread_id":"sess-block"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":`+strconv.Quote(blockedResult())+`}}`,
		`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`,
	)...)
	p.writeStep(1, codexStep(codexStepOptions{
		Session: "sess-unblocked", WriteAfterSleep: "alpha", WritePath: "a.txt",
	})...)
	tui := startAcceptTUI(t, p, 120, 40)
	tui.waitFor("Queue", 20*time.Second)
	tui.send("s")
	tui.waitFor("Start the current Queue run?", 20*time.Second)
	tui.send(keyEnter)
	tui.waitFor("Continue runs the existing unblock command", 30*time.Second)
	tui.waitFor("needs a person", 10*time.Second)
	if !strings.Contains(p.planText("P001"), "[!] P001.1.1") {
		t.Fatalf("the runner did not write the block into the plan:\n%s", p.planText("P001"))
	}
	tui.send("Вошёл в аккаунт")
	tui.send(keyEnter)
	tui.waitFor("unblocked; starting the run again", 30*time.Second)
	tui.waitFor("Start the current Queue run?", 20*time.Second)
	tui.send(keyEnter)
	p.waitPlanStatus("P001.1.1", "x", 30*time.Second)
	waitFile(t, p.path(".git/docflow/run/run.lock"), false, 30*time.Second)
	text := p.planText("P001")
	for _, want := range []string{"[x] P001.1.1", "Unblocked: Вошёл в аккаунт"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the unblocked plan lost %q:\n%s", want, text)
		}
	}
	if p.callCount() != 2 {
		t.Fatalf("calls = %d, want the blocked call and one new run", p.callCount())
	}
	if subject := p.git("log", "-1", "--format=%s"); subject != "P001.1.1: unblocked" {
		t.Fatalf("last commit = %q", subject)
	}
	tui.quit()
}

// TestAcceptanceLargeStreamAndUnknownEvents feeds a large unknown stream
// through the live host: the workspace stays responsive, keeps a bounded
// history, sanitizes an escape sequence from agent output, preserves the
// original call log, and moves between the oldest entry and live follow.
func TestAcceptanceLargeStreamAndUnknownEvents(t *testing.T) {
	plans := []acceptPlan{{
		ID: "P001", Title: "Large stream", Status: "active",
		Meta: map[string]string{"executor": "codex | model-a | high"},
		Units: []acceptUnit{{
			ID: "P001.1.1", Title: "Stream unit", Do: []string{"write a.txt"},
			Verify: "true", Commit: "P001.1.1: stream",
		}},
	}}
	p := newAcceptProject(t, plans)
	step := []string{
		"i=0\nwhile [ \"$i\" -lt 1200 ]; do printf '{\"type\":\"weird\",\"text\":\"unknown-%d\"}\\n' \"$i\"; i=$((i+1)); done\n",
	}
	step = append(step, shellText(`{"type":"weird","text":"unknown-evil-\u001b]0;evil\u0007"}`)...)
	step = append(step, "printf alpha > a.txt\n")
	step = append(step, shellText(
		`{"type":"thread.started","thread_id":"sess-large"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":`+strconv.Quote(doneResult())+`}}`,
		`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`,
	)...)
	p.writeStep(0, step...)
	tui := startAcceptTUI(t, p, 140, 42)
	tui.waitFor("Queue", 20*time.Second)
	tui.send("s")
	tui.waitFor("Start the current Queue run?", 20*time.Second)
	tui.send(keyEnter)
	tui.waitPath(p.Scenario+".n", true, 20*time.Second)
	// The stream is still being parsed: the workspace must answer a key.
	tui.send("?")
	tui.waitFor("Keyboard and workspace", 20*time.Second)
	tui.send(keyEsc)

	p.waitPlanStatus("P001.1.1", "x", 60*time.Second)
	waitFile(t, p.path(".git/docflow/run/run.lock"), false, 30*time.Second)
	logPath := p.path(".git/docflow/run/P001.1.1.1.out")
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(logData) < 30000 || !bytes.Contains(logData, []byte("unknown-1199")) {
		t.Fatalf("the original call log is not intact: %d bytes", len(logData))
	}

	tui.selectUnit("P001.1.1")
	tui.send("/")
	tui.send("unknown-1199")
	tui.send(keyEnter)
	tui.waitFor("unknown-1199", 20*time.Second)
	if strings.Contains(tui.screen(), "evil") || strings.Contains(tui.output(), "\x1b]0;evil") {
		t.Fatalf("agent output executed an escape sequence:\n%s", tui.screen())
	}
	tui.send("g")
	tui.waitFor("paused: new events do not move the view", 10*time.Second)
	tui.send("G")
	tui.waitFor("following the newest event", 10*time.Second)
	tui.send(keyEsc)
	tui.quit()
}

// TestAcceptanceOrdinaryRunUnaffected runs the plain CLI next to a saved
// interactive configuration: the ordinary run keeps its assignment rules, its
// journal, its exit code and never reads or writes the TUI state.
func TestAcceptanceOrdinaryRunUnaffected(t *testing.T) {
	plans := []acceptPlan{{
		ID: "P001", Title: "Ordinary run", Status: "active",
		Meta: map[string]string{"executor": "codex | model-a | high"},
		Units: []acceptUnit{{
			ID: "P001.1.1", Title: "Plain unit", Do: []string{"write a.txt"},
			Verify: "test -f a.txt", Commit: "P001.1.1: plain",
		}},
	}}
	p := newAcceptProject(t, plans)
	p.writeStep(0, codexStep(codexStepOptions{
		Session: "sess-plain", WriteAfterSleep: "alpha", WritePath: "a.txt",
	})...)
	configuration := runner.NewRunConfiguration()
	configuration.Units["P001.1.1"] = []agents.Route{{Agent: "codex", Model: "ignored-model", Effort: "low"}}
	if err := runner.WriteConfiguration(p.Root, configuration); err != nil {
		t.Fatal(err)
	}
	configPath, err := runner.ConfigurationPath(p.Root)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(acceptanceBinary(t), "run", "--yes", "--max-units", "1")
	cmd.Dir = p.Root
	cmd.Env = append([]string{}, p.Env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ordinary run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "P001.1.1 done") {
		t.Fatalf("ordinary run output:\n%s", out)
	}
	if !strings.Contains(p.planText("P001"), "[x] P001.1.1") {
		t.Fatalf("ordinary run did not finish the unit:\n%s", p.planText("P001"))
	}
	if subject := p.git("log", "-1", "--format=%s"); subject != "P001.1.1: plain" {
		t.Fatalf("ordinary run commit = %q", subject)
	}
	calls := p.calls()
	if len(calls) != 1 {
		t.Fatalf("ordinary run calls = %d", len(calls))
	}
	argv := fmt.Sprint(calls[0]["argv"])
	if !strings.Contains(argv, "-m model-a") || strings.Contains(argv, "ignored-model") {
		t.Fatalf("ordinary run used the TUI override: %s", argv)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ordinary run removed the interactive configuration: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("ordinary run rewrote the interactive configuration")
	}
	if _, err := os.Stat(p.path(".git/docflow/tui/checkpoint.json")); !os.IsNotExist(err) {
		t.Fatalf("ordinary run wrote a TUI checkpoint: %v", err)
	}
	_, records := p.newestJournal()
	if hasEvent(records, "state") {
		t.Fatalf("ordinary run emitted a pause state: %v", eventNames(records))
	}
	end := eventByType(records, "run_end")
	if len(end) != 1 || end[0]["exit_code"] != float64(0) || end[0]["paused"] == true {
		t.Fatalf("ordinary run_end = %v", end)
	}
	unitEnd := eventByType(records, "unit_end")
	if len(unitEnd) != 1 || unitEnd[0]["status"] != "done" {
		t.Fatalf("ordinary unit_end = %v", unitEnd)
	}
}

// TestAcceptanceReopenPausedRun reopens the workspace on a saved pause. The
// recovery dialog shows the assignment table from a real configuration.json,
// Resume continues with a new session after a changed assignment, and a plan
// that moved since the pause is refused as a conflict.
func TestAcceptanceReopenPausedRun(t *testing.T) {
	pauseAndQuit := func(t *testing.T, title string) (*acceptProject, *acceptTUI) {
		t.Helper()
		plans := []acceptPlan{{
			ID: "P001", Title: title, Status: "active",
			Meta: map[string]string{"executor": "codex | model-a | high"},
			Units: []acceptUnit{{
				ID: "P001.1.1", Title: "Paused unit", Do: []string{"write a.txt"},
				Verify: "test -f a.txt", Commit: "P001.1.1: reopened",
			}},
		}}
		p := newAcceptProject(t, plans)
		p.writeStep(0, codexStep(codexStepOptions{
			Session: "sess-paused", Sleep: 10, ChildSleep: 6, WriteAfterSleep: "alpha", WritePath: "a.txt",
		})...)
		p.writeStep(1, codexStep(codexStepOptions{
			Session: "sess-resumed", WriteAfterSleep: "alpha", WritePath: "a.txt",
		})...)
		tui := startAcceptTUI(t, p, 120, 40)
		tui.waitFor("Queue", 20*time.Second)
		tui.send("s")
		tui.waitFor("Start the current Queue run?", 20*time.Second)
		tui.send(keyEnter)
		tui.waitPath(p.Scenario+".n", true, 20*time.Second)
		p.waitFakeCalls(1, 20*time.Second)
		tui.send("p")
		p.waitCheckpointStage("agent", 20*time.Second)
		waitFile(t, p.path(".git/docflow/run/run.lock"), false, 20*time.Second)
		tui.quit()
		return p, tui
	}

	t.Run("continue with saved assignment", func(t *testing.T) {
		p, _ := pauseAndQuit(t, "Reopen pausing")
		configuration := runner.NewRunConfiguration()
		configuration.Units["P001.1.1"] = []agents.Route{{Agent: "codex", Model: "model-a2", Effort: "low"}}
		if err := runner.WriteConfiguration(p.Root, configuration); err != nil {
			t.Fatal(err)
		}
		tui := startAcceptTUI(t, p, 120, 40)
		tui.waitFor("Recover the saved run", 20*time.Second)
		tui.waitFor("an agent call", 10*time.Second)
		tui.waitFor("P001.1.1: codex | model-a2 | low", 10*time.Second)
		tui.send("R")
		// The resumed run is short: on a fast machine the whole run lock
		// window can close before the first poll, so the run is observed
		// through its outcome, not the transient lock.
		p.waitPlanStatus("P001.1.1", "x", 30*time.Second)
		waitFile(t, p.path(".git/docflow/run/run.lock"), false, 5*time.Second)
		calls := p.calls()
		if len(calls) != 2 {
			t.Fatalf("reopened run calls = %d, want the paused call and one resume", len(calls))
		}
		argv := fmt.Sprint(calls[1]["argv"])
		if !strings.Contains(argv, "-m model-a2") || !strings.Contains(argv, "model_reasoning_effort=low") || strings.Contains(argv, "sess-paused") {
			t.Fatalf("resumed argv = %s", argv)
		}
		if subject := p.git("log", "-1", "--format=%s"); subject != "P001.1.1: reopened" {
			t.Fatalf("resumed commit = %q", subject)
		}
		_, records := p.newestJournal()
		attempts := eventByType(records, "attempt")
		if len(attempts) != 1 {
			t.Fatalf("resumed attempts = %v", attempts)
		}
		if n, _ := numberField(attempts[0], "n"); n != 1 {
			t.Fatalf("resume after reopening spent an extra attempt: %v", attempts[0])
		}
		tui.quit()
	})

	t.Run("conflict after the plan moved", func(t *testing.T) {
		p, _ := pauseAndQuit(t, "Reopen conflict")
		path := p.path("docs/plans/P001-accept.md")
		text := p.planText("P001")
		edited := strings.Replace(text, "title: Reopen conflict", "title: Reopen conflict edited", 1)
		if edited == text {
			t.Fatal("the plan title was not found for the conflict edit")
		}
		if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
		tui := startAcceptTUI(t, p, 120, 40)
		tui.waitFor("Recover the saved run", 20*time.Second)
		tui.waitFor("Cannot continue this checkpoint: runner: recovery conflict: plan P001 changed since the pause", 10*time.Second)
		tui.send("R")
		tui.waitFor("cannot resume the saved run: runner: recovery conflict: plan P001 changed since the pause", 10*time.Second)
		if p.callCount() != 1 {
			t.Fatalf("a conflicted checkpoint started a call: %d calls", p.callCount())
		}
		tui.quit()
	})
}

// TestAcceptancePendingCommitRecovery builds a real done/commit interruption
// with the CLI, then lets the workspace recover it: the dialog names the
// pending transaction, R commits the existing work without calling an agent,
// and the tree ends clean.
func TestAcceptancePendingCommitRecovery(t *testing.T) {
	plans := []acceptPlan{{
		ID: "P001", Title: "Pending commit", Status: "active",
		Meta: map[string]string{"executor": "codex | model-a | high"},
		Units: []acceptUnit{{
			ID: "P001.1.1", Title: "Pending unit", Do: []string{"write a.txt"},
			Verify: "test -f a.txt", Commit: "P001.1.1: pending",
		}},
	}}
	p := newAcceptProject(t, plans)
	runCLI := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(acceptanceBinary(t), args...)
		cmd.Dir = p.Root
		cmd.Env = append([]string{}, p.Env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("docflow %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	runCLI("prompt", "P001.1.1")
	if err := os.WriteFile(p.path("a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCLI("verify", "P001.1.1")
	runCLI("done", "P001.1.1", "--handoff=recovered commit")
	if !strings.Contains(p.planText("P001"), "[x] P001.1.1") {
		t.Fatalf("done did not mark the unit:\n%s", p.planText("P001"))
	}
	planText := p.planText("P001")
	head := p.git("rev-parse", "HEAD")
	if err := runner.WriteCheckpoint(p.Root, runner.Checkpoint{
		Plan: "P001", Unit: "P001.1.1", Head: head, PlanHash: runner.PlanFingerprint([]byte(planText)),
		Stage: "commit", Agent: "codex", Model: "model-a", Effort: "high", Calls: 1,
	}); err != nil {
		t.Fatal(err)
	}
	tui := startAcceptTUI(t, p, 120, 40)
	tui.waitFor("Recover the saved run", 20*time.Second)
	tui.waitFor("the done/commit transaction (commit pending)", 10*time.Second)
	tui.waitFor("R finishes the pending commit", 10*time.Second)
	tui.send("R")
	// The pending-commit recovery is a single commit with no agent call: on a
	// fast machine the whole run lock window can pass between two polls, so
	// the run is observed through its finished notice, not the transient lock.
	tui.waitFor("run finished", 30*time.Second)
	waitFile(t, p.path(".git/docflow/run/run.lock"), false, 40*time.Second)
	if subject := p.git("log", "-1", "--format=%s"); subject != "P001.1.1: pending" {
		t.Fatalf("recovered commit = %q", subject)
	}
	if p.callCount() != 0 {
		t.Fatalf("pending-commit recovery called an agent %d times", p.callCount())
	}
	if !p.clean() {
		t.Fatalf("recovered commit left the tree dirty:\n%s", p.git("status", "--porcelain"))
	}
	if _, err := os.Stat(p.path(".git/docflow/committing/P001.1.1.json")); !os.IsNotExist(err) {
		t.Fatalf("the committing record survived the recovery: %v", err)
	}
	_, records := p.newestJournal()
	unitEnd := eventByType(records, "unit_end")
	if len(unitEnd) != 1 || unitEnd[0]["status"] != "done" {
		t.Fatalf("recovered unit_end = %v", unitEnd)
	}
	if len(eventByType(records, "commit")) != 1 {
		t.Fatalf("recovered journal has no commit: %v", eventNames(records))
	}
	tui.quit()
}

// TestAcceptancePauseDuringVerify interrupts the verification subprocess at
// a stable boundary: the check and the done/commit transaction run to their
// end, the checkpoint names the complete boundary, the unit is committed and
// the journal records a pause rather than a failed commit.
func TestAcceptancePauseDuringVerify(t *testing.T) {
	plans := []acceptPlan{{
		ID: "P001", Title: "Verify pause", Status: "active",
		Meta: map[string]string{"executor": "codex | model-a | high"},
		Units: []acceptUnit{{
			ID: "P001.1.1", Title: "Verify unit", Do: []string{"write a.txt"},
			Verify: "printf started > verify-started; sleep 3; test -f a.txt",
			Commit: "P001.1.1: verify pause",
		}},
	}}
	p := newAcceptProject(t, plans)
	p.writeStep(0, codexStep(codexStepOptions{
		Session: "sess-verify", WriteAfterSleep: "alpha", WritePath: "a.txt",
	})...)
	tui := startAcceptTUI(t, p, 120, 40)
	tui.waitFor("Queue", 20*time.Second)
	tui.send("s")
	tui.waitFor("Start the current Queue run?", 20*time.Second)
	tui.send(keyEnter)
	waitFile(t, p.path("verify-started"), true, 30*time.Second)
	tui.send("p")
	cp := p.waitCheckpointStage("complete", 30*time.Second)
	if cp.Session != "sess-verify" {
		t.Fatalf("verify checkpoint session = %q", cp.Session)
	}
	p.waitPlanStatus("P001.1.1", "x", 30*time.Second)
	waitFile(t, p.path(".git/docflow/run/run.lock"), false, 20*time.Second)
	if subject := p.git("log", "-1", "--format=%s"); subject != "P001.1.1: verify pause" {
		t.Fatalf("a paused verify did not complete its commit: %q", subject)
	}
	_, journal := p.newestJournal()
	end := eventByType(journal, "run_end")
	if len(end) != 1 || end[0]["paused"] != true || end[0]["exit_code"] != float64(0) {
		t.Fatalf("verify pause run_end = %v", end)
	}
	unitEnd := eventByType(journal, "unit_end")
	if len(unitEnd) != 1 || unitEnd[0]["status"] != "done" {
		t.Fatalf("verify pause unit_end = %v", unitEnd)
	}
	if p.callCount() != 1 {
		t.Fatalf("verify pause started another call: %d", p.callCount())
	}
	tui.quit()
}

// TestAcceptanceActivityPageSplitRune covers a page boundary inside a
// multibyte rune: the raw window is not valid UTF-8, and the rendered page is
// repaired instead of showing a broken byte.
func TestAcceptanceActivityPageSplitRune(t *testing.T) {
	log := filepath.Join(t.TempDir(), "call.out")
	prefix := strings.Repeat("a", activityPageSize-1) + "é" + "tail"
	if err := os.WriteFile(log, []byte(prefix), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := agentio.ActivityRef{Stream: "stdout", Offset: 0, Length: -1}
	first, _, err := agentio.ReadActivityPage(log, ref, 0, activityPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if utf8.ValidString(first) {
		t.Fatal("the page boundary did not split the rune; the test no longer exercises the repair")
	}
	clean := Sanitize(first)
	if !utf8.ValidString(clean) || !strings.ContainsRune(clean, '\uFFFD') {
		t.Fatalf("the split rune was not repaired: %q", clean)
	}
	second, _, err := agentio.ReadActivityPage(log, ref, activityPageSize, activityPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(Sanitize(second)) {
		t.Fatalf("the page starting inside the rune was not repaired: %q", Sanitize(second))
	}
}
