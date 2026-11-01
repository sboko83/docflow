//go:build darwin || linux

package tui

import (
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
)

// watchHangup turns SIGHUP into a quit message so the workspace leaves through
// the same controlled stop path as q and SIGTERM. Bubble Tea handles SIGINT
// and SIGTERM itself; an unhandled hangup would kill the process before the
// run stops and writes its checkpoint.
func watchHangup(program *tea.Program) func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			program.Send(tea.QuitMsg{})
		case <-done:
		}
	}()
	return func() {
		signal.Stop(signals)
		close(done)
	}
}
