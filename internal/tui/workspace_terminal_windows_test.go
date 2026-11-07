//go:build windows

package tui

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

var vkKeyScan = windows.NewLazySystemDLL("user32.dll").NewProc("VkKeyScanW")

// keyboardTypes reports whether ConPTY delivers the text as typed: conhost
// turns input characters into key events of the active keyboard layout and
// drops a character the layout has no key for ($ on a Russian layout).
func keyboardTypes(text string) bool {
	for _, r := range text {
		if r > 0xffff {
			return false
		}
		code, _, _ := vkKeyScan.Call(uintptr(r))
		if int16(code) == -1 {
			return false
		}
	}
	return true
}

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
