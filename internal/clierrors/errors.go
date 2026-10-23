package clierrors

import "fmt"

const (
	EXIT_OK      = 0
	EXIT_NOTHING = 1
	EXIT_FORMAT  = 2
	EXIT_BLOCKED = 3
	EXIT_VERIFY  = 4
	EXIT_ORDER   = 5
	EXIT_STALE   = 6
	EXIT_AGENT   = 7
)

type DocflowError struct {
	Message string
	Code    int
}

func (e DocflowError) Error() string { return e.Message }
func (e DocflowError) ExitCode() int {
	if e.Code == 0 {
		return EXIT_FORMAT
	}
	return e.Code
}

type HookError struct{ DocflowError }

func NewHookError(message string) HookError {
	return HookError{DocflowError{Message: message, Code: EXIT_VERIFY}}
}

func (HookError) ExitCode() int { return EXIT_VERIFY }

type FormatError struct {
	Path string
	Line int
	Text string
}

func (e FormatError) Error() string {
	return fmt.Sprintf("%s:%d: error: %s", e.Path, e.Line, e.Text)
}

func (FormatError) ExitCode() int { return EXIT_FORMAT }
