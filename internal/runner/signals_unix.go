//go:build darwin || linux

package runner

import (
	"os"
	"syscall"
)

var stopSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}

func signalNumber(s os.Signal) int { return int(s.(syscall.Signal)) }
func signalName(s os.Signal) string {
	switch s {
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGHUP:
		return "SIGHUP"
	default:
		return "SIGINT"
	}
}
