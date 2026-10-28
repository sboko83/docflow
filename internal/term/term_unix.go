//go:build darwin || linux

package term

import (
	"os"
	"syscall"
	"unsafe"
)

// IsTerminal reports whether stream is a terminal: a writer that knows about
// itself, or an *os.File whose descriptor accepts the terminal state request.
// Pipes, regular files and character devices like /dev/null read as not a
// terminal.
func IsTerminal(stream any) bool {
	if value, ok := stream.(interface{ IsTerminal() bool }); ok {
		return value.IsTerminal()
	}
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}
	var state syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), termiosGetRequest(), uintptr(unsafe.Pointer(&state)))
	return errno == 0
}

func EnableVTProcessing(stream *os.File) {}

// OpenTTY returns the controlling terminal for reading. /dev/tty stays the
// user's terminal even when stdin carries other data, such as the script
// pipe of curl … | sh.
func OpenTTY() (*os.File, error) {
	return os.OpenFile("/dev/tty", os.O_RDONLY, 0)
}
