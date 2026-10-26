package cycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitMigratesLegacyAgentsMarker(t *testing.T) {
	for _, tc := range []struct {
		name, before, want string
	}{
		{"legacy", "Before\n" + legacyAgentsLine + "\nAfter\n", "Before\n" + agentsLine + "\nAfter\n"},
		{"both", legacyAgentsLine + "\nOther\n" + agentsLine + "\n", "Other\n" + agentsLine + "\n"},
		{"current", "Before\n" + agentsLine + "\n", "Before\n" + agentsLine + "\n"},
		{"no-final-newline", legacyAgentsLine, agentsLine},
		{"quoted", "> " + legacyAgentsLine + "\n", "> " + legacyAgentsLine + "\n\n" + agentsLine + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "AGENTS.md")
			if err := os.WriteFile(path, []byte(tc.before), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Init(root, "", false, ""); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("AGENTS.md = %q, want %q", got, tc.want)
			}
			if strings.Count(string(got), agentsLine) != 1 {
				t.Fatalf("expected one current marker: %q", got)
			}
		})
	}
}
