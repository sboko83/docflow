//go:build darwin

package term

import "syscall"

func termiosGetRequest() uintptr { return uintptr(syscall.TIOCGETA) }
