//go:build windows

package tui

import tea "charm.land/bubbletea/v2"

// watchHangup is a no-op on Windows, which has no SIGHUP: SIGINT already
// reaches Bubble Tea and closing the console terminates the process.
func watchHangup(*tea.Program) func() { return func() {} }
