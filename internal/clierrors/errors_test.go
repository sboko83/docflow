package clierrors

import "testing"

func TestExitCodes(t *testing.T) {
	cases := []struct {
		name string
		err  interface{ ExitCode() int }
		want int
	}{
		{name: "default", err: DocflowError{}, want: EXIT_FORMAT},
		{name: "hook", err: NewHookError("hook failed"), want: EXIT_VERIFY},
		{name: "format", err: FormatError{Path: "plan.md", Line: 7, Text: "bad field"}, want: EXIT_FORMAT},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.ExitCode(); got != tc.want {
				t.Fatalf("ExitCode() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestFormatErrorMessage(t *testing.T) {
	err := FormatError{Path: "plan.md", Line: 7, Text: "bad field"}
	if got, want := err.Error(), "plan.md:7: error: bad field"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}
