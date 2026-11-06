//go:build darwin || linux

package tui

import "testing"

func startWorkspaceTerminal(t *testing.T, f *workspaceFixture, width, height int) workspaceTerminal {
	p := &acceptProject{Root: f.root, Env: f.env}
	s := startAcceptTUI(t, p, width, height, "--roster", f.policy)
	t.Cleanup(func() {
		if s.cmd.ProcessState == nil {
			_ = s.cmd.Process.Kill()
			_ = s.cmd.Wait()
			_ = s.master.Close()
		}
	})
	return s
}
