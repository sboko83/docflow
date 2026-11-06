package agents

import (
	"reflect"
	"testing"
)

func TestInteractiveArgv(t *testing.T) {
	const prompt = "возьми строку `bsv-docflow-plan`: first\nsecond; $(do not run)"
	for _, tc := range []struct {
		agent string
		want  []string
	}{
		{"claude", []string{"claude", "--model", "model", "--", prompt}},
		{"codex", []string{"codex", "-m", "model", "--", prompt}},
		{"pi", []string{"pi", "--model", "model", "--", prompt}},
		{"opencode", []string{"opencode", "--model", "model", "--prompt", prompt}},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			got, err := InteractiveArgv(Route{Agent: tc.agent, Model: "model", Effort: "high"}, prompt)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("argv = %#v, %v", got, err)
			}
		})
	}
	if _, err := InteractiveArgv(Route{Agent: "custom"}, prompt); err == nil {
		t.Fatal("unknown agent accepted")
	}
}
