package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsUserPath(t *testing.T) {
	homeDir := t.TempDir()
	home := filepath.Join(homeDir, ".docflow")
	entry := filepath.Join(home, "current")
	before := `C:\tools;` + strings.Repeat(`C:\long path;`, 150) + entry + "-other"
	for _, initial := range []string{before, before + ";" + strings.ToUpper(entry) + `\`, before + `;%USERPROFILE%\.docflow\current`} {
		t.Run(initial[len(before):], func(t *testing.T) {
			testFile := filepath.Join(t.TempDir(), "user-path")
			if err := os.WriteFile(testFile, []byte(initial), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("DOCFLOW_TEST_USER_PATH", testFile)
			for i := 0; i < 2; i++ {
				if hint, err := EnsurePath(home, "bash", homeDir); err != nil || !strings.Contains(hint, "User Path configured") {
					t.Fatalf("EnsurePath = %q, %v", hint, err)
				}
			}
			value, err := os.ReadFile(testFile)
			if err != nil {
				t.Fatal(err)
			}
			want := initial
			if initial == before {
				want += ";" + entry
			}
			if string(value) != want {
				t.Fatal("install truncated or duplicated the user Path")
			}
			for i := 0; i < 2; i++ {
				if err := RemovePath(homeDir); err != nil {
					t.Fatal(err)
				}
			}
			assertFile(t, testFile, before, 0600)
		})
	}
	if _, err := os.Stat(filepath.Join(homeDir, ".bashrc")); !os.IsNotExist(err) {
		t.Fatalf("Windows Path configuration touched shell profiles: %v", err)
	}
}

func TestWindowsPathOverrideFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	for _, testFile := range []string{"relative-file", filepath.Join(homeDir, "missing"), homeDir} {
		t.Setenv("DOCFLOW_TEST_USER_PATH", testFile)
		if _, err := EnsurePath(filepath.Join(homeDir, ".docflow"), "bash", homeDir); err == nil {
			t.Fatalf("invalid Path override accepted: %s", testFile)
		}
		if err := RemovePath(homeDir); err == nil {
			t.Fatalf("invalid Path override accepted on removal: %s", testFile)
		}
	}
}
