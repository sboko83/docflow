//go:build windows

package term

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// IsTerminal follows the handle, including console handles supplied by ConPTY.
// Redirected streams remain non-terminals even when a console is attached.
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

// Columns returns the visible console width, or the plain-output default.
func Columns(stream *os.File) int {
	if stream != nil {
		var info windows.ConsoleScreenBufferInfo
		if windows.GetConsoleScreenBufferInfo(windows.Handle(stream.Fd()), &info) == nil {
			if width := int(info.Window.Right) - int(info.Window.Left) + 1; width > 0 {
				return width
			}
		}
	}
	return 80
}

// EnableVTProcessing enables ANSI output on console hosts that start without it.
// It leaves redirected streams alone; failure keeps the existing console mode.
func EnableVTProcessing(stream *os.File) {
	if stream == nil {
		return
	}
	handle := windows.Handle(stream.Fd())
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) == nil {
		_ = windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}

// OpenTTY returns the console input even when stdin is redirected.
func OpenTTY() (*os.File, error) { return os.OpenFile("CONIN$", os.O_RDWR, 0) }
