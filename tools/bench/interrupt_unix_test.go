//go:build !windows

package main

import (
	"os"
	"os/exec"
)

// prepareInterrupt readies c to receive sendInterrupt; POSIX needs nothing.
func prepareInterrupt(c *exec.Cmd) {}

// sendInterrupt delivers SIGINT to the started command.
func sendInterrupt(c *exec.Cmd) error { return c.Process.Signal(os.Interrupt) }
