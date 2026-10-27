//go:build windows

package runner

import (
	"io"
	"os"
	"syscall"
)

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
