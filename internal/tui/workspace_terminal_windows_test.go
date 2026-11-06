//go:build windows

package tui

import (
	"testing"
	"time"
)

type workspaceConsole struct{ *conPTY }

func (c workspaceConsole) output() string { return c.text() }

func (c workspaceConsole) send(key string) {
	c.conPTY.send(key)
	if key == "\x1b" {
		time.Sleep(300 * time.Millisecond)
	}
}

func (c workspaceConsole) quit() {
	c.conPTY.quit()
	if code := c.wait(20 * time.Second); code != 0 {
		c.t.Fatalf("workspace exited with code %d", code)
	}
}

func startWorkspaceTerminal(t *testing.T, f *workspaceFixture, width, height int) workspaceTerminal {
	bin, _ := buildConsoleCLI(t, "", f.scenario)
	return workspaceConsole{startConPTY(t, f.root, f.env, width, height, bin, "tui", "--roster", f.policy)}
}
