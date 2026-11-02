//go:build darwin

package tui

import (
	"bytes"
	"os"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func openTestPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	// Non-blocking: the runtime poller registers the master, so Close
	// interrupts a concurrent read. A blocking master deadlocks the reader
	// on Linux, where close does not wake a blocked read.
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []uintptr{syscall.TIOCPTYGRANT, syscall.TIOCPTYUNLK} {
		if !ioctl(master.Fd(), request, nil) {
			master.Close()
			t.Fatalf("PTY ioctl %#x failed", request)
		}
	}
	var name [128]byte
	if !ioctl(master.Fd(), syscall.TIOCPTYGNAME, unsafe.Pointer(&name[0])) {
		master.Close()
		t.Fatal("PTY name ioctl failed")
	}
	slave, err := os.OpenFile(string(bytes.TrimRight(name[:], "\x00")), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { master.Close(); slave.Close() })
	return master, slave
}

func ioctl(fd uintptr, request uintptr, data unsafe.Pointer) bool {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(data))
	return errno == 0
}

func setTestWinsize(file *os.File, width, height int) error {
	return unix.IoctlSetWinsize(int(file.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: uint16(height), Col: uint16(width)})
}

func testEchoEnabled(t *testing.T, file *os.File) bool {
	t.Helper()
	state, err := unix.IoctlGetTermios(int(file.Fd()), unix.TIOCGETA)
	if err != nil {
		t.Fatal(err)
	}
	return state.Lflag&unix.ECHO != 0
}
