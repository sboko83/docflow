package cycle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func verifyPlan() string {
	first := strings.Replace(cycleUnit("P001.1.1", "~"), "Verify: `true`", "Verify: `test -f a.py && echo checked`", 1)
	second := strings.Replace(cycleUnit("P001.1.2", " "), "Verify: `true`", "Verify: `true`", 1)
	return strings.Replace(cyclePlan("P001", "active", first, second), "- решение — почему — альтернатива\n", "", 1)
}

func makeVerifyProject(t *testing.T, docflow, plan string, files map[string]string) string {
	t.Helper()
	return makeCycleProject(t, docflow, map[string]string{"P001": plan}, files)
}

func runVerify(root, unitID string, skip *string, noGate bool) (Result, error) {
	return Verify(root, unitID, skip, noGate, nil, nil)
}

func TestStateDir(t *testing.T) {
	root := makeVerifyProject(t, cycleDocflow("P001"), verifyPlan(), map[string]string{"a.py": "print(1)\n"})
	gitDir, err := filepath.EvalSymlinks(filepath.Join(root, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(gitDir, "docflow", "verified")
	got, err := stateDir(root)
	if err != nil || got != want {
		t.Fatalf("stateDir() = %q, %v; want %q", got, err, want)
	}
	stats, err := statsDir(root)
	if err != nil || stats != filepath.Join(gitDir, "docflow", "stats") {
		t.Fatalf("statsDir() = %q, %v", stats, err)
	}
}

func TestStatsReadWriteAndMissing(t *testing.T) {
	root := makeVerifyProject(t, cycleDocflow("P001"), verifyPlan(), map[string]string{"a.py": "print(1)\n"})
	if got := readStats(root, "P001.1.1"); len(got) != 0 {
		t.Fatalf("missing stats = %#v", got)
	}
	want := map[string]any{"unit": "P001.1.1", "proxy_tokens": float64(123), "prompted_at": "2026-10-22T00:00:00Z"}
	if err := writeStats(root, "P001.1.1", want); err != nil {
		t.Fatal(err)
	}
	got := readStats(root, "P001.1.1")
	if got["unit"] != want["unit"] || got["proxy_tokens"] != want["proxy_tokens"] || got["prompted_at"] != want["prompted_at"] {
		t.Fatalf("read stats = %#v, want %#v", got, want)
	}
	if err := os.WriteFile(filepath.Join(mustStatsDir(t, root), "broken.json"), []byte("[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readStats(root, "broken"); len(got) != 0 {
		t.Fatalf("non-object stats = %#v", got)
	}
}

func mustStatsDir(t *testing.T, root string) string {
	t.Helper()
	dir, err := statsDir(root)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestVerifySuccessAndRunsFromRoot(t *testing.T) {
	root := makeVerifyProject(t, cycleDocflow("P001"), verifyPlan(), map[string]string{"a.py": "print(2)\n"})
	result, err := runVerify(root, "P001.1.1", nil, false)
	if err != nil || result.Code != 0 {
		t.Fatalf("Verify() = %#v, %v", result, err)
	}
	want := "P001.1.1: verify passed: test -f a.py && echo checked\n"
	if result.Stdout != want || result.Stderr != "" {
		t.Fatalf("Verify() = %#v, want stdout %q", result, want)
	}
	digest, err := fingerprint(root, "docs/plans/P001-plan.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	saved, found, err := savedFingerprint(root, "P001.1.1")
	if err != nil || !found || saved != digest {
		t.Fatalf("saved fingerprint = %q, %v; want %q", saved, err, digest)
	}
	reason, err := skipReason(root, "P001.1.1")
	if err != nil || reason != nil {
		t.Fatalf("skip reason = %v, %v; want nil", reason, err)
	}
}

func TestVerifyPreflightRunsFirstAndFailureClearsSavedResult(t *testing.T) {
	docflow := strings.Replace(cycleDocflow("P001"), "read_budget: 40000\n", "read_budget: 40000\npreflight: test -f .git/lint.ok\n", 1)
	plan := strings.Replace(verifyPlan(), "Verify: `test -f a.py && echo checked`", "Verify: `touch .git/verify-ran`", 1)
	root := makeVerifyProject(t, docflow, plan, map[string]string{"a.py": "print(1)\n"})
	digest, err := fingerprint(root, "docs/plans/P001-plan.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveVerified(root, "P001.1.1", digest, nil); err != nil {
		t.Fatal(err)
	}
	result, err := runVerify(root, "P001.1.1", nil, false)
	if err != nil || result.Code != 4 || !strings.Contains(result.Stdout, "preflight failed") {
		t.Fatalf("preflight failure = %#v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "verify-ran")); !os.IsNotExist(err) {
		t.Fatalf("Verify ran before preflight, stat error = %v", err)
	}
	if saved, found, err := savedFingerprint(root, "P001.1.1"); err != nil || found || saved != "" {
		t.Fatalf("saved result after failure = %q, %v", saved, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "lint.ok"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err = runVerify(root, "P001.1.1", nil, false)
	if err != nil || result.Code != 0 {
		t.Fatalf("Verify() after preflight = %#v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "verify-ran")); err != nil {
		t.Fatalf("verify command did not run: %v", err)
	}
}

func TestVerifyFailureTailAndCleanup(t *testing.T) {
	plan := verifyPlan()
	command := `i=1; while [ "$i" -le 100 ]; do echo line$i; i=$((i+1)); done; exit 3`
	plan = strings.Replace(plan, "Verify: `true`", "Verify: `"+command+"`", 1)
	root := makeVerifyProject(t, cycleDocflow("P001"), plan, map[string]string{"a.py": "print(1)\n"})
	oldReason := "old"
	if _, err := runVerify(root, "P001.1.2", &oldReason, false); err != nil {
		t.Fatal(err)
	}
	result, err := runVerify(root, "P001.1.2", nil, false)
	if err != nil || result.Code != 4 {
		t.Fatalf("Verify() = %#v, %v", result, err)
	}
	lines := strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n")
	if len(lines) != verifyTail+1 || lines[0] != "line61" || lines[verifyTail-1] != "line100" || !strings.Contains(lines[verifyTail], "verify failed (exit 3)") {
		t.Fatalf("failure output tail = %d lines, first=%q last=%q", len(lines), lines[0], lines[len(lines)-1])
	}
	if saved, found, err := savedFingerprint(root, "P001.1.2"); err != nil || found || saved != "" {
		t.Fatalf("saved fingerprint after failure = %q, %v", saved, err)
	}
	if reason, err := skipReason(root, "P001.1.2"); err != nil || reason != nil {
		t.Fatalf("skip reason after failure = %v, %v", reason, err)
	}
}

func TestSplitCommandLinesMatchesPythonSplitlines(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"one\n", []string{"one"}},
		{"\n", []string{""}},
		{"one\r\ntwo\rthree", []string{"one", "two", "three"}},
		{"one\v\ftwo\x1c\x1d\x1e\u0085\u2028\u2029three", []string{"one", "", "two", "", "", "", "", "", "three"}},
	}
	for _, test := range tests {
		got := splitCommandLines(test.input)
		if len(got) != len(test.want) {
			t.Errorf("splitCommandLines(%q) = %#v, want %#v", test.input, got, test.want)
			continue
		}
		for index := range got {
			if got[index] != test.want[index] {
				t.Errorf("splitCommandLines(%q) = %#v, want %#v", test.input, got, test.want)
				break
			}
		}
	}
}

func TestVerifySkipAndPassClearsSkip(t *testing.T) {
	root := makeVerifyProject(t, cycleDocflow("P001"), verifyPlan(), map[string]string{"a.py": "print(1)\n"})
	reason := "нет сети"
	result, err := runVerify(root, "P001.1.2", &reason, false)
	if err != nil || result.Code != 0 || result.Stdout != "P001.1.2: verify skipped: нет сети\n" {
		t.Fatalf("Verify(skip) = %#v, %v", result, err)
	}
	digest, err := fingerprint(root, "docs/plans/P001-plan.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	saved, found, err := savedFingerprint(root, "P001.1.2")
	if err != nil || !found || saved != digest {
		t.Fatalf("saved skip fingerprint = %q, %v; want %q", saved, err, digest)
	}
	gotReason, err := skipReason(root, "P001.1.2")
	if err != nil || gotReason == nil || *gotReason != reason {
		t.Fatalf("saved skip reason = %v, %v", gotReason, err)
	}
	blank := "  \t"
	if _, err := runVerify(root, "P001.1.2", &blank, false); err == nil || err.Error() != "--skip needs a reason" {
		t.Fatalf("empty skip error = %v", err)
	}
	if _, err := runVerify(root, "P001.1.1", &reason, false); err != nil {
		t.Fatal(err)
	}
	if _, err := runVerify(root, "P001.1.1", nil, false); err != nil {
		t.Fatal(err)
	}
	gotReason, err = skipReason(root, "P001.1.1")
	if err != nil || gotReason != nil {
		t.Fatalf("skip reason after passing verify = %v, %v", gotReason, err)
	}
}

func TestFingerprintPlanTrackedUntrackedAndIgnoredFiles(t *testing.T) {
	root := makeVerifyProject(t, cycleDocflow("P001"), verifyPlan(), map[string]string{"a.py": "print(1)\n", ".gitignore": "build/\n"})
	before, err := fingerprint(root, "docs/plans/P001-plan.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(root, "docs", "plans", "P001-plan.md")
	plan, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte(strings.Replace(string(plan), "[~]", "[x]", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := fingerprint(root, "docs/plans/P001-plan.md", nil); err != nil || got != before {
		t.Fatalf("plan changed fingerprint: %q, %v; want %q", got, err, before)
	}
	if err := os.WriteFile(filepath.Join(root, "a.py"), []byte("print(2)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracked, err := fingerprint(root, "docs/plans/P001-plan.md", nil)
	if err != nil || tracked == before {
		t.Fatalf("tracked edit fingerprint = %q, %v; before %q", tracked, err, before)
	}
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	untracked, err := fingerprint(root, "docs/plans/P001-plan.md", nil)
	if err != nil || untracked == tracked {
		t.Fatalf("untracked fingerprint = %q, %v; tracked %q", untracked, err, tracked)
	}
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := fingerprint(root, "docs/plans/P001-plan.md", nil)
	if err != nil || changed == untracked {
		t.Fatalf("changed untracked fingerprint = %q, %v; previous %q", changed, err, untracked)
	}
	if err := os.MkdirAll(filepath.Join(root, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "build", "out.bin"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := fingerprint(root, "docs/plans/P001-plan.md", nil); err != nil || got != changed {
		t.Fatalf("ignored file changed fingerprint: %q, %v; want %q", got, err, changed)
	}
}

func TestFingerprintUntrackedNamesBytesAndSymlink(t *testing.T) {
	root := makeVerifyProject(t, cycleDocflow("P001"), verifyPlan(), map[string]string{"a.py": "print(1)\n"})
	if err := os.Symlink("a.py", filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary"), []byte{0, 1, 0xff, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := fingerprint(root, "docs/plans/P001-plan.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.py", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	second, err := fingerprint(root, "docs/plans/P001-plan.md", nil)
	if err != nil || second != first {
		t.Fatalf("same symlink fingerprint = %q, %v; want %q", second, err, first)
	}
	if err := os.Remove(filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other-target", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	third, err := fingerprint(root, "docs/plans/P001-plan.md", nil)
	if err != nil || third == first {
		t.Fatalf("changed symlink fingerprint = %q, %v; want different from %q", third, err, first)
	}
}

func TestFingerprintNestedRepository(t *testing.T) {
	docflow := strings.Replace(cycleDocflow("P001"), "read_budget: 40000\n", "read_budget: 40000\nrepos: deps/*\n", 1)
	root := makeVerifyProject(t, docflow, verifyPlan(), map[string]string{"a.py": "print(1)\n", ".gitignore": "deps/module/\n"})
	nested := filepath.Join(root, "deps", "module")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCycleDir(t, nested, "init", "-q")
	gitCycleDir(t, nested, "config", "user.name", "docflow test")
	gitCycleDir(t, nested, "config", "user.email", "test@example.invalid")
	writeCycleFile(t, nested, "tracked.txt", "initial\n")
	gitCycleDir(t, nested, "add", "-A")
	gitCycleDir(t, nested, "commit", "-q", "-m", "initial")
	projectRepos := func(string) ([]string, error) { return []string{"deps/module"}, nil }
	before, err := fingerprint(root, "docs/plans/P001-plan.md", projectRepos)
	if err != nil {
		t.Fatal(err)
	}
	writeCycleFile(t, nested, "tracked.txt", "changed\n")
	after, err := fingerprint(root, "docs/plans/P001-plan.md", projectRepos)
	if err != nil || after == before {
		t.Fatalf("nested edit fingerprint = %q, %v; before %q", after, err, before)
	}
}

func TestVerifyWorktreeHasOwnState(t *testing.T) {
	root := makeVerifyProject(t, cycleDocflow("P001"), verifyPlan(), map[string]string{"a.py": "print(1)\n"})
	worktree := filepath.Join(t.TempDir(), "worktree")
	gitCycle(t, root, "worktree", "add", "-q", worktree)
	defer gitCycle(t, root, "worktree", "remove", "--force", worktree)
	rootState, err := stateDir(root)
	if err != nil {
		t.Fatal(err)
	}
	worktreeState, err := stateDir(worktree)
	if err != nil || worktreeState == rootState {
		t.Fatalf("worktree state = %q, %v; root state %q", worktreeState, err, rootState)
	}
	result, err := runVerify(worktree, "P001.1.1", nil, false)
	if err != nil || result.Code != 0 {
		t.Fatalf("Verify(worktree) = %#v, %v", result, err)
	}
	if saved, found, err := savedFingerprint(root, "P001.1.1"); err != nil || found || saved != "" {
		t.Fatalf("root saved state = %q, %v; want absent", saved, err)
	}
	if saved, found, err := savedFingerprint(worktree, "P001.1.1"); err != nil || !found || saved == "" {
		t.Fatalf("worktree saved state = %q, %v", saved, err)
	}
}

func TestVerifyUnknownUnit(t *testing.T) {
	root := makeVerifyProject(t, cycleDocflow("P001"), verifyPlan(), map[string]string{"a.py": "print(1)\n"})
	_, err := runVerify(root, "P001.1.9", nil, false)
	if err == nil || err.Error() != "docs/plans/P001-plan.md: no unit P001.1.9" {
		t.Fatalf("unknown unit error = %v", err)
	}
}

func TestVerifyGate(t *testing.T) {
	gate := "test -f gate.ok && echo gated"
	plan := strings.Replace(verifyPlan(), "status: active\n", "status: active\ngate: "+gate+"\n", 1)
	root := makeVerifyProject(t, cycleDocflow("P001"), plan, map[string]string{"a.py": "print(1)\n"})
	result, err := runVerify(root, "P001.1.1", nil, false)
	if err != nil || result.Code != 4 || !strings.Contains(result.Stdout, "P001.1.1: gate failed (exit 1): "+gate) {
		t.Fatalf("gate failure = %#v, %v", result, err)
	}
	if saved, found, err := savedFingerprint(root, "P001.1.1"); err != nil || found || saved != "" {
		t.Fatalf("saved fingerprint after gate failure = %q, %v", saved, err)
	}
	if err := os.WriteFile(filepath.Join(root, "gate.ok"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err = runVerify(root, "P001.1.1", nil, false)
	if err != nil || result.Code != 0 || !strings.HasSuffix(result.Stdout, "P001.1.1: gate passed: "+gate+"\n") {
		t.Fatalf("gate success = %#v, %v", result, err)
	}
	if saved, found, err := savedFingerprint(root, "P001.1.1"); err != nil || !found || saved == "" {
		t.Fatalf("saved fingerprint after gate success = %q, %v", saved, err)
	}
}

func TestVerifyNoGateDoesNotRecord(t *testing.T) {
	gate := "test -f gate.ok"
	plan := strings.Replace(verifyPlan(), "status: active\n", "status: active\ngate: "+gate+"\n", 1)
	root := makeVerifyProject(t, cycleDocflow("P001"), plan, map[string]string{"a.py": "print(1)\n", "gate.ok": ""})
	if _, err := runVerify(root, "P001.1.1", nil, false); err != nil {
		t.Fatal(err)
	}
	result, err := runVerify(root, "P001.1.1", nil, true)
	if err != nil || result.Code != 0 || !strings.Contains(result.Stdout, "gate skipped, not recorded") {
		t.Fatalf("Verify(--no-gate) = %#v, %v", result, err)
	}
	if saved, found, err := savedFingerprint(root, "P001.1.1"); err != nil || found || saved != "" {
		t.Fatalf("saved fingerprint after --no-gate = %q, %v", saved, err)
	}
}

func TestVerifyFingerprintSortsNamesAsBytes(t *testing.T) {
	got := sortedUntrackedNames([]byte{'z', 0, 'n', 0xff, 0, 'a', 0})
	want := []string{"a", "n\xff", "z"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("sorted untracked names = %#v, want %#v", got, want)
	}
	root := makeVerifyProject(t, cycleDocflow("P001"), verifyPlan(), map[string]string{"a.py": "print(1)\n"})
	for _, name := range [][]byte{{'z'}, {'a'}, {'n'}} {
		path := filepath.Join(root, string(name))
		if err := os.WriteFile(path, []byte(fmt.Sprintf("%x", name)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	first, err := fingerprint(root, "docs/plans/P001-plan.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fingerprint(root, "docs/plans/P001-plan.md", nil)
	if err != nil || second != first {
		t.Fatalf("repeated fingerprint = %q, %v; want %q", second, err, first)
	}
}

func TestFingerprintNonUTF8Filename(t *testing.T) {
	root := makeVerifyProject(t, cycleDocflow("P001"), verifyPlan(), map[string]string{"a.py": "print(1)\n"})
	name := string([]byte{'n', 0xff})
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Skipf("filesystem rejects non-UTF-8 file names: %v", err)
	}
	first, err := fingerprint(root, "docs/plans/P001-plan.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := fingerprint(root, "docs/plans/P001-plan.md", nil)
	if err != nil || first == second {
		t.Fatalf("fingerprint did not include non-UTF-8 name: first=%q second=%q err=%v", first, second, err)
	}
}
