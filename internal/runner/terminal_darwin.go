//go:build darwin

package runner

import "syscall"

func termiosGetRequest() uintptr { return uintptr(syscall.TIOCGETA) }
func termiosSetRequest() uintptr { return uintptr(syscall.TIOCSETA) }
