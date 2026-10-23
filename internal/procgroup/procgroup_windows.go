//go:build windows

package procgroup

import (
	"os"
	"os/exec"
	"time"
)

// StopSignals are the signals that ask docflow to stop a running child.
var StopSignals = []os.Signal{os.Interrupt}

// Detach is a no-op on Windows.
func Detach(cmd *exec.Cmd) {}

// Kill kills cmd and waits for done.
func Kill(cmd *exec.Cmd, done <-chan error, grace time.Duration) error {
	_ = cmd.Process.Kill()
	return <-done
}

// Reraise ends the process after a stop signal.
func Reraise(sig os.Signal) { os.Exit(1) }
