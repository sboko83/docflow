package runner

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

func saveNotificationHooks(t *testing.T) {
	t.Helper()
	platform := currentPlatform
	run := runNotificationProcess
	t.Cleanup(func() {
		currentPlatform = platform
		runNotificationProcess = run
	})
	t.Setenv("DOCFLOW_NOTIFY", "")
}

func TestNotifyOff(t *testing.T) {
	saveNotificationHooks(t)
	calls := 0
	runNotificationProcess = func([]string, time.Duration) (bool, error) {
		calls++
		return true, nil
	}
	notify("x", true, nil)
	t.Setenv("DOCFLOW_NOTIFY", "0")
	notify("x", false, nil)
	if calls != 0 {
		t.Fatalf("notification command called %d times", calls)
	}
}

func TestNotifyDarwinAppleScript(t *testing.T) {
	saveNotificationHooks(t)
	currentPlatform = "darwin"
	var got []string
	runNotificationProcess = func(argv []string, timeout time.Duration) (bool, error) {
		got = append([]string(nil), argv...)
		if timeout != NotificationTimeout {
			t.Errorf("timeout = %s", timeout)
		}
		return true, nil
	}
	notify(`P1: ready "to close"`, false, nil)
	want := []string{"osascript", "-e", `display notification "P1: ready \"to close\"" with title "docflow"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
}

func TestNotifyLinuxNotifySend(t *testing.T) {
	saveNotificationHooks(t)
	currentPlatform = "linux"
	var got []string
	runNotificationProcess = func(argv []string, _ time.Duration) (bool, error) {
		got = append([]string(nil), argv...)
		return true, nil
	}
	notify("P1: ready to close", false, nil)
	want := []string{"notify-send", "docflow", "P1: ready to close"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
}

func TestNotifyMissingToolIsSilent(t *testing.T) {
	saveNotificationHooks(t)
	currentPlatform = "linux"
	runNotificationProcess = func([]string, time.Duration) (bool, error) {
		return false, errors.New("executable not found")
	}
	var stderr stringsBuffer
	notify("x", false, &stderr)
	if stderr.String() != "" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestNotifyMissingToolRingsTTYBell(t *testing.T) {
	saveNotificationHooks(t)
	currentPlatform = "linux"
	runNotificationProcess = func([]string, time.Duration) (bool, error) {
		return false, errors.New("executable not found")
	}
	stderr := &fakeTTY{}
	notify("P1: ready to close", false, stderr)
	if stderr.String() != "\a" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestNotifyToolFailureRingsTTYBell(t *testing.T) {
	saveNotificationHooks(t)
	currentPlatform = "linux"
	runNotificationProcess = func([]string, time.Duration) (bool, error) {
		return true, errors.New("exit status 1")
	}
	stderr := &fakeTTY{}
	notify("x", false, stderr)
	if stderr.String() != "\a" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestNotifyShortensReason(t *testing.T) {
	saveNotificationHooks(t)
	currentPlatform = "linux"
	var got []string
	runNotificationProcess = func(argv []string, _ time.Duration) (bool, error) {
		got = append([]string(nil), argv...)
		return true, nil
	}
	notify("P1 blocked (needs plan): first sentence. Second sentence.\n\nMore.", false, nil)
	if want := "P1 blocked (needs plan): first sentence."; got[2] != want {
		t.Fatalf("text = %q, want %q", got[2], want)
	}
}

func TestNotifySpawnTimeoutIsIgnored(t *testing.T) {
	saveNotificationHooks(t)
	runNotificationProcess = func([]string, time.Duration) (bool, error) {
		return true, context.DeadlineExceeded
	}
	if !spawn([]string{"notify-send"}) {
		t.Fatal("spawn returned false after a timeout")
	}
}

// TestNotifyHelperSleep is the slow notification tool of the test below; it
// only sleeps when that test starts it.
func TestNotifyHelperSleep(t *testing.T) {
	if os.Getenv("DOCFLOW_NOTIFY_HELPER") != "sleep" {
		t.Skip("helper process")
	}
	time.Sleep(10 * time.Second)
}

// A real tool killed by the timeout exits non-zero; the process runner must
// still report the deadline, or spawn rings the bell for a slow tool.
func TestNotificationProcessTimeoutReportsDeadline(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCFLOW_NOTIFY_HELPER", "sleep")
	started, err := runNotificationProcess([]string{exe, "-test.run=^TestNotifyHelperSleep$"}, 300*time.Millisecond)
	if !started || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runNotificationProcess = %v, %v; want started with a deadline error", started, err)
	}
}

type stringsBuffer struct{ text string }

func (b *stringsBuffer) Write(data []byte) (int, error) {
	b.text += string(data)
	return len(data), nil
}

func (b *stringsBuffer) String() string { return b.text }
