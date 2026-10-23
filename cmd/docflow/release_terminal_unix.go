//go:build darwin || linux

package main

import (
	"io"
	"os"
	"syscall"
	"unsafe"
)

func releaseTerminal(stream io.Writer) bool {
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}
	var size [4]uint16
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&size)))
	return errno == 0
}
