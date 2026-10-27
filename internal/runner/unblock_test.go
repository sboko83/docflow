package runner

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/term"
)

// syncBuffer is a bytes.Buffer safe to read while the process under test
// writes to it. It also serves the PTY tests: a terminal run's stdout lands
// here from the child while the test polls it for a prompt.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) contains(needle string) bool {
	return strings.Contains(b.String(), needle)
}

// waitFor fails the test when the needle does not show up within the bound.
func (b *syncBuffer) waitFor(t *testing.T, needle string, within time.Duration) string {
	t.Helper()
	for deadline := time.Now().Add(within); ; {
		text := b.String()
		if strings.Contains(text, needle) {
			return text
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q within %s:\n%s", needle, within, text)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// scriptedStdin is the person at a terminal: it hands out the lines they type
// and blocks while they think, so a read that never returns is real. Every
// scripted line fits in one Read call — bufio's buffer is far longer than the
// lines used here, so no line is split.
type scriptedStdin struct {
	lines  chan string
	after  func() // runs when the last handed-out line leaves Read
	closed bool
}

func newScriptedStdin() *scriptedStdin {
	return &scriptedStdin{lines: make(chan string, 4)}
}

func (s *scriptedStdin) Read(p []byte) (int, error) {
	line, ok := <-s.lines
	if !ok {
		return 0, io.EOF
	}
	if len(p) < len(line) {
		panic("scriptedStdin: line does not fit the read buffer")
	}
	copy(p, line)
	if s.after != nil {
		after := s.after
		s.after = nil
		after()
	}
	return len(line), nil
}

func (s *scriptedStdin) typeLines(lines ...string) {
	s.lines <- strings.Join(lines, "")
}

func (s *scriptedStdin) close() { close(s.lines) }

func (s *scriptedStdin) IsTerminal() bool { return true }

// plainStdin is stdin with no terminal behind it: redirected input, a pipe.
type plainStdin struct{ bytes.Buffer }

func newUnblockProgress(t *testing.T) (*Progress, *syncBuffer) {
	t.Helper()
	out := &syncBuffer{}
	return newProgress(out, out, false), out
}

// recordUnblock replaces the runner's `unblock` call for the test and returns
// the arguments of every call it records.
func recordUnblock(t *testing.T) *[][]string {
	t.Helper()
	calls := &[][]string{}
	previous := unblockCommand
	unblockCommand = func(root string, args ...string) cycle.Result {
		*calls = append(*calls, append([]string{root}, args...))
		return cycle.Result{Code: 0, Stdout: "P001.1.1: unblocked\n"}
	}
	t.Cleanup(func() { unblockCommand = previous })
	return calls
}

// askWithin calls askUnblock and fails the test when it does not return in
// time: a question that keeps reading stdin after a cancellation is exactly the
// defect under test.
func askWithin(t *testing.T, within time.Duration, ctx context.Context, root, uid, needs, reason string, yes bool, p *Progress, input runInput, stdout io.Writer) (bool, bool) {
	t.Helper()
	type result struct{ unblocked, cancelled bool }
	done := make(chan result, 1)
	go func() {
		unblocked, cancelled := askUnblock(ctx, root, uid, needs, reason, yes, p, input, stdout)
		done <- result{unblocked, cancelled}
	}()
	select {
	case got := <-done:
		return got.unblocked, got.cancelled
	case <-time.After(within):
		t.Fatalf("askUnblock did not return within %s", within)
		return false, false
	}
}

const unblockPrompt = "done? [y/N] "

// TestIsTerminalRejectsRedirectedStreams: a regular file, a pipe, the null
// device, a stream with no handle behind it — none of them is a terminal on any
// platform, and the question never reaches the person. The `IsTerminal()` hook
// is the seam that says a stream is a console.
func TestIsTerminalRejectsRedirectedStreams(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(path, []byte("redirected\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	nul, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer nul.Close()
	for _, tc := range []struct {
		name   string
		stream any
	}{
		{name: "regular file", stream: file},
		{name: "pipe", stream: reader},
		{name: "null device", stream: nul},
		{name: "stream with no handle", stream: &plainStdin{}},
		{name: "nil", stream: nil},
	} {
		if term.IsTerminal(tc.stream) {
			t.Errorf("%s reads as a terminal", tc.name)
		}
	}
	if !term.IsTerminal(newScriptedStdin()) {
		t.Fatal("the IsTerminal hook is not honoured")
	}
}

func TestAskUnblockGuards(t *testing.T) {
	for _, tc := range []struct {
		name, needs string
		yes         bool
		terminal    bool
	}{
		{name: "plan block goes to replan", needs: "plan"},
		{name: "classless block has no question", needs: ""},
		{name: "--yes runs unattended", needs: "human", yes: true},
		{name: "redirected stdin has no question", needs: "human", terminal: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := recordUnblock(t)
			var stdin io.Reader
			if tc.terminal {
				stdin = newScriptedStdin()
			} else {
				stdin = &plainStdin{}
			}
			p, out := newUnblockProgress(t)
			unblocked, cancelled := askUnblock(context.Background(), t.TempDir(), "P001.1.1", tc.needs, "нужен вход", tc.yes, p, newRunInput(stdin), out)
			if unblocked || cancelled {
				t.Fatalf("unblocked=%v cancelled=%v", unblocked, cancelled)
			}
			if out.contains(unblockPrompt) || out.contains("note: ") {
				t.Fatalf("asked a question:\n%s", out)
			}
			if len(*calls) != 0 {
				t.Fatalf("unblock ran: %v", *calls)
			}
		})
	}
}

func TestAskUnblockCancels(t *testing.T) {
	root := "root"
	const reason = "нужен вход в аккаунт"
	for _, stage := range []struct {
		name       string
		forbidNote bool
		script     func(t *testing.T, stdin *scriptedStdin, out *syncBuffer, cancel context.CancelFunc)
	}{
		{
			name:       "before the question reads",
			forbidNote: true,
			script: func(t *testing.T, stdin *scriptedStdin, out *syncBuffer, cancel context.CancelFunc) {
				cancel()
			},
		},
		{
			name:       "while waiting for done?",
			forbidNote: true,
			script: func(t *testing.T, stdin *scriptedStdin, out *syncBuffer, cancel context.CancelFunc) {
				out.waitFor(t, unblockPrompt, 5*time.Second)
				cancel()
			},
		},
		{
			name: "while waiting for note:",
			script: func(t *testing.T, stdin *scriptedStdin, out *syncBuffer, cancel context.CancelFunc) {
				out.waitFor(t, unblockPrompt, 5*time.Second)
				stdin.typeLines("y\n")
				out.waitFor(t, "note: ", 5*time.Second)
				cancel()
			},
		},
		{
			name: "before the unblock side effect",
			script: func(t *testing.T, stdin *scriptedStdin, out *syncBuffer, cancel context.CancelFunc) {
				out.waitFor(t, unblockPrompt, 5*time.Second)
				stdin.typeLines("y\n")
				out.waitFor(t, "note: ", 5*time.Second)
				// the note lands as the run is cancelled: the answer is real,
				// the context is not, and `unblock` must not run
				stdin.after = cancel
				stdin.typeLines("вошёл в аккаунт\n")
			},
		},
	} {
		t.Run(stage.name, func(t *testing.T) {
			calls := recordUnblock(t)
			p, out := newUnblockProgress(t)
			stdin := newScriptedStdin()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go stage.script(t, stdin, out, cancel)
			unblocked, cancelled := askWithin(t, 5*time.Second, ctx, root, "P001.1.1", "human", reason, false, p, newRunInput(stdin), out)
			if unblocked || !cancelled {
				t.Fatalf("unblocked=%v cancelled=%v:\n%s", unblocked, cancelled, out)
			}
			if len(*calls) != 0 {
				t.Fatalf("unblock ran after cancellation: %v", *calls)
			}
			if !utf8.ValidString(out.String()) {
				t.Fatalf("the output is not valid UTF-8:\n%s", out)
			}
			leaked := []string{"unblocked"}
			if stage.forbidNote {
				leaked = append(leaked, "note: ")
			}
			for _, text := range leaked {
				if strings.Contains(out.String(), text) {
					t.Fatalf("the question kept going and printed %q:\n%s", text, out)
				}
			}
			if !strings.Contains(out.String(), "P001.1.1 is blocked (needs human): "+reason) {
				t.Fatalf("the reason is missing:\n%s", out)
			}
		})
	}
}

func TestAskUnblockUnblocksOnAnswer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		note  string
	}{
		{name: "note", lines: []string{"y\n", "вошёл в аккаунт\n"}, note: "вошёл в аккаунт"},
		{name: "empty note keeps the default text", lines: []string{"y\n", "\n"}, note: ""},
		{name: "yes", lines: []string{"yes\n", "готово\n"}, note: "готово"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := recordUnblock(t)
			p, out := newUnblockProgress(t)
			stdin := newScriptedStdin()
			stdin.typeLines(tc.lines...)
			unblocked, cancelled := askWithin(t, 5*time.Second, context.Background(), "root", "P001.1.1", "human", "нужен вход", false, p, newRunInput(stdin), out)
			if !unblocked || cancelled {
				t.Fatalf("unblocked=%v cancelled=%v:\n%s", unblocked, cancelled, out)
			}
			if len(*calls) != 1 || strings.Join((*calls)[0], " ") != "root unblock P001.1.1 --note="+tc.note {
				t.Fatalf("unblock call = %v", *calls)
			}
			if !out.contains("P001.1.1: unblocked") {
				t.Fatalf("no unblocked line:\n%s", out)
			}
		})
	}
}

func TestAskUnblockRefusalAndClosedStdin(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		close bool
	}{
		{name: "n", lines: []string{"n\n"}},
		{name: "empty answer", lines: []string{"\n"}},
		{name: "stdin closes", close: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := recordUnblock(t)
			p, out := newUnblockProgress(t)
			stdin := newScriptedStdin()
			if tc.close {
				stdin.close()
			} else {
				stdin.typeLines(tc.lines...)
			}
			unblocked, cancelled := askWithin(t, 5*time.Second, context.Background(), "root", "P001.1.1", "human", "нужен вход", false, p, newRunInput(stdin), out)
			if unblocked || cancelled {
				t.Fatalf("unblocked=%v cancelled=%v", unblocked, cancelled)
			}
			if len(*calls) != 0 {
				t.Fatalf("unblock ran: %v", *calls)
			}
			if !out.contains(unblockPrompt) {
				t.Fatalf("no question:\n%s", out)
			}
		})
	}
}

func TestAskUnblockFailureStopsTheRun(t *testing.T) {
	previous := unblockCommand
	unblockCommand = func(string, ...string) cycle.Result {
		return cycle.Result{Code: 5, Stderr: "P001.1.1 is blocked (needs plan): replan it with bsv-docflow-plan\n"}
	}
	t.Cleanup(func() { unblockCommand = previous })
	p, out := newUnblockProgress(t)
	stdin := newScriptedStdin()
	stdin.typeLines("y\n", "готово\n")
	unblocked, cancelled := askWithin(t, 5*time.Second, context.Background(), "root", "P001.1.1", "human", "нужен вход", false, p, newRunInput(stdin), out)
	if unblocked || cancelled {
		t.Fatalf("unblocked=%v cancelled=%v", unblocked, cancelled)
	}
	if !out.contains("needs plan") {
		t.Fatalf("the failure is not reported:\n%s", out)
	}
}

// TestRunInputKeepsBufferedLines: the person pastes the answer, the note and
// the start confirmation as one line. The question takes its two lines and
// leaves the rest in the shared reader — the start confirmation reads them
// from the same reader, so no second reader swallows the input.
func TestRunInputKeepsBufferedLines(t *testing.T) {
	calls := recordUnblock(t)
	p, out := newUnblockProgress(t)
	stdin := newScriptedStdin()
	stdin.typeLines("y\nвошёл в аккаунт\ny\n")
	input := newRunInput(stdin)
	unblocked, cancelled := askWithin(t, 5*time.Second, context.Background(), "root", "P001.1.1", "human", "нужен вход", false, p, input, out)
	if !unblocked || cancelled {
		t.Fatalf("unblocked=%v cancelled=%v:\n%s", unblocked, cancelled, out)
	}
	if len(*calls) != 1 || strings.Join((*calls)[0], " ") != "root unblock P001.1.1 --note=вошёл в аккаунт" {
		t.Fatalf("unblock call = %v", *calls)
	}
	line, err := input.reader.ReadString('\n')
	if err != nil || line != "y\n" {
		t.Fatalf("the next prompt loses the typed answer: %q %v", line, err)
	}
}
