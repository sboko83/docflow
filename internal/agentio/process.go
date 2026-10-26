package agentio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/procgroup"
)

const (
	KillGrace = 10 * time.Second
	// PipeGrace bounds how long a call waits for the agent's stdout and stderr
	// to close after the agent exits: a descendant that escaped the kill (a
	// daemon reparented before the process snapshot) may keep them open.
	PipeGrace         = 5 * time.Second
	LimitWait         = 1800
	LimitTries        = 10
	ReplanUnitLimit   = 2
	PrereqTimeout     = 60 * time.Second
	QuotaProbeTimeout = 30 * time.Second
	RefusalPrompt     = "Runner: the previous run stopped on a provider refusal. Continue the unit.\n"
	RunnerEnv         = "DOCFLOW_RUNNER_UNIT"
)

var RunnerOnly = []string{"finish", "done", "commit", "block"}

type AgentError struct{ clierrors.DocflowError }

func (AgentError) ExitCode() int { return clierrors.EXIT_AGENT }

func RunDir(root string) (string, error) {
	dir, err := cycle.StateDir(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(dir), "run"), nil
}

type AgentRun struct {
	Code           int
	Stdout, Stderr string
	TimedOut       bool
	Stalled        bool
}

type CallOptions struct {
	Timeout time.Duration
	// Idle stops the call as stalled when no stdout line arrives for this
	// long and no tool is in flight (Liveness tracks both). Zero disables
	// the check.
	Idle     time.Duration
	Liveness *Liveness
	OnEvent  func(map[string]any)
	// OnStreamEvent retains JSON object order for StreamEvent quota decoding.
	OnStreamEvent func(Event)
	// A nil Env inherits the environment; an empty slice supplies no variables.
	Env []string
	// Context lets the runner's signal handler cancel and reap the process group.
	Context context.Context
}

// textOutput implements Python's UTF-8 replacement and universal newlines.
func textOutput(raw string) string {
	var b strings.Builder
	for len(raw) > 0 {
		r, size := utf8.DecodeRuneInString(raw)
		if r == utf8.RuneError && size == 1 {
			// Python replaces an incomplete but otherwise valid sequence once,
			// while each invalid leading/continuation byte gets its own U+FFFD.
			want := 1
			first := raw[0]
			switch {
			case first >= 0xc2 && first <= 0xdf:
				want = 2
			case first >= 0xe0 && first <= 0xef:
				want = 3
			case first >= 0xf0 && first <= 0xf4:
				want = 4
			}
			for size < want && size < len(raw) {
				next := raw[size]
				if next < 0x80 || next > 0xbf {
					break
				}
				if size == 1 && (first == 0xe0 && next < 0xa0 || first == 0xed && next > 0x9f || first == 0xf0 && next < 0x90 || first == 0xf4 && next > 0x8f) {
					break
				}
				size++
			}
		}
		b.WriteRune(r)
		raw = raw[size:]
	}
	return strings.ReplaceAll(strings.ReplaceAll(b.String(), "\r\n", "\n"), "\r", "\n")
}

type eventWriter struct {
	all      bytes.Buffer
	pending  string
	callback func(Event)
	observe  func(string)
	log      io.Writer
}

func (w *eventWriter) Write(p []byte) (int, error) {
	w.all.Write(p)
	if w.log != nil {
		w.log.Write(p)
	}
	if w.callback != nil || w.observe != nil {
		w.pending += string(p)
		for {
			i := strings.IndexAny(w.pending, "\r\n")
			if i < 0 {
				break
			}
			width := 1
			if w.pending[i] == '\r' {
				// Hold a trailing CR until the next read distinguishes CRLF.
				if i+1 == len(w.pending) {
					break
				}
				if w.pending[i+1] == '\n' {
					width = 2
				}
			}
			w.line(w.pending[:i])
			w.pending = w.pending[i+width:]
		}
	}
	return len(p), nil
}
func (w *eventWriter) flush() {
	if (w.callback != nil || w.observe != nil) && w.pending != "" {
		w.line(w.pending)
		w.pending = ""
	}
}

// line handles one complete stdout line. The log and the observer run
// before the event callback, so by the time a caller reacts to an event
// its line is already in the log file.
func (w *eventWriter) line(line string) {
	text := textOutput(line)
	if w.observe != nil {
		func() { defer func() { _ = recover() }(); w.observe(line) }()
	}
	if w.callback != nil {
		if obj := ParseLine(text); obj != nil {
			func() { defer func() { _ = recover() }(); w.callback(Event{obj, text}) }()
		}
	}
}

var shellSafe = regexp.MustCompile(`^[\w@%+=:,./-]+$`)

// LivenessCheck is how often CallAgent samples the idle condition; the
// check itself is a mutex-guarded timestamp comparison.
const LivenessCheck = 2 * time.Second

func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = arg
		if !shellSafe.MatchString(arg) {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'"'"'`) + "'"
		}
	}
	return strings.Join(quoted, " ")
}

func CallAgent(root string, argv []string, stdin *string, log string, options CallOptions) (AgentRun, error) {
	run := AgentRun{}
	if len(argv) == 0 {
		return run, AgentError{clierrors.DocflowError{Message: "run: cannot start: empty command", Code: clierrors.EXIT_AGENT}}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = root
	cmd.Env = options.Env
	cmd.WaitDelay = PipeGrace
	procgroup.Detach(cmd)
	out := &eventWriter{}
	if options.Liveness != nil {
		out.observe = options.Liveness.Observe
	}
	if options.OnEvent != nil || options.OnStreamEvent != nil {
		out.callback = func(event Event) {
			if options.OnStreamEvent != nil {
				func() { defer func() { _ = recover() }(); options.OnStreamEvent(event) }()
			}
			if options.OnEvent != nil {
				options.OnEvent(event.Data)
			}
		}
	}
	var stderr bytes.Buffer
	cmd.Stdout = out
	cmd.Stderr = &stderr
	if stdin != nil {
		cmd.Stdin = strings.NewReader(*stdin)
	}
	// The call log is written while the call runs: the header before the
	// process starts, stdout lines as they arrive, the rest at the end.
	var logFile *os.File
	if log != "" {
		file, err := os.OpenFile(log, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return run, err
		}
		logFile = file
		if _, err := fmt.Fprintf(file, "$ %s\n--- stdout\n", shellJoin(argv)); err != nil {
			logFile.Close()
			return run, err
		}
		out.log = logFile
	}
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return run, AgentError{clierrors.DocflowError{Message: fmt.Sprintf("run: cannot start %s: %s", argv[0], startError(err, argv[0])), Code: clierrors.EXIT_AGENT}}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var timer *time.Timer
	var deadline <-chan time.Time
	if options.Timeout > 0 {
		timer = time.NewTimer(options.Timeout)
		deadline = timer.C
		defer timer.Stop()
	}
	var stalls *time.Ticker
	var stall <-chan time.Time
	if options.Idle > 0 && options.Liveness != nil {
		stalls = time.NewTicker(LivenessCheck)
		stall = stalls.C
		defer stalls.Stop()
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	var waitErr error
	var canceled bool
wait:
	for {
		select {
		case waitErr = <-done:
			break wait
		case <-deadline:
			run.TimedOut = true
			waitErr = procgroup.Kill(cmd, done, KillGrace)
			break wait
		case <-stall:
			// Silence with no tool in flight means a hung agent: no line for
			// the idle limit and nothing running that could still answer.
			if state := options.Liveness.State(); state.Tools == 0 && time.Since(state.LastLine) >= options.Idle {
				run.Stalled = true
				waitErr = procgroup.Kill(cmd, done, KillGrace)
				break wait
			}
		case <-ctx.Done():
			canceled = true
			waitErr = procgroup.Kill(cmd, done, KillGrace)
			break wait
		}
	}
	out.flush()
	run.Code = exitCode(cmd, waitErr)
	run.Stdout = textOutput(out.all.String())
	run.Stderr = textOutput(stderr.String())
	if logFile != nil {
		suffix := ""
		if run.TimedOut {
			suffix = " (timed out)"
		} else if run.Stalled {
			suffix = " (stalled)"
		}
		fmt.Fprintf(logFile, "\n--- stderr\n%s\n--- exit %d%s\n", run.Stderr, run.Code, suffix)
		logFile.Close()
	}
	if canceled {
		return run, ctx.Err()
	}
	return run, nil
}

func startError(err error, argv0 string) string {
	path := argv0
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && pathErr.Op == "chdir" {
		path = pathErr.Path
	}
	if os.IsNotExist(err) || errors.Is(err, exec.ErrNotFound) {
		return fmt.Sprintf("[Errno 2] No such file or directory: %s", pathRepr(path))
	}
	if os.IsPermission(err) {
		return fmt.Sprintf("[Errno 13] Permission denied: %s", pathRepr(path))
	}
	return err.Error()
}

func pathRepr(path string) string {
	quote := "'"
	if strings.Contains(path, "'") && !strings.Contains(path, `"`) {
		quote = `"`
	}
	path = strings.ReplaceAll(path, `\`, `\\`)
	path = strings.ReplaceAll(path, quote, `\`+quote)
	path = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(path, "\n", `\n`), "\r", `\r`), "\t", `\t`)
	return quote + path + quote
}
