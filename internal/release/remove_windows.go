package release

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"bsv-docflow/internal/dirlink"
)

// RemoveInstallation moves the running executable out of the installation so
// deletion can finish synchronously. A helper removes that one file after exit.
func RemoveInstallation(home string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = dirlink.Resolve(executable)
	if err != nil {
		return err
	}
	root, err := dirlink.Resolve(home)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, executable)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return os.RemoveAll(home)
	}
	// A sibling stays on the installation's volume, so rename also works when
	// the user's home is on a different drive from the system temporary folder.
	scratch, err := os.MkdirTemp(filepath.Dir(root), ".docflow-uninstall-")
	if err != nil {
		return err
	}
	moved := filepath.Join(scratch, BinaryName())
	if err := os.Rename(executable, moved); err != nil {
		_ = os.Remove(scratch)
		return err
	}
	// Literal paths and non-recursive removal constrain cleanup to the moved
	// executable and its empty temporary directory. No shell interpolation.
	cmd := powershell(`$ErrorActionPreference = 'Stop'; $p = Get-Process -Id $env:DOCFLOW_UNINSTALL_PID -ErrorAction SilentlyContinue; if ($p -and !$p.WaitForExit(60000)) { exit 1 }; Remove-Item -LiteralPath $env:DOCFLOW_UNINSTALL_FILE -Force; Remove-Item -LiteralPath $env:DOCFLOW_UNINSTALL_DIR`)
	cmd.Env = append(os.Environ(), "DOCFLOW_UNINSTALL_PID="+strconv.Itoa(os.Getpid()),
		"DOCFLOW_UNINSTALL_FILE="+moved, "DOCFLOW_UNINSTALL_DIR="+scratch)
	if err := cmd.Start(); err != nil {
		restoreErr := os.Rename(moved, executable)
		_ = os.Remove(scratch)
		return fmt.Errorf("start uninstall cleanup: %w", errors.Join(err, restoreErr))
	}
	_ = cmd.Process.Release()
	return os.RemoveAll(home)
}
