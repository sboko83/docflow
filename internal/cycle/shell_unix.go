//go:build !windows

package cycle

func planShell() []string {
	return []string{"/bin/sh", "-c"}
}
