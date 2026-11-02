//go:build windows

package main

import (
	"errors"
	"time"
)

type terminal struct{}

func startTerminal(string, string, []string) (*terminal, error) {
	return nil, errors.New("live terminal acceptance needs a Unix PTY; run on macOS or Linux")
}

func (t *terminal) PID() int                            { return 0 }
func (t *terminal) Output() string                      { return "" }
func (t *terminal) Send(string)                         {}
func (t *terminal) WaitFor(string, time.Duration) error { return errors.New("no terminal") }
func (t *terminal) WaitExit(time.Duration) error        { return errors.New("no terminal") }
func (t *terminal) Close()                              {}

func agentProcesses(int, string) []processInfo { return nil }
