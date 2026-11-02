//go:build darwin || linux

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// terminal runs docflow tui in a real PTY, reads its rendered output and
// writes keys. It is the same terminal contract the tui package tests use.
type terminal struct {
	cmd      *exec.Cmd
	master   *os.File
	slave    *os.File
	mu       sync.Mutex
	screen   bytes.Buffer
	readDone chan struct{}
}

func startTerminal(bin, dir string, env []string) (*terminal, error) {
	master, slave, err := openPTY()
	if err != nil {
		return nil, err
	}
	if err := setWinsize(slave, 120, 40); err != nil {
		master.Close()
		slave.Close()
		return nil, err
	}
	cmd := exec.Command(bin, "tui")
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		master.Close()
		slave.Close()
		return nil, err
	}
	// The slave belongs to the child now; the parent keeps only the master.
	_ = slave.Close()
	t := &terminal{cmd: cmd, master: master, readDone: make(chan struct{})}
	go t.read()
	return t, nil
}

func (t *terminal) read() {
	defer close(t.readDone)
	buf := make([]byte, 4096)
	for {
		n, err := t.master.Read(buf)
		if n > 0 {
			t.mu.Lock()
			t.screen.Write(buf[:n])
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (t *terminal) PID() int { return t.cmd.Process.Pid }

func (t *terminal) Output() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.screen.String()
}

func (t *terminal) Send(keys string) {
	_, _ = t.master.WriteString(keys)
	time.Sleep(40 * time.Millisecond)
}

func (t *terminal) WaitFor(text string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(t.Output(), text) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("terminal output does not contain " + strconv.Quote(text))
}

func (t *terminal) WaitExit(timeout time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- t.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return err
		}
		return nil
	case <-time.After(timeout):
		_ = t.cmd.Process.Kill()
		return errors.New("terminal did not exit")
	}
}

func (t *terminal) Close() {
	if t.cmd.Process != nil {
		// A driver error can leave the runner and its agent alive. Kill the
		// session's descendants as well as the workspace process, so a failed
		// scenario never leaves an agent behind.
		for _, proc := range descendants(t.cmd.Process.Pid) {
			_ = syscall.Kill(proc.PID, syscall.SIGKILL)
		}
		_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
		_ = t.cmd.Process.Kill()
	}
	_ = t.master.Close()
	select {
	case <-t.readDone:
	case <-time.After(time.Second):
	}
}

// processTable reads pid, ppid and the command line of every live process.
func processTable() ([]processInfo, map[int]int) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil, nil
	}
	var procs []processInfo
	parents := map[int]int{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		procs = append(procs, processInfo{PID: pid, Command: strings.Join(fields[2:], " ")})
		parents[pid] = ppid
	}
	return procs, parents
}

func descendants(root int) []processInfo {
	procs, parents := processTable()
	children := map[int][]processInfo{}
	for _, proc := range procs {
		children[parents[proc.PID]] = append(children[parents[proc.PID]], proc)
	}
	var out []processInfo
	queue := []int{root}
	seen := map[int]bool{root: true}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range children[pid] {
			if seen[child.PID] {
				continue
			}
			seen[child.PID] = true
			out = append(out, child)
			queue = append(queue, child.PID)
		}
	}
	return out
}

func agentProcesses(rootPID int, agent string) []processInfo {
	var out []processInfo
	for _, proc := range descendants(rootPID) {
		for _, field := range strings.Fields(proc.Command) {
			base := filepath.Base(strings.Trim(field, `"'`))
			if base == agent || strings.HasPrefix(base, agent+".") {
				out = append(out, proc)
				break
			}
		}
	}
	return out
}
