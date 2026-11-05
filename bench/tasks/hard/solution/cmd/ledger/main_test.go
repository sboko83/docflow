package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func noEnv(string) (string, bool) { return "", false }

func runIn(t *testing.T, env map[string]string, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	lookup := func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	}
	err := run(args, &stdout, &stderr, lookup)
	return stdout.String(), err
}

func TestRunAddsListsDeletesAndTotals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	for i, want := range []string{"Entry 1 added\n", "Entry 2 added\n"} {
		out, err := runIn(t, nil, "--file", path, "add", "2025-01-0"+fmt.Sprint(i+2), "350", "coffee", "beans")
		if err != nil || out != want {
			t.Fatalf("add = %q, %v", out, err)
		}
	}
	out, err := runIn(t, nil, "--file", path, "delete", "1")
	if err != nil || out != "Entry 1 deleted\n" {
		t.Fatalf("delete = %q, %v", out, err)
	}
	out, err = runIn(t, nil, "--file", path, "list")
	if err != nil || out != "ID\tDATE\tAMOUNT\tDESCRIPTION\n2\t2025-01-03\t350\tcoffee beans\n" {
		t.Fatalf("list = %q, %v", out, err)
	}
	out, err = runIn(t, nil, "--file", path, "balance")
	if err != nil || out != "Balance: 350\n" {
		t.Fatalf("balance = %q, %v", out, err)
	}
}

func TestRunRejectsInvalidInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	cases := []struct {
		env  map[string]string
		args []string
	}{
		{nil, []string{"--file", path}},
		{nil, []string{"--file", path, "remove"}},
		{nil, []string{"--file", path, "add", "2025-01-01", "3"}},
		{nil, []string{"--file", path, "delete"}},
		{nil, []string{"--file", path, "delete", "0"}},
		{nil, []string{"--file", path, "delete", "1"}},
		{map[string]string{"LEDGER_LOCK_TIMEOUT": "soon"}, []string{"--file", path, "add", "2025-01-01", "3", "x"}},
		{map[string]string{"LEDGER_LOCK_STALE": "0s"}, []string{"--file", path, "add", "2025-01-01", "3", "x"}},
		{map[string]string{"LEDGER_CRASH": "boom"}, []string{"--file", path, "add", "2025-01-01", "3", "x"}},
	}
	for _, tc := range cases {
		if out, err := runIn(t, tc.env, tc.args...); err == nil || out != "" {
			t.Fatalf("run(%q, %v) = %q, %v", tc.args, tc.env, out, err)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed commands created the journal: %v", err)
	}
}

func TestRunReadsIgnoreLockVariables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	if out, err := runIn(t, map[string]string{"LEDGER_CRASH": "boom"}, "--file", path, "balance"); err != nil || out != "Balance: 0\n" {
		t.Fatalf("balance = %q, %v", out, err)
	}
}

func TestRunRequiresConfiguredPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"balance"}, &stdout, &stderr, noEnv)
	if err == nil || !strings.Contains(err.Error(), "--file or LEDGER_FILE") {
		t.Fatalf("run error = %v", err)
	}
}

// buildLedger compiles the CLI so tests can run it as separate processes.
func buildLedger(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "ledger")
	if os.PathSeparator == '\\' {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return binary
}

func runBinary(binary string, env []string, args ...string) (string, int, error) {
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return stdout.String(), exit.ExitCode(), nil
	}
	return stdout.String(), 0, err
}

func TestBinaryConcurrentAddsKeepEveryRecord(t *testing.T) {
	binary := buildLedger(t)
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	const writers = 12
	var group sync.WaitGroup
	codes := make([]int, writers)
	for i := 0; i < writers; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			_, code, err := runBinary(binary, []string{"LEDGER_LOCK_TIMEOUT=60s"}, "--file", path, "add", "2025-02-01", "10", fmt.Sprintf("writer-%02d", i))
			if err != nil {
				code = -1
			}
			codes[i] = code
		}(i)
	}
	group.Wait()
	for i, code := range codes {
		if code != 0 {
			t.Fatalf("writer %d exit %d", i, code)
		}
	}
	out, code, err := runBinary(binary, nil, "--file", path, "list")
	if err != nil || code != 0 {
		t.Fatalf("list exit %d: %v", code, err)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")[1:]
	var ids, descriptions []string
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		ids = append(ids, fields[0])
		descriptions = append(descriptions, fields[3])
	}
	sort.Strings(descriptions)
	for i := 0; i < writers; i++ {
		if ids[i] != fmt.Sprint(i+1) || descriptions[i] != fmt.Sprintf("writer-%02d", i) {
			t.Fatalf("list = %q", out)
		}
	}
	if _, err := os.Stat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock left behind: %v", err)
	}
}

func TestBinaryCrashDuringMigrationRecovers(t *testing.T) {
	binary := buildLedger(t)
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	legacy := "2025-01-01\t5\ttea\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	_, code, err := runBinary(binary, []string{"LEDGER_CRASH=before-rename"}, "--file", path, "add", "2025-01-02", "7", "bus")
	if err != nil || code != 99 {
		t.Fatalf("crash exit %d: %v", code, err)
	}
	if data, _ := os.ReadFile(path); string(data) != legacy {
		t.Fatalf("journal changed: %q", data)
	}
	out, code, err := runBinary(binary, nil, "--file", path, "list")
	if err != nil || code != 0 || out != "ID\tDATE\tAMOUNT\tDESCRIPTION\n1\t2025-01-01\t5\ttea\n" {
		t.Fatalf("list under leftover lock = %q, %d, %v", out, code, err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path+".lock", old, old); err != nil {
		t.Fatal(err)
	}
	out, code, err = runBinary(binary, nil, "--file", path, "add", "2025-01-02", "7", "bus")
	if err != nil || code != 0 || out != "Entry 2 added\n" {
		t.Fatalf("recovery add = %q, %d, %v", out, code, err)
	}
	want := "# ledger-v2 next=3\n1\t2025-01-01\t5\ttea\n2\t2025-01-02\t7\tbus\n"
	if data, _ := os.ReadFile(path); string(data) != want {
		t.Fatalf("journal = %q, want %q", data, want)
	}
}
