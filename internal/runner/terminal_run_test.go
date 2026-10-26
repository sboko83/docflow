//go:build darwin || linux

package runner

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestTerminalRunRealTTY(t *testing.T) {
	f := newRunFixture(t)
	f.configure("stream = \"claude\"\n")
	note := "Физические Apple Watch не проверялись."
	f.steps("echo not-json\n", "echo a > a.txt\n"+quotaEvent(.86, .6, 1791904200)+doneReply(map[string]any{"unverified": []string{note}}), quotaEvent(.97, .62, 1791904200)+envelope(map[string]any{"status": "blocked", "reason": "нет файла"}))
	code, text, _ := runInTerminal(t, f, "--config", f.config)
	if code != 3 {
		t.Fatalf("exit %d: %s", code, text)
	}
	for _, s := range []string{"┌─ P001.1.1", "! attempt 1:", "✓ Done:", "! Unverified:", note, "! Blocked (needs human):\n│    нет файла", "Quota · used · Δ run", "97% (+11)", "1 unverified"} {
		if !strings.Contains(text, s) {
			t.Errorf("missing %q:\n%s", s, text)
		}
	}
	if strings.Count(text, "│  Time: ") != 2 || !strings.HasSuffix(text, "╰─\n") {
		t.Fatal(text)
	}
}

func TestTerminalReplanRealTTY(t *testing.T) {
	f := newReplanFixture(t)
	f.steps(blockReply(), liftReply("P001.1.1"), writeReply("b.txt"), writeReply("d.txt"))
	code, text, raw := runInTerminal(t, f)
	if code != 0 || strings.Count(text, "│  Time: ") != 4 {
		t.Fatalf("exit %d, missing unit/replan durations:\n%s", code, text)
	}
	if !strings.Contains(text, "↻ Replanned:") || !strings.Contains(raw, "\x1b[1;97;48;2;193;95;60m Claude ") {
		t.Fatalf("replan status or white Claude label missing:\n%s", raw)
	}
}

func runInTerminal(t *testing.T, f *runFixture, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "")
	master, slave := openPTY(t)
	c := f.command(append([]string{"run", "--no-wait"}, args...)...)
	c.Stdout, c.Stderr = slave, slave
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	done := make(chan string, 1)
	go func() { b, _ := io.ReadAll(master); done <- string(b) }()
	finished := make(chan error, 1)
	go func() { finished <- c.Wait() }()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		_ = c.Process.Kill()
		<-finished
		t.Fatal("TTY run timed out")
	}
	var output string
	select {
	case output = <-done:
	case <-time.After(3 * time.Second):
		master.Close()
		output = <-done
	}
	text := strings.ReplaceAll(regexp.MustCompile(`\x1b\[[\d;]*[mK]`).ReplaceAllString(output, ""), "\r", "")
	return c.ProcessState.ExitCode(), text, output
}

func TestTerminalUnblockAtRunStart(t *testing.T) {
	note := "вошёл в аккаунт"
	for _, mode := range []string{"yes", "empty", "no", "plan"} {
		t.Run(mode, func(t *testing.T) {
			f := newRunFixture(t)
			needs, keys, want := "human", "y\n"+note+"\n", 0
			if mode == "empty" {
				keys = "y\n\n"
			}
			if mode == "no" {
				keys, want = "n\n", 3
			}
			if mode == "plan" {
				needs, keys, want = "plan", "y\n", 3
			}
			f.blockUnit(needs)
			f.steps(writeReply("a.txt"), writeReply("b.txt"))
			code, out, tty := runInTerminalWithInput(t, f, keys, "--max-units", "1")
			if code != want {
				t.Fatalf("exit %d, want %d\n%s%s", code, want, out, tty)
			}
			asked := strings.Contains(out, "done? [y/N] ")
			if (mode != "plan") != asked {
				t.Fatalf("%q asked=%v:\n%s", mode, asked, out)
			}
			if mode == "plan" {
				if !strings.Contains(out, "P001.1.1 is blocked") || strings.Contains(out, "unblock:") {
					t.Fatal(out)
				}
			}
			if mode == "no" && !strings.Contains(out, `unblock: docflow unblock P001.1.1 --note "…", then docflow run`) {
				t.Fatal(out)
			}
			plan := f.read(planFile)
			if mode == "yes" {
				if !strings.Contains(out, "P001.1.1: unblocked") || !strings.Contains(tty, note) {
					t.Fatalf("note is not visible in the terminal:\n%s\n%s", out, tty)
				}
				if !strings.Contains(plan, "- Unblocked: "+note+"\n") || !strings.Contains(plan, "[x] P001.1.1") {
					t.Fatal(plan)
				}
			}
			if mode == "empty" && !strings.Contains(plan, "- Unblocked: the person reports the blocked step done\n") {
				t.Fatal(plan)
			}
			if (mode == "no" || mode == "plan") && (strings.Contains(plan, "Unblocked:") || !strings.Contains(plan, "[!] P001.1.1")) {
				t.Fatal(plan)
			}
			if calls := len(f.calls()); calls != map[string]int{"yes": 1, "empty": 1, "no": 0, "plan": 0}[mode] {
				t.Fatal(calls)
			}
		})
	}
}

// runInTerminalWithInput runs `docflow run` on a PTY, types `keys` as the
// person, and returns the exit code, the captured stdout+stderr and the
// normalized terminal screen — the echo of the typed text lands there.
func runInTerminalWithInput(t *testing.T, f *runFixture, keys string, args ...string) (int, string, string) {
	t.Helper()
	master, slave := openPTY(t)
	c := f.command(append([]string{"run", "--config", f.config, "--no-wait"}, args...)...)
	c.Stdin = slave
	var out, errOut bytes.Buffer
	c.Stdout, c.Stderr = &out, &errOut
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	if _, err := io.WriteString(master, keys); err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() { b, _ := io.ReadAll(master); done <- string(b) }()
	finished := make(chan error, 1)
	go func() { finished <- c.Wait() }()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		_ = c.Process.Kill()
		<-finished
		t.Fatal("TTY run timed out")
	}
	var tty string
	select {
	case tty = <-done:
	case <-time.After(3 * time.Second):
		master.Close()
		tty = <-done
	}
	text := strings.ReplaceAll(regexp.MustCompile(`\x1b\[[\d;]*[mK]`).ReplaceAllString(tty, ""), "\r", "")
	return c.ProcessState.ExitCode(), out.String() + errOut.String(), text
}

// blockUnit turns the fixture's first unit into a blocked one, the way
// `docflow block` writes it: the status plus the Handoff item of the class.
func (f *runFixture) blockUnit(needs string) {
	f.t.Helper()
	f.replace(planFile, "[ ] P001.1.1", "[!] P001.1.1")
	f.replace(planFile, "Commit: P001.1.1: first unit\n", "Commit: P001.1.1: first unit\nHandoff:\n- Blocked ("+needs+"): нужен вход\n")
}

// ttySession is a terminal run that the test drives as the person: it types
// into the PTY, watches the run's output for a prompt, and sends the run a
// signal. The keys go in only when the prompt the test waits for has appeared,
// so a run that never asks — or never stops asking — fails loudly.
type ttySession struct {
	t      *testing.T
	cmd    *exec.Cmd
	master *os.File
	out    syncBuffer
	screen chan string
	exited chan error
}

func startTerminalRun(t *testing.T, f *runFixture, args ...string) *ttySession {
	t.Helper()
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "")
	master, slave := openPTY(t)
	c := f.command(append([]string{"run", "--config", f.config, "--no-wait"}, args...)...)
	c.Stdin = slave
	s := &ttySession{t: t, cmd: c, master: master, screen: make(chan string, 1), exited: make(chan error, 1)}
	c.Stdout, c.Stderr = &s.out, &s.out
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	go func() {
		raw, _ := io.ReadAll(master)
		s.screen <- strings.ReplaceAll(regexp.MustCompile(`\x1b\[[\d;]*[mK]`).ReplaceAllString(string(raw), ""), "\r", "")
	}()
	go func() { s.exited <- c.Wait() }()
	t.Cleanup(func() { _ = s.cmd.Process.Kill() })
	return s
}

// typeKeys types into the terminal. It reports the write error instead of
// failing: after the run has exited, the PTY has no reader left and the write
// answers EIO — which is what a prompt-free stop looks like from the outside.
func (s *ttySession) typeKeys(keys string) error {
	_, err := io.WriteString(s.master, keys)
	return err
}

func (s *ttySession) signal(sig os.Signal) {
	s.t.Helper()
	if err := s.cmd.Process.Signal(sig); err != nil {
		s.t.Fatal(err)
	}
}

// awaitExit reaps the run within the bound: a run that needs a keystroke to
// finish, or that keeps reading stdin after the signal, fails here.
func (s *ttySession) awaitExit(within time.Duration) int {
	s.t.Helper()
	select {
	case <-s.exited:
	case <-time.After(within):
		_ = s.cmd.Process.Kill()
		<-s.exited
		s.t.Fatalf("the run did not exit within %s:\n%s", within, s.out.String())
	}
	return s.cmd.ProcessState.ExitCode()
}

func (s *ttySession) screenText(within time.Duration) string {
	s.t.Helper()
	select {
	case text := <-s.screen:
		return text
	case <-time.After(within):
		_ = s.master.Close()
		select {
		case text := <-s.screen:
			return text
		case <-time.After(3 * time.Second):
			s.t.Fatal("the terminal screen was not captured")
		}
	}
	return ""
}

// TestTerminalUnblockSignalCancels: Ctrl-C at the unblock question ends the run
// with its signal code — no keystroke, within a bounded wait. The late answer
// the person types after that finds no reader, the plan stays byte-for-byte as
// it was and the agent never runs.
func TestTerminalUnblockSignalCancels(t *testing.T) {
	for _, stage := range []struct {
		name     string
		answered bool
		late     string
	}{
		{name: "at done?", late: "y\nвошёл в аккаунт\n"},
		{name: "at note:", answered: true, late: "вошёл в аккаунт\n"},
	} {
		t.Run(stage.name, func(t *testing.T) {
			f := newRunFixture(t)
			f.blockUnit("human")
			f.steps(writeReply("a.txt"), writeReply("b.txt"))
			before := f.read(planFile)
			s := startTerminalRun(t, f, "--max-units", "1")
			s.out.waitFor(t, "done? [y/N] ", 20*time.Second)
			if stage.answered {
				if err := s.typeKeys("y\n"); err != nil {
					t.Fatal(err)
				}
				s.out.waitFor(t, "note: ", 20*time.Second)
			}
			s.signal(syscall.SIGINT)
			// no keystroke: the signal alone ends the run, within the bound
			if code := s.awaitExit(10 * time.Second); code != 128+int(syscall.SIGINT) {
				t.Fatalf("exit %d, want %d:\n%s", code, 128+int(syscall.SIGINT), s.out.String())
			}
			// the person answers a moment too late: the run is gone, so the
			// write finds no reader left on the terminal
			if err := s.typeKeys(stage.late); err == nil {
				t.Fatalf("the terminal still reads stdin after the signal:\n%s", s.out.String())
			}
			text := s.out.String()
			if !strings.Contains(text, "stopped by "+signalName(syscall.SIGINT)) || strings.Count(text, "stopped by ") != 1 {
				t.Fatalf("the signal is not reported once:\n%s", text)
			}
			if strings.Contains(text, "unblocked") {
				t.Fatalf("the run unblocked the unit:\n%s", text)
			}
			if !stage.answered && strings.Contains(text, "note: ") {
				t.Fatalf("the question kept going:\n%s", text)
			}
			plan := f.read(planFile)
			if plan != before {
				t.Fatalf("the plan changed:\n%s\nwas:\n%s", plan, before)
			}
			if strings.Contains(plan, "Unblocked:") || !strings.Contains(plan, "[!] P001.1.1") {
				t.Fatalf("the unit is not blocked:\n%s", plan)
			}
			if calls := len(f.calls()); calls != 0 {
				t.Fatalf("the agent ran %d times", calls)
			}
			if screen := s.screenText(5 * time.Second); stage.answered && !strings.Contains(screen, "y") {
				t.Fatalf("the typed answer is not echoed on the terminal:\n%s", screen)
			}
		})
	}
}

func TestTerminalStartConfirmation(t *testing.T) {
	for _, answer := range []string{"y", "n"} {
		t.Run(answer, func(t *testing.T) {
			f := newRunFixture(t)
			f.replace(planFile, "status: active", "status: active\npermissions: full")
			f.configure("[agents.fake.permissions]\nfull = [\"--no-sandbox\"]\n")
			f.steps(writeReply("a.txt"))
			master, slave := openPTY(t)
			c := f.command("run", "--config", f.config, "--no-wait", "--max-units", "1")
			c.Stdin = slave
			var out, errOut bytes.Buffer
			c.Stdout, c.Stderr = &out, &errOut
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(master, answer+"\n")
			done := make(chan error, 1)
			go func() { done <- c.Wait() }()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				_ = c.Process.Kill()
				<-done
				t.Fatal("confirmation timed out")
			}
			code := 0
			if answer == "n" {
				code = 1
			}
			f.check(runResult{c.ProcessState.ExitCode(), out.String(), errOut.String()}, code, "continue? [y/N]")
			if answer == "y" {
				if len(f.calls()) != 1 || !strings.Contains(strings.Join(f.calls()[0].Argv, " "), "--no-sandbox") {
					t.Fatal(f.calls())
				}
			} else if len(f.calls()) != 0 {
				t.Fatal(f.calls())
			}
		})
	}
}

func TestTerminalSoftStopAndEcho(t *testing.T) {
	f := newRunFixture(t)
	pid := filepath.Join(f.tmp, "pid")
	f.steps("echo $$ > "+shellQuote(pid)+"\nsleep 2\n"+writeReply("a.txt"), writeReply("b.txt"))
	master, slave := openPTY(t)
	c := f.command("run", "--config", f.config, "--no-wait")
	c.Stdin = slave
	var out, errOut bytes.Buffer
	c.Stdout, c.Stderr = &out, &errOut
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, pid)
	_, _ = io.WriteString(master, "s\n")
	time.Sleep(100 * time.Millisecond)
	var state syscall.Termios
	if !ioctl(master.Fd(), termiosGetRequest(), unsafe.Pointer(&state)) {
		t.Fatal("cannot read termios")
	}
	if state.Lflag&syscall.ECHO != 0 {
		t.Error("echo remained enabled")
	}
	_, _ = io.WriteString(master, "s\n")
	if err := c.Wait(); err != nil {
		t.Fatal(err, out.String(), errOut.String())
	}
	if !ioctl(master.Fd(), termiosGetRequest(), unsafe.Pointer(&state)) || state.Lflag&syscall.ECHO == 0 {
		t.Error("echo was not restored")
	}
	f.check(runResult{0, out.String(), errOut.String()}, 0, "stopped on request", "run: 1 unit,")
	if strings.Count(out.String(), "s + Enter received; the plan stops after P001.1.1") != 1 || len(f.calls()) != 1 {
		t.Fatal(out.String())
	}
}

func TestRunStopSignalsKillAgent(t *testing.T) {
	for _, s := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(fmt.Sprint(s), func(t *testing.T) {
			f := newRunFixture(t)
			path := filepath.Join(f.tmp, "pid")
			detachedPath := filepath.Join(f.tmp, "detached-pid")
			f.steps("echo $$ > " + shellQuote(path) + "\n" +
				"perl -e 'use POSIX; setsid(); open(F, \">\", $ARGV[0]) or die $!; print F $$; close F; sleep 300' " + shellQuote(detachedPath) + " &\nwait\n")
			c := f.command("run", "--config", f.config, "--no-wait")
			var out, errOut bytes.Buffer
			c.Stdout, c.Stderr = &out, &errOut
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			waitForFile(t, path)
			waitForFile(t, detachedPath)
			b, _ := os.ReadFile(path)
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			detachedBytes, _ := os.ReadFile(detachedPath)
			detachedPID, _ := strconv.Atoi(strings.TrimSpace(string(detachedBytes)))
			t.Cleanup(func() { _ = syscall.Kill(detachedPID, syscall.SIGKILL) })
			if err := c.Process.Signal(s); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- c.Wait() }()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				_ = c.Process.Kill()
				<-done
				t.Fatal("signal did not stop runner")
			}
			f.check(runResult{c.ProcessState.ExitCode(), out.String(), errOut.String()}, 128+int(s), "stopped by "+signalName(s), "run: 0 units,")
			records := fixtureEvents(t, f)
			end := records[len(records)-1]
			if end["event"] != "run_end" || end["exit_code"] != float64(128+int(s)) || end["reason"] != "stopped by "+signalName(s) {
				t.Fatalf("signal run_end = %v", end)
			}
			if strings.Count(out.String(), "P001.1.1 time: ") != 1 {
				t.Fatalf("interrupted unit did not report duration once:\n%s", out.String())
			}
			if syscall.Kill(pid, 0) != syscall.ESRCH {
				t.Errorf("agent %d survived", pid)
			}
			if !waitRunnerProcessGone(detachedPID) {
				t.Errorf("detached descendant %d survived", detachedPID)
			}
			if !strings.Contains(f.read(planFile), "[~] P001.1.1") {
				t.Fatal("unit status changed")
			}
			if _, err := os.Stat(filepath.Join(f.root, ".git/docflow/run/run.lock")); err == nil {
				t.Fatal("lock remained")
			}
		})
	}
}

func waitRunnerProcessGone(pid int) bool {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestRunMixedInterruptKeepsQuota(t *testing.T) {
	f := mixedRunFixture(t, false)
	pid := filepath.Join(f.tmp, "pid")
	f.steps(quotaEvent(.08, .07, 1791904200)+writeReply("u1.txt"), codexStep(f, 2, 20, 30, true), "echo $$ > "+shellQuote(pid)+"\nexec sleep 60\n")
	c := f.command("run", "--config", f.config, "--no-wait")
	var out, errOut bytes.Buffer
	c.Stdout, c.Stderr = &out, &errOut
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, pid)
	_ = c.Process.Signal(syscall.SIGINT)
	if err := c.Wait(); err == nil {
		t.Fatal("signal returned success")
	}
	f.check(runResult{c.ProcessState.ExitCode(), out.String(), errOut.String()}, 130, "stopped by SIGINT", "run: 2 units,", "cl 5h 8% (+0) · 7d 7% (+0); cx 5h 20% (+0) · 7d 30% (+0)")
}

func TestInterruptedCallKeepsQuota(t *testing.T) {
	f := newRunFixture(t)
	f.configure("stream = \"claude\"\n")
	pidFile := filepath.Join(f.tmp, "pid")
	f.steps(quotaEvent(.23, .41, 1791904200) + "sleep 1\necho $$ > " + shellQuote(pidFile) + "\nexec sleep 60\n")
	c := f.command("run", "--config", f.config, "--no-wait", "--max-units", "1")
	var out, errOut bytes.Buffer
	c.Stdout, c.Stderr = &out, &errOut
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, pidFile)
	if err := c.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		_ = c.Process.Kill()
		<-done
		t.Fatal("signal did not stop runner")
	}
	f.check(runResult{c.ProcessState.ExitCode(), out.String(), errOut.String()}, 130,
		"stopped by SIGINT", "run: 0 units,", "quota: fake 5h 23% · 7d 41%", "fake 5h 23% (+0) · 7d 41% (+0)")
}
