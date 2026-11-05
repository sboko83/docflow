package runner

import "testing"

// skipSlow marks an integration-style test that takes several seconds on
// Windows. `go test -short` skips it; the full run (CI, acceptance) keeps it.
func skipSlow(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("slow runner integration test; skipped with -short")
	}
}
