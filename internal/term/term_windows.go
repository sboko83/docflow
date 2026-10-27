//go:build windows

package term

import (
	"os"
	"syscall"
)

// IsTerminal reports a real console. GetConsoleMode succeeds only on a console
// input or output handle, so a redirected file, a pipe and the NUL device read
// as not a terminal. The fd of a standard stream is the value Windows gave the
// process for it, which is the console handle in a terminal.
func IsTerminal(stream any) bool {
	if value, ok := stream.(interface{ IsTerminal() bool }); ok {
		return value.IsTerminal()
	}
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(file.Fd()), &mode) == nil
}

// OpenTTY returns the console input for reading. CONIN$ stays the user's
// console even when stdin carries other data, such as the script pipe of
// curl … | sh.
func OpenTTY() (*os.File, error) {
	return os.OpenFile("CONIN$", os.O_RDWR, 0)
}
