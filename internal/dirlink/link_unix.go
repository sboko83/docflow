//go:build !windows

// Package dirlink creates directory links without requiring privileges.
package dirlink

import (
	"os"
	"path/filepath"
)

// Create links name to target, preserving relative targets on Unix.
func Create(target, name string) error {
	return os.Symlink(target, name)
}

// Resolve follows directory links and symbolic links to an existing path.
func Resolve(name string) (string, error) {
	return filepath.EvalSymlinks(name)
}
