//go:build darwin

package runner

import (
	"bytes"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func openPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
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
