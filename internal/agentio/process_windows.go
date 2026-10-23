//go:build windows

package agentio

import "os/exec"

func exitCode(cmd *exec.Cmd, err error) int {
	if err == nil {
		return 0
	}
	return cmd.ProcessState.ExitCode()
}
