//go:build windows

package procgroup

import (
	"os"
	"os/exec"
	"strconv"
	"time"
)

// StopSignals are the signals that ask docflow to stop a running child.
var StopSignals = []os.Signal{os.Interrupt}

// Detach is a no-op on Windows.
func Detach(cmd *exec.Cmd) {}

// Kill ends cmd and its descendants with taskkill /T /F, falls back to
// killing cmd alone when taskkill fails, and waits up to grace for done, which
// must deliver the result of cmd.Wait.
func Kill(cmd *exec.Cmd, done <-chan error, grace time.Duration) error {
	pid := strconv.Itoa(cmd.Process.Pid)
	if err := exec.Command("taskkill", "/T", "/F", "/PID", pid).Run(); err != nil {
		_ = cmd.Process.Kill()
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		_ = cmd.Process.Kill()
		return <-done
	}
}

// Reraise ends the process after a stop signal.
func Reraise(sig os.Signal) { os.Exit(1) }
