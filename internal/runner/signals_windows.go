//go:build windows

package runner

import "os"

var stopSignals = []os.Signal{os.Interrupt}

func signalNumber(os.Signal) int  { return 2 }
func signalName(os.Signal) string { return "SIGINT" }
