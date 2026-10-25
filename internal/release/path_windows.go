package release

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func powershell(script string) *exec.Cmd {
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
		"-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}

func pathKey(value string) string {
	value = strings.Trim(strings.TrimSpace(value), `"`)
	value = strings.ReplaceAll(value, "/", `\`)
	return strings.TrimRight(value, `\`)
}

// updateUserPath uses a regular temporary file when explicitly overridden by
// tests. An invalid override fails closed before any registry access.
func updateUserPath(home string, add bool, testFile string) error {
	var value string
	if testFile != "" {
		if !filepath.IsAbs(testFile) {
			return fmt.Errorf("DOCFLOW_TEST_USER_PATH must be an absolute regular file")
		}
		info, err := os.Lstat(testFile)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("DOCFLOW_TEST_USER_PATH must be a regular file")
		}
		data, err := os.ReadFile(testFile)
		if err != nil {
			return err
		}
		value = string(data)
	} else {
		cmd := powershell(`$ErrorActionPreference = 'Stop'; [Console]::OutputEncoding = [Text.UTF8Encoding]::new($false); [Console]::Write([Environment]::GetEnvironmentVariable('Path','User'))`)
		data, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("read user Path: %w", err)
		}
		value = string(data)
	}
	entry := filepath.Join(home, "current")
	var kept []string
	matched := false
	if value != "" {
		for _, part := range strings.Split(value, ";") {
			key := pathKey(part)
			if strings.EqualFold(key, pathKey(entry)) || strings.EqualFold(key, `%USERPROFILE%\.docflow\current`) {
				if add && !matched {
					kept = append(kept, part)
				}
				matched = true
				continue
			}
			kept = append(kept, part)
		}
	}
	if add && !matched {
		kept = append(kept, entry)
	}
	next := strings.Join(kept, ";")
	if next == value {
		return nil
	}
	if testFile != "" {
		return os.WriteFile(testFile, []byte(next), 0600)
	}
	cmd := powershell(`$ErrorActionPreference = 'Stop'; [Environment]::SetEnvironmentVariable('Path',$env:DOCFLOW_USER_PATH_VALUE,'User')`)
	cmd.Env = append(os.Environ(), "DOCFLOW_USER_PATH_VALUE="+next)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("write user Path: %w: %s", err, output)
	}
	return nil
}

// EnsurePath configures the current directory in the persistent user Path.
func EnsurePath(home, shell, homeDir string) (string, error) {
	if err := updateUserPath(home, true, os.Getenv("DOCFLOW_TEST_USER_PATH")); err != nil {
		return "", err
	}
	return "User Path configured; restart your terminal to use " + filepath.Join(home, "current") + ".", nil
}

// RemovePath removes the installation's entry and preserves other user paths.
func RemovePath(homeDir string) error {
	return updateUserPath(filepath.Join(homeDir, ".docflow"), false, os.Getenv("DOCFLOW_TEST_USER_PATH"))
}
