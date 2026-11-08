// Package roster reads the version 4 roster policy and its scan catalog,
// resolves a route chain for every plan role and writes the chains into a
// plan header. The runner never reads the roster: it only feeds roster
// brief (print the chains), explain (compare them with a plan) and apply
// (write them into a plan header), so a plan stays reproducible without
// the file. Older formats are read by the migration alone.
package roster

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"bsv-docflow/internal/clierrors"
)

// Roles is the fixed set of roster roles in plan-header order. The
// executor role renders as the executor:/fallback: header pair, the rest
// as role keys.
var Roles = []string{"executor", "cheap", "heavy", "review", "acceptance", "replan"}

// DefaultPath returns the roster path the way agents.ConfigPath builds the
// agents path: $XDG_CONFIG_HOME/docflow/roster.toml, ~/.config otherwise.
func DefaultPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			base = "~/.config"
		} else {
			base = filepath.Join(home, ".config")
		}
	}
	return filepath.Join(base, "docflow", "roster.toml")
}

// failAt returns a located exit-code-2 error; a line of 0 from the index
// (no entry — exotic syntax) becomes line 1, the file head.
func failAt(path string, index *lineIndex, table, key, format string, args ...any) error {
	line := index.line(table, key)
	if line == 0 {
		line = 1
	}
	return clierrors.FormatError{Path: path, Line: line, Text: fmt.Sprintf(format, args...)}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func expandUser(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}
