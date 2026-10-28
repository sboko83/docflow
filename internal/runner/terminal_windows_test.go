//go:build windows

package runner

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"bsv-docflow/internal/term"
)

// consoleMode is the Windows oracle: the system accepts GetConsoleMode on a
// console handle and refuses it on a redirected file, a pipe and NUL. term.IsTerminal
// must agree on the very handle the process was given.
func consoleMode(handle uintptr) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(handle), &mode) == nil
}

func TestIsTerminalWindowsRealHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(path, []byte("redirected\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if term.IsTerminal(file) {
		t.Fatal("a regular file reads as a console")
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if term.IsTerminal(reader) {
		t.Fatal("a pipe reads as a console")
	}
	nul, err := os.OpenFile("NUL", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer nul.Close()
	if term.IsTerminal(nul) {
		t.Fatal("NUL reads as a console")
	}
	if term.IsTerminal(&plainStdin{}) {
		t.Fatal("a stream with no handle reads as a console")
	}
}

func TestIsTerminalWindowsStandardStreams(t *testing.T) {
	for _, stream := range []*os.File{os.Stdin, os.Stdout, os.Stderr} {
		name := stream.Name()
		if term.IsTerminal(stream) != consoleMode(stream.Fd()) {
			t.Fatalf("%s: term.IsTerminal=%v, GetConsoleMode accepts=%v", name, term.IsTerminal(stream), consoleMode(stream.Fd()))
		}
		if term.IsTerminal(stream) {
			t.Logf("%s is a Windows console", name)
		} else {
			t.Logf("%s is redirected", name)
		}
	}
}

// TestIsTerminalWindowsConsoleHandle follows the handle, not the stream: a
// duplicated console input handle is the same console.
func TestIsTerminalWindowsConsoleHandle(t *testing.T) {
	if !consoleMode(os.Stdin.Fd()) {
		t.Skip("stdin is not a Windows console (run `go test` in a terminal); recorded as Unverified")
	}
	var duplicate syscall.Handle
	process, err := syscall.GetCurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.DuplicateHandle(process, syscall.Handle(os.Stdin.Fd()), process, &duplicate, 0, false, syscall.DUPLICATE_SAME_ACCESS); err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(duplicate)
	console := os.NewFile(uintptr(duplicate), "stdin-console")
	if console == nil {
		t.Fatal("os.NewFile rejected the duplicated console handle")
	}
	if !term.IsTerminal(console) {
		t.Fatal("a duplicated console handle is not a terminal")
	}
	calls := recordUnblock(t)
	p, _ := newUnblockProgress(t)
	// the question goes through the native console handle and stops on the
	// cancellation: nothing is typed, no state is touched
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	unblocked, cancelled := askUnblock(ctx, t.TempDir(), "P001.1.1", "human", "нужен вход", false, p, newRunInput(console), os.Stdout)
	if unblocked || !cancelled {
		t.Fatalf("unblocked=%v cancelled=%v", unblocked, cancelled)
	}
	if len(*calls) != 0 {
		t.Fatalf("unblock ran: %v", *calls)
	}
}
