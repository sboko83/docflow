package runner

import (
	"context"
	"errors"
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

func TestNotifySpawnTimeoutIsIgnored(t *testing.T) {
	saveNotificationHooks(t)
	runNotificationProcess = func([]string, time.Duration) (bool, error) {
		return true, context.DeadlineExceeded
	}
	if !spawn([]string{"notify-send"}) {
		t.Fatal("spawn returned false after a timeout")
	}
}

type stringsBuffer struct{ text string }

func (b *stringsBuffer) Write(data []byte) (int, error) {
	b.text += string(data)
	return len(data), nil
}

func (b *stringsBuffer) String() string { return b.text }
