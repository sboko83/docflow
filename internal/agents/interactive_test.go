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

func TestBatchLauncherArgs(t *testing.T) {
	got, err := BatchLauncherArgs([]string{"--model", "m", "--", "first line `x`\r\n  second & third\n"})
	want := []string{"--model", "m", "--", "first line `x`   second & third"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, %v", got, err)
	}
	for _, arg := range []string{`say "hi"`, "100%PATH%"} {
		if _, err := BatchLauncherArgs([]string{arg}); err == nil {
			t.Fatalf("%q accepted", arg)
		}
	}
	if !IsBatchLauncher(`C:\npm\pi.CMD`) || !IsBatchLauncher("x.bat") || IsBatchLauncher("codex.exe") || IsBatchLauncher("/usr/bin/pi") {
		t.Fatal("IsBatchLauncher misclassified a path")
	}
}
