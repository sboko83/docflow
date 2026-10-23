package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestHomeAndBaseURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOCFLOW_RELEASE_URL", "")
	got, err := Home()
	if err != nil || got != filepath.Join(home, ".docflow") {
		t.Fatalf("Home() = %q, %v", got, err)
	}
	if BaseURL() != defaultBaseURL {
		t.Fatalf("default BaseURL = %q", BaseURL())
	}
	t.Setenv("DOCFLOW_RELEASE_URL", "http://localhost:1234/releases///")
	if BaseURL() != "http://localhost:1234/releases" {
		t.Fatalf("override BaseURL = %q", BaseURL())
	}
	t.Setenv("HOME", "")
	if _, err := Home(); err == nil {
		t.Fatal("empty HOME accepted")
	}
}

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"v1.2.3", "1.2.3", 0},
		{"0.4.0", "0.3.99", 1},
		{"1.10.0", "1.9.99", 1},
		{"2.0.0", "10.0.0", -1},
		{"1.2.3", "1.2.4", -1},
		{"1.2.999999999999999999999999999999", "1.2.1000000000000000000000000000000", -1},
		{"dev", "dev", 0},
	} {
		t.Run(tc.a+"_"+tc.b, func(t *testing.T) {
			if got := Compare(tc.a, tc.b); got != tc.want {
				t.Fatalf("Compare(%q, %q) = %d; want %d", tc.a, tc.b, got, tc.want)
			}
			if got := Compare(tc.b, tc.a); got != -tc.want {
				t.Fatalf("reverse comparison = %d; want %d", got, -tc.want)
			}
		})
	}
}

func TestLatest(t *testing.T) {
	for _, tc := range []struct {
		body string
		ok   bool
	}{
		{"0.4.0\n", true}, {" \t1.2.3\r\n", true},
		{"1.2", false}, {"1.2.3.4", false}, {"1.2.3-rc1", false},
		{"01.2.3", false}, {"v1.2.3", false}, {"1. 2.3", false},
		{"1.2.3\n1.2.4", false}, {"", false}, {strings.Repeat("1", 1025), false},
	} {
		t.Run(fmt.Sprintf("%q", tc.body[:min(len(tc.body), 30)]), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/releases/latest/download/VERSION" {
					t.Errorf("unexpected URL %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			got, err := Latest(context.Background(), server.URL+"/releases/")
			if tc.ok {
				if err != nil || got != strings.TrimSpace(tc.body) {
					t.Fatalf("Latest = %q, %v", got, err)
				}
			} else if err == nil {
				t.Fatalf("Latest accepted %q", tc.body)
			}
		})
	}
}

func TestHTTPFailureAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	if _, err := Latest(context.Background(), server.URL); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("Latest HTTP error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Latest(ctx, server.URL); err == nil {
		t.Fatal("Latest ignored canceled context")
	}
	if err := Fetch(ctx, server.URL, "1.2.3", "linux", "amd64", t.TempDir()); err == nil {
		t.Fatal("Fetch ignored canceled context")
	}
}

type archiveEntry struct {
	name, body, link string
	mode             int64
	kind             byte
}

func archiveBytes(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		size := int64(0)
		if kind == tar.TypeReg {
			size = int64(len(entry.body))
		}
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Mode: entry.mode, Typeflag: kind, Size: size, Linkname: entry.link}); err != nil {
			t.Fatal(err)
		}
		if size != 0 {
			if _, err := tw.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func releaseServer(t *testing.T, archive []byte, sums string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download/v1.2.3/docflow_linux_amd64.tar.gz":
			w.Write(archive)
		case "/download/v1.2.3/checksums.txt":
			fmt.Fprint(w, sums)
		default:
			t.Errorf("unexpected release URL %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func TestFetch(t *testing.T) {
	archive := archiveBytes(t, []archiveEntry{
		{name: "./", mode: 0755, kind: tar.TypeDir},
		{name: "./docflow", body: "binary", mode: 0751},
		{name: "LICENSE", body: "license", mode: 0644},
		{name: "skills/", mode: 0750, kind: tar.TypeDir},
		{name: "skills/example/SKILL.md", body: "skill", mode: 0640},
	})
	sum := fmt.Sprintf("%x", sha256.Sum256(archive))
	server := releaseServer(t, archive, strings.Repeat("0", 64)+"  docflow_darwin_arm64.tar.gz\n"+sum+" *docflow_linux_amd64.tar.gz\n")
	defer server.Close()
	dir := filepath.Join(t.TempDir(), "unpacked")
	if err := Fetch(context.Background(), server.URL+"/", "v1.2.3", "linux", "amd64", dir); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, content string
		mode          os.FileMode
	}{
		{"docflow", "binary", 0751}, {"LICENSE", "license", 0644},
		{"skills/example/SKILL.md", "skill", 0640},
	} {
		assertFile(t, filepath.Join(dir, tc.name), tc.content, tc.mode)
	}
	info, err := os.Stat(filepath.Join(dir, "skills"))
	if err != nil || info.Mode().Perm() != 0750 {
		t.Fatalf("skills directory mode: %v, %v", info, err)
	}
}

func TestFetchBadChecksums(t *testing.T) {
	archive := archiveBytes(t, []archiveEntry{{name: "docflow", body: "binary", mode: 0755}})
	sum := fmt.Sprintf("%x", sha256.Sum256(archive))
	for name, sums := range map[string]string{
		"mismatch":  strings.Repeat("0", 64) + "  docflow_linux_amd64.tar.gz\n",
		"missing":   sum + "  docflow_linux_arm64.tar.gz\n",
		"malformed": "xyz  docflow_linux_amd64.tar.gz\n",
		"duplicate": strings.Repeat(sum+"  docflow_linux_amd64.tar.gz\n", 2),
	} {
		t.Run(name, func(t *testing.T) {
			server := releaseServer(t, archive, sums)
			defer server.Close()
			dir := t.TempDir()
			if err := Fetch(context.Background(), server.URL, "1.2.3", "linux", "amd64", dir); err == nil {
				t.Fatal("Fetch accepted invalid checksums")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("unverified archive was extracted: %v, %v", entries, err)
			}
		})
	}
}

func TestFetchUnsafeEntries(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "escaped")
	for _, entry := range []archiveEntry{
		{name: "../escaped"}, {name: "skills/../../escaped"},
		{name: "skills/../escaped"}, {name: outside},
		{name: "skills\\..\\escaped"},
		{name: "link", kind: tar.TypeSymlink, link: outside},
		{name: "link", kind: tar.TypeLink, link: outside},
		{name: "pipe", kind: tar.TypeFifo},
	} {
		t.Run(fmt.Sprintf("%s_%d", entry.name, entry.kind), func(t *testing.T) {
			archive := archiveBytes(t, []archiveEntry{entry})
			server := releaseServer(t, archive, fmt.Sprintf("%x  docflow_linux_amd64.tar.gz\n", sha256.Sum256(archive)))
			defer server.Close()
			if err := Fetch(context.Background(), server.URL, "1.2.3", "linux", "amd64", t.TempDir()); err == nil {
				t.Fatal("Fetch accepted unsafe entry")
			}
		})
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("archive escaped: %v", err)
	}
}

func TestFetchRejectsExistingSymlinks(t *testing.T) {
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "existing"), 0755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "skills")); err != nil {
		t.Fatal(err)
	}
	archive := archiveBytes(t, []archiveEntry{{name: "skills/existing/escaped", body: "bad", mode: 0644}})
	server := releaseServer(t, archive, fmt.Sprintf("%x  docflow_linux_amd64.tar.gz\n", sha256.Sum256(archive)))
	defer server.Close()
	if err := Fetch(context.Background(), server.URL, "1.2.3", "linux", "amd64", dir); err == nil {
		t.Fatal("Fetch followed an existing symlink outside its root")
	}
	if _, err := os.Stat(filepath.Join(outside, "existing", "escaped")); !os.IsNotExist(err) {
		t.Fatalf("archive escaped through symlink: %v", err)
	}
}

func TestFetchInvalidInput(t *testing.T) {
	for _, tc := range []struct{ version, goos, goarch string }{
		{"../bad", "linux", "amd64"}, {"1.2.3", "windows", "amd64"},
		{"1.2.3", "linux", "../amd64"},
	} {
		if err := Fetch(context.Background(), "http://invalid.invalid", tc.version, tc.goos, tc.goarch, t.TempDir()); err == nil {
			t.Fatalf("Fetch accepted invalid input %+v", tc)
		}
	}
}

func assertFile(t *testing.T, name, want string, mode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil || string(data) != want {
		t.Fatalf("%s = %q, %v; want %q", name, data, err, want)
	}
	info, err := os.Stat(name)
	if err != nil || info.Mode().Perm() != mode {
		t.Fatalf("%s mode = %v, %v; want %o", name, info, err, mode)
	}
}

func sourceTree(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range map[string]string{"docflow": version, "LICENSE": "license", "skills/example/SKILL.md": "skill"} {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0644)
		if name == "docflow" {
			mode = 0755
		}
		if err := os.WriteFile(full, []byte(data), mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestActivateRetainsPrevious(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".docflow")
	for _, version := range []string{"1.0.0", "1.2.0", "1.1.0"} {
		src := sourceTree(t, version)
		if err := Activate(home, src, version); err != nil {
			t.Fatal(err)
		}
		assertFile(t, filepath.Join(home, "bin", "docflow"), version, 0755)
		assertFile(t, filepath.Join(home, "current", "LICENSE"), "license", 0644)
		assertFile(t, filepath.Join(home, "current", "skills", "example", "SKILL.md"), "skill", 0644)
		// A later edit to the source cannot change the installed tree.
		if err := os.WriteFile(filepath.Join(src, "docflow"), []byte("changed"), 0755); err != nil {
			t.Fatal(err)
		}
		assertFile(t, filepath.Join(home, "bin", "docflow"), version, 0755)
	}
	entries, err := os.ReadDir(filepath.Join(home, "versions"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !reflect.DeepEqual(names, []string{"1.1.0", "1.2.0"}) {
		t.Fatalf("retained versions = %v", names)
	}
	for name, want := range map[string]string{"current": "versions/1.1.0", "bin/docflow": "../current/docflow"} {
		got, err := os.Readlink(filepath.Join(home, name))
		if err != nil || got != want {
			t.Fatalf("%s link = %q, %v; want %q", name, got, err, want)
		}
	}
}

func TestActivateFailurePreservesCurrent(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".docflow")
	if err := Activate(home, sourceTree(t, "1.0.0"), "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := Activate(home, sourceTree(t, "replacement"), "1.0.0"); err == nil {
		t.Fatal("existing release overwritten")
	}
	src := sourceTree(t, "1.1.0")
	if err := os.Symlink("docflow", filepath.Join(src, "unsupported-link")); err != nil {
		t.Fatal(err)
	}
	if err := Activate(home, src, "1.1.0"); err == nil {
		t.Fatal("source symlink accepted")
	}
	assertFile(t, filepath.Join(home, "bin", "docflow"), "1.0.0", 0755)
	entries, err := os.ReadDir(filepath.Join(home, "versions"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "1.0.0" {
		t.Fatalf("failed activation left files: %v, %v", entries, err)
	}
	if err := Activate(home, src, "../bad"); err == nil {
		t.Fatal("invalid version accepted")
	}
}

func TestEnsureAndRemovePath(t *testing.T) {
	for _, shell := range []string{"/bin/zsh", "/bin/bash"} {
		t.Run(shell, func(t *testing.T) {
			homeDir := t.TempDir()
			t.Setenv("HOME", homeDir)
			profile := filepath.Join(homeDir, profileName(shell, runtime.GOOS))
			initial := "# user settings\nexport EDITOR=vi\n"
			if err := os.WriteFile(profile, []byte(initial), 0600); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				got, err := EnsurePath(filepath.Join(homeDir, ".docflow"), shell, homeDir)
				if err != nil || got != profile {
					t.Fatalf("EnsurePath = %q, %v", got, err)
				}
			}
			assertFile(t, profile, initial+pathBlock, 0600)
			for i := 0; i < 2; i++ {
				if err := RemovePath(homeDir); err != nil {
					t.Fatal(err)
				}
			}
			assertFile(t, profile, initial, 0600)
		})
	}
}

func TestPathProfiles(t *testing.T) {
	for _, tc := range []struct{ shell, goos, want string }{
		{"/bin/zsh", "darwin", ".zshrc"}, {"zsh", "linux", ".zshrc"},
		{"/bin/bash", "darwin", ".bash_profile"}, {"bash", "linux", ".bashrc"},
		{"fish", "linux", ""}, {"", "darwin", ""},
	} {
		if got := profileName(tc.shell, tc.goos); got != tc.want {
			t.Fatalf("profileName(%q, %q) = %q; want %q", tc.shell, tc.goos, got, tc.want)
		}
	}
	homeDir := t.TempDir()
	home := filepath.Join(homeDir, ".docflow")
	got, err := EnsurePath(home, "/usr/bin/fish", homeDir)
	if err != nil || !strings.Contains(got, filepath.Join(home, "bin")) {
		t.Fatalf("manual instruction = %q, %v", got, err)
	}
	entries, err := os.ReadDir(homeDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unsupported shell wrote files: %v, %v", entries, err)
	}
	// Removal covers every supported profile, including CRLF and no final LF.
	for _, name := range []string{".zshrc", ".bashrc", ".bash_profile"} {
		if err := os.WriteFile(filepath.Join(homeDir, name), []byte("before\n"+strings.ReplaceAll(pathBlock, "\n", "\r\n")+"after"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemovePath(homeDir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".zshrc", ".bashrc", ".bash_profile"} {
		assertFile(t, filepath.Join(homeDir, name), "before\nafter", 0644)
	}
}

func TestEnsurePathCreatesProfile(t *testing.T) {
	homeDir := t.TempDir()
	profile, err := EnsurePath(filepath.Join(homeDir, ".docflow"), "zsh", homeDir)
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, profile, pathBlock, 0644)
	if err := os.WriteFile(profile, []byte("user-setting"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsurePath(filepath.Join(homeDir, ".docflow"), "zsh", homeDir); err != nil {
		t.Fatal(err)
	}
	assertFile(t, profile, "user-setting\n"+pathBlock, 0644)
}

func TestCheckDue(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".docflow")
	now := time.Date(2026, 10, 23, 12, 0, 0, 1234, time.FixedZone("MSK", 3*60*60))
	if !CheckDue(home, now) {
		t.Fatal("missing timestamp was not due")
	}
	if err := RecordCheck(home, now); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []time.Duration{0, 24*time.Hour - time.Nanosecond} {
		if CheckDue(home, now.Add(delta)) {
			t.Fatalf("check due after %s", delta)
		}
	}
	if !CheckDue(home, now.Add(24*time.Hour)) {
		t.Fatal("check not due after 24 hours")
	}
	if err := os.WriteFile(filepath.Join(home, "last-update-check"), []byte("broken\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !CheckDue(home, now) {
		t.Fatal("invalid timestamp was not due")
	}
}
