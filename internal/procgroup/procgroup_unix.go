//go:build darwin || linux

package procgroup

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// StopSignals are the signals that ask docflow to stop a running child.
var StopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// Detach starts cmd in a new session without a controlling terminal, so the
// child cannot take over the terminal from docflow (an interactive shell would
// otherwise make itself the foreground group and stop docflow with SIGTTIN).
func Detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// Track is a no-op on Unix: Kill walks live parent links itself.
func Track(p *os.Process) (*Watch, error) { return nil, nil }

// Watch carries the Windows kill-on-close job; on Unix it is always nil.
type Watch struct{}

// Close is a no-op on Unix.
func (w *Watch) Close() {}

// Kill sends SIGTERM to cmd's process group and every descendant process group,
// waits up to grace for the process tree to stop, then sends SIGKILL to those
// groups and the captured PIDs. done must deliver the result of cmd.Wait.
func Kill(cmd *exec.Cmd, watch *Watch, done <-chan error, grace time.Duration) error {
	pids, groups := descendantTree(cmd.Process.Pid)
	signalGroups(groups, syscall.SIGTERM)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var waitErr error
	doneCh := done
	for {
		if !processTreeAlive(pids, groups) {
			if doneCh != nil {
				return <-doneCh
			}
			return waitErr
		}
		select {
		case waitErr = <-doneCh:
			doneCh = nil
		case <-timer.C:
			signalGroups(groups, syscall.SIGKILL)
			signalPIDs(pids, syscall.SIGKILL)
			if doneCh != nil {
				return <-doneCh
			}
			return waitErr
		case <-ticker.C:
		}
	}
}

type processInfo struct {
	group int
}

// descendantTree snapshots the process table before signaling: once the agent
// exits, launchd or init reparents its children and their ancestry is lost.
func descendantTree(root int) ([]int, []int) {
	pids := map[int]struct{}{root: {}}
	groups := map[int]struct{}{root: {}}
	if output, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,pgid=").Output(); err == nil {
		entries := make(map[int]processInfo)
		children := make(map[int][]int)
		for _, line := range strings.Split(string(output), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 3 {
				continue
			}
			pid, pidErr := strconv.Atoi(fields[0])
			parent, parentErr := strconv.Atoi(fields[1])
			group, groupErr := strconv.Atoi(fields[2])
			if pidErr != nil || parentErr != nil || groupErr != nil || pid <= 0 {
				continue
			}
			entries[pid] = processInfo{group: group}
			children[parent] = append(children[parent], pid)
		}
		queue := []int{root}
		for len(queue) > 0 {
			parent := queue[0]
			queue = queue[1:]
			for _, child := range children[parent] {
				if _, seen := pids[child]; seen {
					continue
				}
				pids[child] = struct{}{}
				queue = append(queue, child)
			}
		}
		for pid := range pids {
			if info, ok := entries[pid]; ok && info.group > 0 {
				groups[info.group] = struct{}{}
			}
		}
	}

	runnerGroup := syscall.Getpgrp()
	delete(groups, runnerGroup)
	pidList := make([]int, 0, len(pids))
	for pid := range pids {
		pidList = append(pidList, pid)
	}
	sort.Ints(pidList)
	groupList := make([]int, 0, len(groups))
	for group := range groups {
		if group > 0 {
			groupList = append(groupList, group)
		}
	}
	sort.Ints(groupList)
	return pidList, groupList
}

func signalGroups(groups []int, sig syscall.Signal) {
	for _, group := range groups {
		_ = syscall.Kill(-group, sig)
	}
}

func signalPIDs(pids []int, sig syscall.Signal) {
	for _, pid := range pids {
		_ = syscall.Kill(pid, sig)
	}
}

func processTreeAlive(pids, groups []int) bool {
	for _, group := range groups {
		err := syscall.Kill(-group, 0)
		if err == nil || errors.Is(err, syscall.EPERM) {
			return true
		}
	}
	for _, pid := range pids {
		err := syscall.Kill(pid, 0)
		if err == nil || errors.Is(err, syscall.EPERM) {
			return true
		}
	}
	return false
}

// Reraise ends the process with sig, as if it had not been caught.
func Reraise(sig os.Signal) {
	signal.Reset(sig)
	if s, ok := sig.(syscall.Signal); ok {
		_ = syscall.Kill(os.Getpid(), s)
		time.Sleep(time.Second)
		os.Exit(128 + int(s))
	}
	os.Exit(1)
}
