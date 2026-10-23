//go:build !windows

package cycle

import "os/exec"

func verifyShell(command string) *exec.Cmd {
	return execCommand("/bin/sh", "-c", command)
}
