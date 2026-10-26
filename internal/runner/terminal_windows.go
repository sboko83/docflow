//go:build windows

package runner

import (
	"io"
	"os"
	"syscall"
)

// isTerminal reports a real console. GetConsoleMode succeeds only on a console
// input or output handle, so a redirected file, a pipe and the NUL device read
// as not a terminal. The fd of a standard stream is the value Windows gave the
// process for it, which is the console handle in a terminal.
func isTerminal(stream any) bool {
	if value, ok := stream.(interface{ IsTerminal() bool }); ok {
		return value.IsTerminal()
	}
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(file.Fd()), &mode) == nil
}

func terminalColumns(stream io.Writer) int { return 80 }

func disableEcho(file *os.File) func() { return func() {} }

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return err == syscall.ERROR_ACCESS_DENIED
	}
	defer syscall.CloseHandle(handle)
	state, err := syscall.WaitForSingleObject(handle, 0)
	if err != nil {
		return true
	}
	return state == syscall.WAIT_TIMEOUT
}
