package dirlink

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Create makes a directory junction. Environment expansion keeps shell
// metacharacters in paths inside quotes, without a second expansion pass.
func Create(target, name string) error {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"))
	// cmd.exe uses shell quoting, unlike the CommandLineToArgvW quoting in exec.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:    `cmd.exe /d /c mklink /J "%DOCFLOW_DIR_LINK%" "%DOCFLOW_DIR_TARGET%"`,
		HideWindow: true,
	}
	cmd.Env = append(os.Environ(), "DOCFLOW_DIR_LINK="+name, "DOCFLOW_DIR_TARGET="+absolute)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create junction %s: %w: %s", name, err, output)
	}
	return nil
}

// Resolve follows junctions too: since Go 1.23 EvalSymlinks treats them as
// ModeIrregular and leaves them in the path instead of following their target.
func Resolve(name string) (string, error) {
	for followed := 0; followed < 255; followed++ {
		absolute, err := filepath.Abs(name)
		if err != nil {
			return "", err
		}
		volume := filepath.VolumeName(absolute)
		parts := strings.Split(strings.TrimPrefix(absolute[len(volume):], `\`), `\`)
		prefix := volume + `\`
		found := false
		for i, part := range parts {
			prefix = filepath.Join(prefix, part)
			info, err := os.Lstat(prefix)
			if err != nil {
				return "", err
			}
			if info.Mode()&(os.ModeIrregular|os.ModeSymlink) == 0 {
				continue
			}
			target, err := os.Readlink(prefix)
			if err != nil {
				return "", err
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(prefix), target)
			}
			name = filepath.Join(append([]string{target}, parts[i+1:]...)...)
			found = true
			break
		}
		if !found {
			return filepath.EvalSymlinks(absolute)
		}
	}
	return "", fmt.Errorf("too many directory links in %s", name)
}
