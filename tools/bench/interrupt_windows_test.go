//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// prepareInterrupt starts c in its own process group, the only kind of
// target GenerateConsoleCtrlEvent can address with CTRL_BREAK.
func prepareInterrupt(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// sendInterrupt delivers CTRL_BREAK, which Go reports as os.Interrupt.
func sendInterrupt(c *exec.Cmd) error {
	dll, err := syscall.LoadDLL("kernel32.dll")
	if err != nil {
		return err
	}
	proc, err := dll.FindProc("GenerateConsoleCtrlEvent")
	if err != nil {
		return err
	}
	const ctrlBreakEvent = 1
	if r, _, err := proc.Call(ctrlBreakEvent, uintptr(c.Process.Pid)); r == 0 {
		return err
	}
	return nil
}
