package agentio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	KillGrace         = 10 * time.Second
	LimitWait         = 1800
	LimitTries        = 10
	ReplanUnitLimit   = 2
	PrereqTimeout     = 60 * time.Second
	QuotaProbeTimeout = 30 * time.Second
	LimitPrompt       = "Runner: the previous run stopped on a usage limit. Continue the unit.\n"
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
}

type CallOptions struct {
	Timeout time.Duration
	OnEvent func(map[string]any)
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
}

func (w *eventWriter) Write(p []byte) (int, error) {
	w.all.Write(p)
	if w.callback != nil {
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
			w.event(w.pending[:i])
			w.pending = w.pending[i+width:]
		}
	}
	return len(p), nil
}
func (w *eventWriter) flush() {
	if w.callback != nil && w.pending != "" {
		w.event(w.pending)
		w.pending = ""
	}
}
func (w *eventWriter) event(line string) {
	if obj := ParseLine(textOutput(line)); obj != nil {
		func() { defer func() { _ = recover() }(); w.callback(Event{obj, textOutput(line)}) }()
	}
}

var shellSafe = regexp.MustCompile(`^[\w@%+=:,./-]+$`)

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
	procgroup.Detach(cmd)
	out := &eventWriter{}
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
	if err := cmd.Start(); err != nil {
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
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	var waitErr error
	var canceled bool
	select {
	case waitErr = <-done:
	case <-deadline:
		run.TimedOut = true
		waitErr = procgroup.Kill(cmd, done, KillGrace)
	case <-ctx.Done():
		canceled = true
		waitErr = procgroup.Kill(cmd, done, KillGrace)
	}
	out.flush()
	run.Code = exitCode(cmd, waitErr)
	run.Stdout = textOutput(out.all.String())
	run.Stderr = textOutput(stderr.String())
	timed := ""
	if run.TimedOut {
		timed = " (timed out)"
	}
	content := fmt.Sprintf("$ %s\n--- exit %d%s\n--- stdout\n%s\n--- stderr\n%s", shellJoin(argv), run.Code, timed, run.Stdout, run.Stderr)
	if log != "" {
		if err := os.WriteFile(log, []byte(content), 0o644); err != nil {
			return run, err
		}
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
