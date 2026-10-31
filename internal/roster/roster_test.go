package roster

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got, want := DefaultPath(), filepath.Join("/xdg", "docflow", "roster.toml"); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got, want := DefaultPath(), filepath.Join(home, ".config", "docflow", "roster.toml"); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestExpandUser(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	cases := map[string]string{
		"/abs/roster.toml":  "/abs/roster.toml",
		"relative.toml":     "relative.toml",
		"~/cfg/roster.toml": filepath.Join(home, "cfg", "roster.toml"),
		"~user/roster.toml": "~user/roster.toml",
		"~":                 home,
	}
	for input, want := range cases {
		if got := expandUser(input); got != want {
			t.Errorf("expandUser(%q) = %q, want %q", input, got, want)
		}
	}
}
