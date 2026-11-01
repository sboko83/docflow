//go:build linux

package tui

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func openTestPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	var unlock, number uint32
	if !ioctl(master.Fd(), syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)) || !ioctl(master.Fd(), syscall.TIOCGPTN, unsafe.Pointer(&number)) {
		master.Close()
		t.Fatal("PTY ioctl failed")
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
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
	state, err := unix.IoctlGetTermios(int(file.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	return state.Lflag&unix.ECHO != 0
}
