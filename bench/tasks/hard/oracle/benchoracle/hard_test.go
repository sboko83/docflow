package benchoracle

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// The hard oracle drives the built binary as separate processes: lock races,
// crashes and stale locks cannot be observed through an in-process run().

var ledger string

func TestMain(m *testing.M) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		os.Exit(1)
	}
	root := filepath.Dir(filepath.Dir(source))
	binDir, err := os.MkdirTemp("", "docflow-hard-oracle-")
	if err != nil {
		os.Exit(1)
	}
	ledger = filepath.Join(binDir, "ledger")
	if os.PathSeparator == '\\' {
		ledger += ".exe"
	}
	build := exec.Command("go", "build", "-o", ledger, "./cmd/ledger")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		_, _ = os.Stderr.Write(output)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(binDir)
	os.Exit(code)
}

type result struct {
	stdout, stderr string
	code           int
	elapsed        time.Duration
}

// run starts the ledger with --file path and extra environment entries; the
// lock and crash variables of the caller's environment are cleared.
func run(t *testing.T, path string, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command(ledger, append([]string{"--file", path}, args...)...)
	cmd.Env = append(cleanEnv(), env...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	r := result{stdout: stdout.String(), stderr: stderr.String(), elapsed: time.Since(start)}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		r.code = exit.ExitCode()
	default:
		t.Fatalf("run ledger: %v", err)
	}
	return r
}

func cleanEnv() []string {
	var env []string
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "LEDGER_") {
			continue
		}
		env = append(env, item)
	}
	return env
}

func journalPath(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	if contents != "" {
		write(t, path, contents)
	}
	return path
}

func write(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func age(t *testing.T, path string, by time.Duration) {
	t.Helper()
	old := time.Now().Add(-by)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func expectOK(t *testing.T, r result, stdout string) {
	t.Helper()
	if r.code != 0 || r.stdout != stdout {
		t.Fatalf("exit %d stdout %q stderr %q, want exit 0 stdout %q", r.code, r.stdout, r.stderr, stdout)
	}
}

func expectError(t *testing.T, r result) {
	t.Helper()
	if r.code != 1 || r.stderr == "" || r.stdout != "" {
		t.Fatalf("exit %d stdout %q stderr %q, want exit 1 with a diagnostic", r.code, r.stdout, r.stderr)
	}
}

const legacy = "2025-01-01\t5\ttea\n2025-01-02\t7\tbus\n"

func TestHardNewJournalFormat(t *testing.T) {
	path := journalPath(t, "")
	expectOK(t, run(t, path, nil, "add", "2025-01-02", "100", "coffee", "beans"), "Entry 1 added\n")
	expectOK(t, run(t, path, nil, "add", "2025-01-03", "50", "tea"), "Entry 2 added\n")
	if got := read(t, path); got != "# ledger-v2 next=3\n1\t2025-01-02\t100\tcoffee beans\n2\t2025-01-03\t50\ttea\n" {
		t.Fatalf("journal = %q", got)
	}
	expectOK(t, run(t, path, nil, "list"), "ID\tDATE\tAMOUNT\tDESCRIPTION\n1\t2025-01-02\t100\tcoffee beans\n2\t2025-01-03\t50\ttea\n")
	expectOK(t, run(t, path, nil, "balance"), "Balance: 150\n")
	if exists(path + ".lock") {
		t.Fatal("lock left behind")
	}
}

func TestHardEmptyJournalReads(t *testing.T) {
	path := journalPath(t, "")
	expectOK(t, run(t, path, nil, "list"), "ID\tDATE\tAMOUNT\tDESCRIPTION\n")
	expectOK(t, run(t, path, nil, "balance"), "Balance: 0\n")
	write(t, path, "# ledger-v2 next=7\n")
	expectOK(t, run(t, path, nil, "add", "2025-01-01", "1", "x"), "Entry 7 added\n")
}

func TestHardReadsLeaveLegacyJournal(t *testing.T) {
	path := journalPath(t, legacy)
	expectOK(t, run(t, path, nil, "list"), "ID\tDATE\tAMOUNT\tDESCRIPTION\n1\t2025-01-01\t5\ttea\n2\t2025-01-02\t7\tbus\n")
	expectOK(t, run(t, path, nil, "balance"), "Balance: 12\n")
	if got := read(t, path); got != legacy {
		t.Fatalf("read rewrote the journal: %q", got)
	}
}

func TestHardAddMigratesLegacyJournal(t *testing.T) {
	path := journalPath(t, legacy)
	expectOK(t, run(t, path, nil, "add", "2025-01-03", "9", "lunch"), "Entry 3 added\n")
	if got := read(t, path); got != "# ledger-v2 next=4\n1\t2025-01-01\t5\ttea\n2\t2025-01-02\t7\tbus\n3\t2025-01-03\t9\tlunch\n" {
		t.Fatalf("journal = %q", got)
	}
}

func TestHardDeleteMigratesLegacyJournal(t *testing.T) {
	path := journalPath(t, legacy+"2025-01-03\t9\tlunch\n")
	expectOK(t, run(t, path, nil, "delete", "2"), "Entry 2 deleted\n")
	if got := read(t, path); got != "# ledger-v2 next=4\n1\t2025-01-01\t5\ttea\n3\t2025-01-03\t9\tlunch\n" {
		t.Fatalf("journal = %q", got)
	}
}

func TestHardDeleteNeverReusesIDs(t *testing.T) {
	path := journalPath(t, "")
	for _, description := range []string{"a", "b", "c"} {
		if r := run(t, path, nil, "add", "2025-01-01", "1", description); r.code != 0 {
			t.Fatalf("add: %+v", r)
		}
	}
	expectOK(t, run(t, path, nil, "delete", "3"), "Entry 3 deleted\n")
	expectOK(t, run(t, path, nil, "add", "2025-01-01", "1", "d"), "Entry 4 added\n")
	expectOK(t, run(t, path, nil, "delete", "1"), "Entry 1 deleted\n")
	if got := read(t, path); got != "# ledger-v2 next=5\n2\t2025-01-01\t1\tb\n4\t2025-01-01\t1\td\n" {
		t.Fatalf("journal = %q", got)
	}
}

func TestHardDeleteRejectsBadIDs(t *testing.T) {
	path := journalPath(t, "# ledger-v2 next=3\n2\t2025-01-01\t5\ttea\n")
	before := read(t, path)
	for _, id := range []string{"1", "3", "0", "-2", "abc", "+2"} {
		expectError(t, run(t, path, nil, "delete", id))
	}
	expectError(t, run(t, path, nil, "delete"))
	if got := read(t, path); got != before {
		t.Fatalf("failed delete changed journal: %q", got)
	}
	absent := filepath.Join(t.TempDir(), "absent.tsv")
	expectError(t, run(t, absent, nil, "delete", "1"))
	if exists(absent) {
		t.Fatal("delete created the journal")
	}
}

func TestHardRejectsMalformedJournals(t *testing.T) {
	cases := map[string]string{
		"id not below next": "# ledger-v2 next=2\n2\t2025-01-01\t5\ttea\n",
		"duplicate id":      "# ledger-v2 next=5\n1\t2025-01-01\t5\ttea\n1\t2025-01-01\t5\tbus\n",
		"decreasing id":     "# ledger-v2 next=5\n3\t2025-01-01\t5\ttea\n2\t2025-01-01\t5\tbus\n",
		"other header":      "# ledger-v3\n",
		"header not first":  "2025-01-01\t5\ttea\n# ledger-v2 next=3\n",
		"zero next":         "# ledger-v2 next=0\n",
		"record without id": "# ledger-v2 next=2\n2025-01-01\t5\ttea\n",
	}
	for name, contents := range cases {
		t.Run(name, func(t *testing.T) {
			path := journalPath(t, contents)
			expectError(t, run(t, path, nil, "list"))
			expectError(t, run(t, path, nil, "balance"))
			expectError(t, run(t, path, nil, "add", "2025-01-09", "1", "x"))
			if got := read(t, path); got != contents {
				t.Fatalf("journal changed: %q", got)
			}
			if exists(path + ".lock") {
				t.Fatal("lock left behind")
			}
		})
	}
}

func TestHardConcurrentWritersKeepEveryRecord(t *testing.T) {
	path := journalPath(t, "")
	const writers = 12
	results := make([]result, writers)
	var group sync.WaitGroup
	for i := 0; i < writers; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			results[i] = run(t, path, []string{"LEDGER_LOCK_TIMEOUT=60s"}, "add", "2025-02-01", "10", fmt.Sprintf("writer-%02d", i))
		}(i)
	}
	group.Wait()
	var printed []string
	for i, r := range results {
		if r.code != 0 {
			t.Fatalf("writer %d: exit %d stderr %q", i, r.code, r.stderr)
		}
		printed = append(printed, r.stdout)
	}
	sort.Strings(printed)
	var want []string
	for i := 1; i <= writers; i++ {
		want = append(want, fmt.Sprintf("Entry %d added\n", i))
	}
	sort.Strings(want)
	if strings.Join(printed, "") != strings.Join(want, "") {
		t.Fatalf("writers printed %q", printed)
	}
	list := run(t, path, nil, "list")
	rows := strings.Split(strings.TrimSuffix(list.stdout, "\n"), "\n")
	if list.code != 0 || len(rows) != writers+1 {
		t.Fatalf("list = %q", list.stdout)
	}
	var descriptions []string
	for i, row := range rows[1:] {
		fields := strings.Split(row, "\t")
		if len(fields) != 4 || fields[0] != fmt.Sprint(i+1) {
			t.Fatalf("list = %q", list.stdout)
		}
		descriptions = append(descriptions, fields[3])
	}
	sort.Strings(descriptions)
	for i, description := range descriptions {
		if description != fmt.Sprintf("writer-%02d", i) {
			t.Fatalf("descriptions = %q", descriptions)
		}
	}
	if !strings.HasPrefix(read(t, path), fmt.Sprintf("# ledger-v2 next=%d\n", writers+1)) {
		t.Fatalf("journal = %q", read(t, path))
	}
	if exists(path + ".lock") {
		t.Fatal("lock left behind")
	}
}

func TestHardFreshLockTimesOut(t *testing.T) {
	path := journalPath(t, legacy)
	write(t, path+".lock", "held\n")
	r := run(t, path, []string{"LEDGER_LOCK_TIMEOUT=300ms"}, "add", "2025-01-09", "1", "x")
	expectError(t, r)
	if !strings.Contains(r.stderr, "locked") {
		t.Fatalf("stderr = %q, want the word locked", r.stderr)
	}
	if r.elapsed < 250*time.Millisecond {
		t.Fatalf("gave up after %v without waiting for the timeout", r.elapsed)
	}
	if got := read(t, path); got != legacy {
		t.Fatalf("journal changed: %q", got)
	}
	if got := read(t, path+".lock"); got != "held\n" {
		t.Fatalf("foreign lock changed or removed: %q", got)
	}
	r = run(t, path, []string{"LEDGER_LOCK_TIMEOUT=200ms"}, "delete", "1")
	expectError(t, r)
	if got := read(t, path); got != legacy {
		t.Fatalf("journal changed by delete: %q", got)
	}
}

func TestHardWriterWaitsForLockRelease(t *testing.T) {
	path := journalPath(t, "")
	lock := path + ".lock"
	write(t, lock, "held\n")
	done := make(chan result, 1)
	go func() {
		done <- run(t, path, []string{"LEDGER_LOCK_TIMEOUT=20s"}, "add", "2025-01-09", "1", "x")
	}()
	time.Sleep(600 * time.Millisecond)
	select {
	case r := <-done:
		t.Fatalf("writer finished while the lock was held: %+v", r)
	default:
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	r := <-done
	expectOK(t, r, "Entry 1 added\n")
	if exists(lock) {
		t.Fatal("lock left behind")
	}
}

func TestHardStaleLockIsBroken(t *testing.T) {
	path := journalPath(t, "")
	lock := path + ".lock"
	write(t, lock, "crashed\n")
	age(t, lock, time.Hour)
	expectOK(t, run(t, path, []string{"LEDGER_LOCK_TIMEOUT=2s"}, "add", "2025-01-09", "1", "x"), "Entry 1 added\n")
	if exists(lock) {
		t.Fatal("lock left behind")
	}

	write(t, lock, "recent\n")
	age(t, lock, 3*time.Second)
	expectError(t, run(t, path, []string{"LEDGER_LOCK_TIMEOUT=200ms"}, "add", "2025-01-09", "1", "y"))
	expectOK(t, run(t, path, []string{"LEDGER_LOCK_TIMEOUT=2s", "LEDGER_LOCK_STALE=1s"}, "add", "2025-01-09", "1", "y"), "Entry 2 added\n")
	if exists(lock) {
		t.Fatal("lock left behind")
	}
}

func TestHardReadsIgnoreLock(t *testing.T) {
	path := journalPath(t, legacy)
	lock := path + ".lock"
	write(t, lock, "held\n")
	for _, args := range [][]string{{"list"}, {"balance"}} {
		r := run(t, path, []string{"LEDGER_LOCK_TIMEOUT=30s"}, args...)
		if r.code != 0 || r.elapsed > 3*time.Second {
			t.Fatalf("%v under a held lock: exit %d after %v", args, r.code, r.elapsed)
		}
	}
	if got := read(t, lock); got != "held\n" {
		t.Fatalf("read touched the lock: %q", got)
	}
}

func TestHardRejectsInvalidVariables(t *testing.T) {
	for _, env := range []string{
		"LEDGER_LOCK_TIMEOUT=soon", "LEDGER_LOCK_TIMEOUT=0s", "LEDGER_LOCK_TIMEOUT=-1s",
		"LEDGER_LOCK_STALE=later", "LEDGER_LOCK_STALE=0",
		"LEDGER_CRASH=boom",
	} {
		t.Run(env, func(t *testing.T) {
			path := journalPath(t, "")
			expectError(t, run(t, path, []string{env}, "add", "2025-01-09", "1", "x"))
			if exists(path) || exists(path+".lock") {
				t.Fatal("invalid variable touched files")
			}
			legacyPath := journalPath(t, legacy)
			expectError(t, run(t, legacyPath, []string{env}, "delete", "1"))
			if got := read(t, legacyPath); got != legacy {
				t.Fatalf("journal changed: %q", got)
			}
			expectOK(t, run(t, legacyPath, []string{env}, "balance"), "Balance: 12\n")
		})
	}
}

func TestHardCrashBeforeRenameKeepsJournal(t *testing.T) {
	path := journalPath(t, "# ledger-v2 next=2\n1\t2025-01-01\t5\ttea\n")
	before := read(t, path)
	r := run(t, path, []string{"LEDGER_CRASH=before-rename"}, "add", "2025-01-02", "7", "bus")
	if r.code != 99 {
		t.Fatalf("crash exit %d stderr %q, want 99", r.code, r.stderr)
	}
	if got := read(t, path); got != before {
		t.Fatalf("crashed add changed the journal: %q", got)
	}
	if !exists(path + ".lock") {
		t.Fatal("crash removed the lock")
	}
	age(t, path+".lock", time.Hour)
	expectOK(t, run(t, path, nil, "add", "2025-01-03", "9", "lunch"), "Entry 2 added\n")
	if got := read(t, path); got != "# ledger-v2 next=3\n1\t2025-01-01\t5\ttea\n2\t2025-01-03\t9\tlunch\n" {
		t.Fatalf("journal after recovery = %q", got)
	}

	before = read(t, path)
	r = run(t, path, []string{"LEDGER_CRASH=before-rename"}, "delete", "1")
	if r.code != 99 {
		t.Fatalf("crash delete exit %d, want 99", r.code)
	}
	if got := read(t, path); got != before {
		t.Fatalf("crashed delete changed the journal: %q", got)
	}
}

func TestHardCrashDuringMigrationKeepsLegacyJournal(t *testing.T) {
	path := journalPath(t, legacy)
	r := run(t, path, []string{"LEDGER_CRASH=before-rename"}, "add", "2025-01-03", "9", "lunch")
	if r.code != 99 {
		t.Fatalf("crash exit %d, want 99", r.code)
	}
	if got := read(t, path); got != legacy {
		t.Fatalf("crashed migration changed the journal: %q", got)
	}
	expectOK(t, run(t, path, nil, "list"), "ID\tDATE\tAMOUNT\tDESCRIPTION\n1\t2025-01-01\t5\ttea\n2\t2025-01-02\t7\tbus\n")
	age(t, path+".lock", time.Hour)
	expectOK(t, run(t, path, nil, "add", "2025-01-03", "9", "lunch"), "Entry 3 added\n")
	if got := read(t, path); got != "# ledger-v2 next=4\n1\t2025-01-01\t5\ttea\n2\t2025-01-02\t7\tbus\n3\t2025-01-03\t9\tlunch\n" {
		t.Fatalf("journal after recovery = %q", got)
	}
}
