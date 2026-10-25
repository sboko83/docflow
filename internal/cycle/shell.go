package cycle

import "os/exec"

// PlanShell returns the argv that runs a plan command (`Verify:`, `gate:`,
// `preflight:`, prerequisites): the shell and its flag, then the command.
func PlanShell(command string) []string {
	return append(planShell(), command)
}

func verifyShell(command string) *exec.Cmd {
	argv := PlanShell(command)
	return execCommand(argv[0], argv[1:]...)
}
