package agents

import (
	"fmt"
	"path/filepath"
	"strings"
)

// InteractiveArgv starts the agent's own terminal UI with an initial prompt.
// It deliberately does not reuse batch profiles or bypass their permissions.
func InteractiveArgv(route Route, prompt string) ([]string, error) {
	switch route.Agent {
	case "claude":
		return []string{"claude", "--model", route.Model, "--", prompt}, nil
	case "codex":
		return []string{"codex", "-m", route.Model, "--", prompt}, nil
	case "pi":
		return []string{"pi", "--model", route.Model, "--", prompt}, nil
	case "opencode":
		return []string{"opencode", "--model", route.Model, "--prompt", prompt}, nil
	default:
		return nil, fmt.Errorf("agent %q has no interactive launch", route.Agent)
	}
}

// BatchLauncherArgs adapts arguments for a Windows .cmd or .bat launcher,
// the form npm installs agent CLIs in. cmd.exe ends the command at a line
// break, so a multi-line prompt would reach the agent cut at its first
// line: line breaks become spaces. cmd.exe also expands %VAR% and does not
// understand the \" escape Go writes for a quote, so an argument with % or
// " is refused rather than passed altered or split.
func BatchLauncherArgs(args []string) ([]string, error) {
	adapted := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsAny(arg, "%\"") {
			return nil, fmt.Errorf("a .cmd launcher cannot pass %% or \" safely through cmd.exe")
		}
		lines := strings.FieldsFunc(arg, func(r rune) bool { return r == '\n' || r == '\r' })
		adapted[i] = strings.Join(lines, " ")
	}
	return adapted, nil
}

// IsBatchLauncher reports whether a resolved executable path runs through
// cmd.exe on Windows.
func IsBatchLauncher(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".cmd" || ext == ".bat"
}
