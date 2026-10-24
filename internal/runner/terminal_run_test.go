//go:build darwin || linux

package runner

import (
	"bytes"
	"fmt"
	"io"
	"os"
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
