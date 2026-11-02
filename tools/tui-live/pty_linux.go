//go:build linux

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func openPTY() (*os.File, *os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock, number uint32
	if !ioctl(master.Fd(), syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)) || !ioctl(master.Fd(), syscall.TIOCGPTN, unsafe.Pointer(&number)) {
		master.Close()
		return nil, nil, syscall.EIO
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}

func ioctl(fd uintptr, request uintptr, data unsafe.Pointer) bool {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(data))
	return errno == 0
}

func setWinsize(file *os.File, width, height int) error {
	return unix.IoctlSetWinsize(int(file.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: uint16(height), Col: uint16(width)})
}
