package roster

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeRoster puts a roster text into a temporary XDG_CONFIG_HOME and
// returns its path.
func writeRosterText(t *testing.T, text string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	path := filepath.Join(home, "docflow", "roster.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRosterAccessNames(t *testing.T) {
	t.Run("policy file order", func(t *testing.T) {
		path := writeRosterText(t, policyHeader+
			"[access.zai]\nagents = [\"pi\"]\n\n[access.codex]\nagents = [\"codex\"]\n\n"+
			"[access.\"opencode-go\"]\nagents = [\"pi\", \"opencode\"]\n\n[access.local]\nagents = [\"pi\"]\nkind = \"local\"\n\n"+
			policyRoles)
		names, err := RosterAccessNames(path)
		if err != nil {
			t.Fatalf("RosterAccessNames() error = %v", err)
		}
		// The names keep the policy file order, and a quoted header loses
		// its quotes.
		if want := []string{"zai", "codex", "opencode-go", "local"}; !reflect.DeepEqual(names, want) {
			t.Fatalf("names = %v, want %v", names, want)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		names, err := RosterAccessNames("")
		if err != nil || names != nil {
			t.Fatalf("RosterAccessNames() = (%v, %v), want an empty list", names, err)
		}
	})

	t.Run("v1 file names the migration", func(t *testing.T) {
		path := writeRosterText(t, "spend = [\"zai\", \"codex\"]\n\n[provider.zai]\ntrusted = true\n")
		_, err := RosterAccessNames(path)
		if err == nil || !strings.Contains(err.Error(), "docflow roster migrate") {
			t.Fatalf("RosterAccessNames() error = %v, want the migration hint", err)
		}
		if coded, ok := err.(interface{ ExitCode() int }); !ok || coded.ExitCode() != 2 {
			t.Fatalf("error = %v, want exit code 2", err)
		}
	})

	t.Run("broken file", func(t *testing.T) {
		path := writeRosterText(t, "version = 2\n[access\n")
		_, err := RosterAccessNames(path)
		if err == nil || !strings.Contains(err.Error(), "roster.toml") {
			t.Fatalf("RosterAccessNames() error = %v, want the roster path", err)
		}
		if coded, ok := err.(interface{ ExitCode() int }); !ok || coded.ExitCode() != 2 {
			t.Fatalf("error = %v, want exit code 2", err)
		}
	})
}
