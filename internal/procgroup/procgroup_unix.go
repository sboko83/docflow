//go:build darwin || linux

package procgroup

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// StopSignals are the signals that ask docflow to stop a running child.
var StopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// Detach starts cmd in a new session without a controlling terminal, so the
// child cannot take over the terminal from docflow (an interactive shell would
// otherwise make itself the foreground group and stop docflow with SIGTTIN).
func Detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// Kill sends SIGTERM to the whole group of cmd, waits up to grace for done,
// then sends SIGKILL. done must deliver the result of cmd.Wait.
func Kill(cmd *exec.Cmd, done <-chan error, grace time.Duration) error {
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EPERM) {
		return <-done
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	return <-done
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
