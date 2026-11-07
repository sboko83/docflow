package roster

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// docPolicyExamples returns the full roster.toml examples of a document:
// every toml fence that carries the version marker and the [role] table.
// The state and variants examples start with version = 2 too but have no
// roles, so they are not mistaken for a policy.
func docPolicyExamples(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var examples []string
	var current []string
	inFence := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "```") {
			if inFence {
				block := strings.Join(current, "\n")
				if strings.Contains(block, fmt.Sprintf("version = %d", PolicyVersion)) && strings.Contains(block, "[role]") {
					examples = append(examples, block)
				}
				current = nil
				inFence = false
				continue
			}
			inFence = strings.TrimSpace(strings.TrimPrefix(line, "```")) == "toml"
			continue
		}
		if inFence {
			current = append(current, line)
		}
	}
	return examples
}

// TestDocPolicyExamples parses every full policy example of the design and
// usage docs and resolves it against the isolated fixture catalog on a
// pinned day: an example naming an undeclared access or an invalid preset
// fails the build instead of waiting for a reader to copy it.
func TestDocPolicyExamples(t *testing.T) {
	catalog, err := LoadCatalog("testdata/catalog-scenario.json")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	for _, path := range []string{"../../docs/design/roster.md", "../../docs/usage.md"} {
		examples := docPolicyExamples(t, path)
		if len(examples) == 0 {
			t.Fatalf("%s has no full policy example to check", path)
		}
		for n, text := range examples {
			policy, err := ParsePolicy(path, text)
			if err != nil {
				t.Errorf("%s example %d: %v", path, n+1, err)
				continue
			}
			presets := append([]string{""}, policy.PresetNames()...)
			for _, preset := range presets {
				state := NewState("")
				state.Preset = preset
				brief, err := buildBriefAt(policy, state, catalog, nil, day)
				if err != nil {
					t.Errorf("%s example %d preset %q: %v", path, n+1, preset, err)
					continue
				}
				if brief.Header()["executor"] == "" {
					t.Errorf("%s example %d preset %q resolves no executor route", path, n+1, preset)
				}
			}
		}
	}
}
