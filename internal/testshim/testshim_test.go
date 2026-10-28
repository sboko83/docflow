package testshim

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestScriptDirectExecution(t *testing.T) {
	for _, prefix := range []string{"", "#!/bin/sh\n"} {
		name := "without-shebang"
		if prefix != "" {
			name = "with-shebang"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			Script(t, dir, "probe", prefix+"printf '%s' \"$1\"\n")
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			want := "quota: 5% used\n\"quoted\" and 'single'"
			out, err := exec.Command("probe", want).CombinedOutput()
			if err != nil {
				t.Fatalf("execute %s: %v: %s", filepath.Join(dir, "probe"), err, out)
			}
			if string(out) != want {
				t.Fatalf("output = %q, want %q", out, want)
			}
		})
	}
}
