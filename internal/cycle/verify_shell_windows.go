//go:build windows

package cycle

import (
	"os"
	"os/exec"
)

func verifyShell(command string) *exec.Cmd {
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	return execCommand(shell, "/c", command)
}
