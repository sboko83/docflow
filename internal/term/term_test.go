package term

import (
	"bytes"
	"os"
	"runtime"
	"testing"
)

func TestIsTerminalRejectsNonTerminals(t *testing.T) {
	if IsTerminal(&bytes.Buffer{}) {
		t.Fatal("buffer detected as terminal")
	}
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if IsTerminal(file) {
		t.Fatal("null character device detected as terminal")
	}
}

func TestOpenTTYReturnsConsoleWhenPresent(t *testing.T) {
	tty, err := OpenTTY()
	if err != nil {
		t.Skipf("no controlling terminal: %v", err)
	}
	defer tty.Close()
	info, err := tty.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode()&os.ModeCharDevice == 0 {
		t.Fatalf("controlling terminal is not a character device: %v", info.Mode())
	}
}
