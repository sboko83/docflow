//go:build windows

package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/term"
	"golang.org/x/sys/windows"
)

// The host runs in a subprocess: clearing its inherited standard handles must
// never change the test runner's handles. The child gets real ConPTY handles.
func TestWindowsConsoleProgress(t *testing.T) {
	switch os.Getenv("DOCFLOW_TEST_CONSOLE_STAGE") {
	case "host":
		os.Exit(consoleProgressHost())
	case "child":
		checkConsoleProgress(t)
		return
	}
	for _, width := range []int{120, 30} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWindowsConsoleProgress$", "-test.v")
			cmd.Env = append(os.Environ(), "DOCFLOW_TEST_CONSOLE_STAGE=host", fmt.Sprintf("DOCFLOW_TEST_CONSOLE_WIDTH=%d", width))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("ConPTY: %v\n%s", err, output)
			}
			if !bytes.Contains(output, []byte("PASS")) {
				t.Fatalf("child did not pass:\n%s", output)
			}
		})
	}
}

func consoleProgressHost() int {
	fail := func(err error) int { fmt.Fprintln(os.Stderr, err); return 1 }
	input, writer, err := os.Pipe()
	if err != nil {
		return fail(err)
	}
	defer input.Close()
	defer writer.Close()
	reader, output, err := os.Pipe()
	if err != nil {
		return fail(err)
	}
	defer reader.Close()
	defer output.Close()
	width, _ := strconv.Atoi(os.Getenv("DOCFLOW_TEST_CONSOLE_WIDTH"))
	var console windows.Handle
	if err := windows.CreatePseudoConsole(windows.Coord{X: int16(width), Y: 40}, windows.Handle(input.Fd()), windows.Handle(output.Fd()), 0, &console); err != nil {
		return fail(err)
	}
	defer func() {
		if console != 0 {
			windows.ClosePseudoConsole(console)
		}
	}()
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return fail(err)
	}
	defer attrs.Delete()
	// This API takes the HPCON value, not the address of the handle variable.
	// Reinterpret its bits without uintptr-to-pointer arithmetic: HPCON is an
	// opaque Windows value and must never be dereferenced by Go.
	consoleValue := *(*unsafe.Pointer)(unsafe.Pointer(&console))
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, consoleValue, unsafe.Sizeof(console)); err != nil {
		return fail(err)
	}
	var startup windows.StartupInfoEx
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.ProcThreadAttributeList = attrs.List()
	exe, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	commandText := os.Getenv("DOCFLOW_TEST_CONSOLE_COMMAND")
	if commandText == "" {
		commandText = windows.ComposeCommandLine([]string{exe, "-test.run=^TestWindowsConsoleProgress$", "-test.v"})
	}
	command, err := windows.UTF16PtrFromString(commandText)
	if err != nil {
		return fail(err)
	}
	if err := os.Setenv("DOCFLOW_TEST_CONSOLE_STAGE", "child"); err != nil {
		return fail(err)
	}
	for _, slot := range []uint32{windows.STD_INPUT_HANDLE, windows.STD_OUTPUT_HANDLE, windows.STD_ERROR_HANDLE} {
		if err := windows.SetStdHandle(slot, 0); err != nil {
			return fail(err)
		}
	}
	var process windows.ProcessInformation
	if err := windows.CreateProcess(nil, command, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT, nil, nil, &startup.StartupInfo, &process); err != nil {
		return fail(err)
	}
	defer windows.CloseHandle(process.Process)
	defer windows.CloseHandle(process.Thread)
	input.Close()
	output.Close()
	drained := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(reader); drained <- data }()
	status, err := windows.WaitForSingleObject(process.Process, 40000)
	if err != nil || status == uint32(windows.WAIT_TIMEOUT) {
		_ = windows.TerminateProcess(process.Process, 124)
	}
	var code uint32
	_ = windows.GetExitCodeProcess(process.Process, &code)
	windows.ClosePseudoConsole(console)
	console = 0
	os.Stdout.Write(<-drained)
	return int(code)
}

// Exercise the whole run/close cycle as well as the renderer checks above.
// ConPTY rewrites ANSI and cursor movement, so check semantic output here;
// exact emitted line widths and frame boundaries are checked by the child.
func TestWindowsConsoleRun(t *testing.T) {
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "")
	f := readyCloseFixture(t)
	f.configure("stream = \"claude\"\ntimeout = 4\n")
	tick := emit(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "tick"}}}})
	active := emit(map[string]any{"type": "system", "subtype": "init", "session_id": "s1"}) + tick + "sleep 1\n" + tick + "sleep 60\n"
	f.steps(active, closeArchiveStep(f.binary)+quotaEvent(.86, .6, 1791904200)+doneReply())
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWindowsConsoleProgress$")
	cmd.Dir = f.root
	cmd.Env = append(os.Environ(), "DOCFLOW_TEST_CONSOLE_STAGE=host", "DOCFLOW_TEST_CONSOLE_WIDTH=120",
		"DOCFLOW_TEST_CONSOLE_COMMAND="+windows.ComposeCommandLine([]string{f.binary, "run", "--no-wait", "--config", f.config, "--close", "--attempts", "2"}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("console run: %v\n%s", err, output)
	}
	text := regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\a]*\a)`).ReplaceAllString(string(output), "")
	for _, want := range []string{"P001: close plan", "close attempt 1: time cap 4s reached while active", "resuming the session", "Quota · used", "86%", "hint: P001 needs more than 4s"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	if len(f.calls()) != 2 {
		t.Fatalf("close did not resume exactly once: %d calls", len(f.calls()))
	}
}

type consoleCapture struct {
	io.Writer
}

func (consoleCapture) IsTerminal() bool { return true }

func checkConsoleProgress(t *testing.T) {
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "")
	width, _ := strconv.Atoi(os.Getenv("DOCFLOW_TEST_CONSOLE_WIDTH"))
	if !term.IsTerminal(os.Stdout) || terminalColumns(os.Stdout) != width {
		t.Fatalf("not a %d-column console", width)
	}
	handle := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		t.Fatal(err)
	}
	defer windows.SetConsoleMode(handle, mode)
	if err := windows.SetConsoleMode(handle, mode&^windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		t.Fatal(err)
	}
	for _, left := range []bool{false, true} {
		p := newProgress(os.Stdout, os.Stdout, false)
		var enabled uint32
		if err := windows.GetConsoleMode(handle, &enabled); err != nil || enabled&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING == 0 {
			t.Fatalf("VT mode not enabled: %v", err)
		}
		// Capture exactly what is also written to the real console. Keep the
		// constructor's width closure, so an 80-column stub fails at 30/120.
		var raw bytes.Buffer
		capture := consoleCapture{io.MultiWriter(os.Stdout, &raw)}
		p.out, p.err = capture, capture
		display, percent := agents.QuotaUsed, "86%"
		if left {
			display, percent = agents.QuotaLeft, "14%"
		}
		p.setDisplay(agents.Display{Quota: display, Resets: !left})
		p.unit("P001.1.1", "Console output", agents.Route{Agent: "claude"}, time.Now())
		p.label = "P001.1.1 · turn 1 · 2s ago · tool Bash · 1s"
		p.drawLocked("⠋")
		p.out.Write([]byte("\r\x1b[K"))
		quota := []NamedQuota{{Name: "claude", Quota: agentio.Quota{{Label: "five_hour", Used: .86, Reset: 1791904200}, {Label: "seven_day", Used: .60, Reset: 1792476000}}}}
		p.quotas(quota, nil, "")
		p.unitEnd("P001.1.1")
		p.unit("P001", "close plan", agents.Route{Agent: "claude"}, time.Now())
		p.resume("close attempt 1: time cap 4s reached while active (2 turns); resuming the session")
		p.quotas(quota, nil, "")
		p.close()
		text := strings.ReplaceAll(ansiControl.ReplaceAllString(raw.String(), ""), "\r", "\n")
		for _, line := range strings.Split(text, "\n") {
			if len([]rune(line)) > width-1 {
				t.Errorf("line exceeds %d columns: %q", width, line)
			}
			if strings.ContainsAny(line, "█░↻") && !strings.HasPrefix(line, "│") {
				t.Errorf("outside frame: %q", line)
			}
		}
		if !strings.Contains(text, percent) || regexp.MustCompile(`↻ (?:[A-Z][a-z]{2} )?\d\d:\d\d`).MatchString(text) == left {
			t.Fatalf("quota mode/reset: %s", text)
		}
		if width == 30 && !left && !regexp.MustCompile(`(?m)^│ +↻ [A-Z][a-z]{2} \d\d:\d\d$`).MatchString(text) {
			t.Fatalf("weekly reset did not move below the bar: %s", text)
		}
		if !strings.HasSuffix(text, "└─\n") || !frameEnd.MatchString(text) {
			t.Fatalf("missing frame end: %s", text)
		}
		if len(spinnerLabels(raw.String())) != 1 {
			t.Fatalf("missing spinner: %s", text)
		}
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if term.IsTerminal(writer) || terminalColumns(writer) != 80 {
		t.Fatal("redirected pipe inherits console properties")
	}
	term.EnableVTProcessing(writer)
}
