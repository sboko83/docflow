// Package release downloads and activates standalone docflow releases.
package release

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const defaultBaseURL = "https://github.com/sboko83/docflow/releases"
const pathBlock = "# docflow\nexport PATH=\"$HOME/.docflow/bin:$PATH\"\n"

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// Home returns the standalone installation directory without creating it.
func Home() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".docflow"), nil
}

// InstalledExecutable reports whether the resolved executable belongs to a
// standalone installation. Resolve the root too, since HOME may be a symlink.
func InstalledExecutable(executable string) bool {
	home, err := Home()
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return false
	}
	versions, err := filepath.EvalSymlinks(filepath.Join(home, "versions"))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(versions, resolved)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func BaseURL() string {
	base := os.Getenv("DOCFLOW_RELEASE_URL")
	if base == "" {
		base = defaultBaseURL
	}
	return strings.TrimRight(base, "/")
}

func normalizedVersion(version string) (string, error) {
	version = strings.TrimPrefix(version, "v")
	if !versionPattern.MatchString(version) {
		return "", fmt.Errorf("invalid release version %q: want X.Y.Z", version)
	}
	return version, nil
}

// Compare compares X.Y.Z versions numerically, accepting a leading v.
// Invalid inputs fall back to lexical ordering. Release input must first be
// validated by Latest, Fetch or Activate.
func Compare(a, b string) int {
	left, errA := normalizedVersion(a)
	right, errB := normalizedVersion(b)
	if errA != nil || errB != nil {
		return strings.Compare(a, b)
	}
	x, y := strings.Split(left, "."), strings.Split(right, ".")
	for i := range x {
		// Compare lengths first so numeric components cannot overflow an int.
		if len(x[i]) < len(y[i]) {
			return -1
		}
		if len(x[i]) > len(y[i]) {
			return 1
		}
		if n := strings.Compare(x[i], y[i]); n != 0 {
			return n
		}
	}
	return 0
}

func get(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp.Body, nil
}

func Latest(ctx context.Context, base string) (string, error) {
	body, err := get(ctx, strings.TrimRight(base, "/")+"/latest/download/VERSION")
	if err != nil {
		return "", err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, 1025))
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(string(data))
	if len(data) > 1024 || !versionPattern.MatchString(version) {
		return "", fmt.Errorf("invalid VERSION: want X.Y.Z")
	}
	return version, nil
}

// Fetch verifies a release archive before extracting it. Only regular files
// and directories are accepted; archive links and special files are rejected.
func Fetch(ctx context.Context, base, version, goos, goarch, dir string) error {
	version, err := normalizedVersion(version)
	if err != nil {
		return err
	}
	if (goos != "darwin" && goos != "linux") || (goarch != "amd64" && goarch != "arm64") {
		return fmt.Errorf("unsupported release platform %s/%s", goos, goarch)
	}
	name := fmt.Sprintf("docflow_%s_%s.tar.gz", goos, goarch)
	url := strings.TrimRight(base, "/") + "/download/v" + version + "/"
	want, err := checksum(ctx, url+"checksums.txt", name)
	if err != nil {
		return err
	}
	body, err := get(ctx, url+name)
	if err != nil {
		return err
	}
	defer body.Close()
	archive, err := os.CreateTemp("", "docflow-release-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(archive, hash), body); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != want {
		return fmt.Errorf("sha256 mismatch for %s", name)
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return extract(archive, dir)
}

func checksum(ctx context.Context, url, name string) (string, error) {
	body, err := get(ctx, url)
	if err != nil {
		return "", err
	}
	defer body.Close()
	scanner := bufio.NewScanner(io.LimitReader(body, 1<<20))
	var sum string
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		decoded, err := hex.DecodeString(fields[0])
		if err != nil || len(decoded) != sha256.Size || sum != "" {
			return "", fmt.Errorf("invalid checksum entry for %s", name)
		}
		sum = strings.ToLower(fields[0])
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if sum == "" {
		return "", fmt.Errorf("checksum missing for %s", name)
	}
	return sum, nil
}

type directoryMode struct {
	path string
	mode fs.FileMode
}

func restoreDirectoryModes(dirs []directoryMode) error {
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i].path, dirs[i].mode); err != nil {
			return err
		}
	}
	return nil
}

// makeDirectory creates a directory without replacing an existing symlink.
func makeDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("not a directory: %s", dir)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := makeDirectory(filepath.Dir(dir)); err != nil {
		return err
	}
	return os.Mkdir(dir, 0755)
}

func extract(src io.Reader, dir string) error {
	if err := makeDirectory(dir); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	gz, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var dirs []directoryMode
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := header.Name
		if path.IsAbs(name) || strings.Contains(name, "\\") {
			return fmt.Errorf("unsafe archive path %q", name)
		}
		for _, part := range strings.Split(name, "/") {
			if part == ".." {
				return fmt.Errorf("unsafe archive path %q", name)
			}
		}
		target := filepath.FromSlash(path.Clean(name))
		mode := header.FileInfo().Mode().Perm()
		switch header.Typeflag {
		case tar.TypeDir:
			if err := makeExtractDirectory(root, target); err != nil {
				return err
			}
			dirs = append(dirs, directoryMode{target, mode})
		case tar.TypeReg:
			if err := makeExtractDirectory(root, filepath.Dir(target)); err != nil {
				return err
			}
			file, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return err
			}
			if err := writeOpenedFile(file, reader, mode); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported archive entry %q (type %d)", name, header.Typeflag)
		}
	}
	// Consume the gzip trailer too: tar EOF can precede a corrupt gzip checksum.
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		file, err := root.Open(dirs[i].path)
		if err != nil {
			return err
		}
		if err := errors.Join(file.Chmod(dirs[i].mode), file.Close()); err != nil {
			return err
		}
	}
	return nil
}

func makeExtractDirectory(root *os.Root, dir string) error {
	info, err := root.Lstat(dir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("not an archive directory: %s", dir)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := makeExtractDirectory(root, filepath.Dir(dir)); err != nil {
		return err
	}
	return root.Mkdir(dir, 0755)
}

func writeFile(name string, src io.Reader, mode fs.FileMode) error {
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	return writeOpenedFile(file, src, mode)
}

func writeOpenedFile(file *os.File, src io.Reader, mode fs.FileMode) error {
	_, copyErr := io.Copy(file, src)
	modeErr := file.Chmod(mode)
	closeErr := file.Close()
	return errors.Join(copyErr, modeErr, closeErr)
}

func copyTree(src, dst string) error {
	var dirs []directoryMode
	err := filepath.WalkDir(src, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, name)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			if err := makeDirectory(target); err != nil {
				return err
			}
			dirs = append(dirs, directoryMode{target, info.Mode().Perm()})
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported release file %s", name)
		}
		file, err := os.Open(name)
		if err != nil {
			return err
		}
		copyErr := writeFile(target, file, info.Mode().Perm())
		return errors.Join(copyErr, file.Close())
	})
	if err != nil {
		return err
	}
	return restoreDirectoryModes(dirs)
}

// Activate installs an immutable version directory, then atomically switches
// current. An existing version directory is never overwritten.
func Activate(home, srcDir, version string) error {
	version, err := normalizedVersion(version)
	if err != nil {
		return err
	}
	info, err := os.Lstat(srcDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("release source is not a directory: %s", srcDir)
	}
	versions := filepath.Join(home, "versions")
	src, err := filepath.Abs(srcDir)
	if err != nil {
		return err
	}
	dst, err := filepath.Abs(versions)
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(src, dst); err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return fmt.Errorf("release destination must be outside its source")
	}
	previous := ""
	current := filepath.Join(home, "current")
	if target, err := os.Readlink(current); err == nil {
		previous = strings.TrimPrefix(target, "versions/")
		if !versionPattern.MatchString(previous) || target != "versions/"+previous {
			return fmt.Errorf("invalid current release link %q", target)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := makeDirectory(versions); err != nil {
		return err
	}
	final := filepath.Join(versions, version)
	if _, err := os.Lstat(final); err == nil {
		return fmt.Errorf("release %s already exists", version)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stage, err := os.MkdirTemp(versions, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := copyTree(srcDir, stage); err != nil {
		return err
	}
	if err := os.Rename(stage, final); err != nil {
		return err
	}
	activated := false
	defer func() {
		if !activated {
			os.RemoveAll(final)
		}
	}()
	if err := makeDirectory(filepath.Join(home, "bin")); err != nil {
		return err
	}
	bin := filepath.Join(home, "bin", "docflow")
	if _, err := os.Lstat(bin); errors.Is(err, os.ErrNotExist) {
		if err := os.Symlink("../current/docflow", bin); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	links, err := os.MkdirTemp(home, ".activate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(links)
	link := filepath.Join(links, "current")
	if err := os.Symlink("versions/"+version, link); err != nil {
		return err
	}
	if err := os.Rename(link, current); err != nil {
		return err
	}
	activated = true
	entries, err := os.ReadDir(versions)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == version || entry.Name() == previous || !versionPattern.MatchString(entry.Name()) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(versions, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func profileName(shell, goos string) string {
	switch filepath.Base(shell) {
	case "zsh":
		return ".zshrc"
	case "bash":
		if goos == "darwin" {
			return ".bash_profile"
		}
		return ".bashrc"
	default:
		return ""
	}
}

// EnsurePath returns the profile path it updated (or already found configured).
// For unsupported shells it writes nothing and returns a manual instruction.
func EnsurePath(home, shell, homeDir string) (string, error) {
	profile := profileName(shell, runtime.GOOS)
	if profile == "" {
		return fmt.Sprintf("Add %s to your shell's PATH.", filepath.Join(home, "bin")), nil
	}
	name := filepath.Join(homeDir, profile)
	data, err := os.ReadFile(name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if removePathBlock(string(data)) != string(data) {
		return name, nil
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	data = append(data, pathBlock...)
	if err := os.WriteFile(name, data, 0644); err != nil {
		return "", err
	}
	return name, nil
}

func removePathBlock(text string) string {
	lines := strings.SplitAfter(text, "\n")
	var result strings.Builder
	for i := 0; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r\n") == "# docflow" && i+1 < len(lines) &&
			strings.TrimRight(lines[i+1], "\r\n") == `export PATH="$HOME/.docflow/bin:$PATH"` {
			i++
			continue
		}
		result.WriteString(lines[i])
	}
	return result.String()
}

// RemovePath removes only the managed two-line block from supported profiles.
func RemovePath(homeDir string) error {
	for _, profile := range []string{".zshrc", ".bashrc", ".bash_profile"} {
		name := filepath.Join(homeDir, profile)
		data, err := os.ReadFile(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		clean := removePathBlock(string(data))
		if clean != string(data) {
			if err := os.WriteFile(name, []byte(clean), 0644); err != nil {
				return err
			}
		}
	}
	return nil
}

// CheckDue treats a missing or unreadable timestamp as due.
func CheckDue(home string, now time.Time) bool {
	data, err := os.ReadFile(filepath.Join(home, "last-update-check"))
	if err != nil {
		return true
	}
	last, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data)))
	return err != nil || now.Sub(last) >= 24*time.Hour
}

func RecordCheck(home string, now time.Time) error {
	if err := os.MkdirAll(home, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, "last-update-check"), []byte(now.UTC().Format(time.RFC3339Nano)+"\n"), 0644)
}
