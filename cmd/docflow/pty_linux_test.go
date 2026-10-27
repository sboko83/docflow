//go:build linux

package main

import (
	"fmt"
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
	var unlock, number uint32
	if !ptyIoctl(master.Fd(), syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)) || !ptyIoctl(master.Fd(), syscall.TIOCGPTN, unsafe.Pointer(&number)) {
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
