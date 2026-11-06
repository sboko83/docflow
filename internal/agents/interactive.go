package agents

import "fmt"

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
