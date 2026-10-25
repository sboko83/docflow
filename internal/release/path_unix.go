//go:build !windows

package release

import "os"

// EnsurePath returns the managed profile path, or a manual instruction for
// unsupported shells without writing a profile.
func EnsurePath(home, shell, homeDir string) (string, error) {
	return ensureProfilePath(home, shell, homeDir)
}

// RemovePath removes only the managed block from supported shell profiles.
func RemovePath(homeDir string) error {
	return removeProfilePath(homeDir)
}

// RemoveInstallation deletes a standalone installation after unlinking skills.
func RemoveInstallation(home string) error {
	return os.RemoveAll(home)
}
