//go:build darwin || linux

package agentio

import (
	"os/exec"
	"syscall"
)

func exitCode(cmd *exec.Cmd, err error) int {
	if err == nil {
		return 0
	}
	if s, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && s.Signaled() {
		return -int(s.Signal())
	}
	return cmd.ProcessState.ExitCode()
}
