//go:build darwin || linux

package runner

import (
	"io"
	"os"
	"syscall"
	"unsafe"
)

type terminalSize struct {
	rows, columns, xPixels, yPixels uint16
}

func isTerminal(stream any) bool {
	if value, ok := stream.(interface{ IsTerminal() bool }); ok {
		return value.IsTerminal()
	}
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}
	var state syscall.Termios
	return ioctl(file.Fd(), termiosGetRequest(), unsafe.Pointer(&state))
}

func terminalColumns(stream io.Writer) int {
	file, ok := stream.(*os.File)
	if !ok || !isTerminal(file) {
		return 80
	}
	var size terminalSize
	if !ioctl(file.Fd(), uintptr(syscall.TIOCGWINSZ), unsafe.Pointer(&size)) || size.columns == 0 {
		return 80
	}
	return int(size.columns)
}

func disableEcho(file *os.File) func() {
	if file == nil {
		return func() {}
	}
	var original syscall.Termios
	if !ioctl(file.Fd(), termiosGetRequest(), unsafe.Pointer(&original)) {
		return func() {}
	}
	quiet := original
	quiet.Lflag &^= syscall.ECHO
	if !ioctl(file.Fd(), termiosSetRequest(), unsafe.Pointer(&quiet)) {
		return func() {}
	}
	return func() { _ = ioctl(file.Fd(), termiosSetRequest(), unsafe.Pointer(&original)) }
}

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == syscall.ESRCH {
		return false
	}
	return true
}

func ioctl(fd uintptr, request uintptr, data unsafe.Pointer) bool {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(data))
	return errno == 0
}
