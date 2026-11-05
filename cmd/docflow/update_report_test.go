package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"bsv-docflow/internal/release"
)

func TestFormatBytes(t *testing.T) {
	for n, want := range map[int64]string{512: "512 B", 2048: "2.0 KB", 3 << 20: "3.0 MB"} {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestUpdateReportWithoutTerminalPrintsLinesOnly(t *testing.T) {
	var out bytes.Buffer
	r := newUpdateReport(&out)
	for _, e := range []release.Event{
		{Stage: release.StageDownload, Name: "a.tar.gz", Total: 100},
		{Stage: release.StageDownload, Name: "a.tar.gz", Done: 50, Total: 100},
		{Stage: release.StageDownload, Name: "a.tar.gz", Done: 100, Total: 100},
		{Stage: release.StageVerify},
		{Stage: release.StageExtract},
	} {
		r.fetch(e)
	}
	got := out.String()
	for _, want := range []string{"update: downloading a.tar.gz (100 B)\n", "update: downloaded 100 B in ", "update: checksum ok (sha256)\n", "update: extracting\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.ContainsAny(got, "\r[") {
		t.Errorf("bar drawn without a terminal: %q", got)
	}
}

func TestUpdateReportBarOnTerminal(t *testing.T) {
	old := stderrIsTerminal
	stderrIsTerminal = func(io.Writer) bool { return true }
	defer func() { stderrIsTerminal = old }()
	var out bytes.Buffer
	r := newUpdateReport(&out)
	r.fetch(release.Event{Stage: release.StageDownload, Name: "a.tar.gz", Total: 4 << 20})
	r.fetch(release.Event{Stage: release.StageDownload, Name: "a.tar.gz", Done: 2 << 20, Total: 4 << 20})
	r.fetch(release.Event{Stage: release.StageDownload, Name: "a.tar.gz", Done: 4 << 20, Total: 4 << 20})
	r.say("next step")
	got := out.String()
	if !strings.Contains(got, "100%  4.0 MB / 4.0 MB") || !strings.HasSuffix(got, "\nupdate: next step\n") {
		t.Fatalf("bar output = %q", got)
	}
	if r.barOpen {
		t.Fatal("bar left open after the download finished")
	}
}

func TestUpdateReportEndBarBeforeError(t *testing.T) {
	old := stderrIsTerminal
	stderrIsTerminal = func(io.Writer) bool { return true }
	defer func() { stderrIsTerminal = old }()
	var out bytes.Buffer
	r := newUpdateReport(&out)
	r.fetch(release.Event{Stage: release.StageDownload, Name: "a", Total: 100})
	r.fetch(release.Event{Stage: release.StageDownload, Name: "a", Done: 10, Total: 100})
	r.endBar()
	if !strings.HasSuffix(out.String(), "\n") || r.barOpen {
		t.Fatalf("unfinished bar not terminated: %q", out.String())
	}
}

// A server that announces no size still gets the summary line in a log.
func TestUpdateReportWithoutTerminalUnknownSize(t *testing.T) {
	var out bytes.Buffer
	r := newUpdateReport(&out)
	for _, e := range []release.Event{
		{Stage: release.StageDownload, Name: "a.tar.gz", Total: -1},
		{Stage: release.StageDownload, Name: "a.tar.gz", Done: 2048, Total: -1},
		{Stage: release.StageVerify, Name: "a.tar.gz", Done: 2048, Total: -1},
	} {
		r.fetch(e)
	}
	got := out.String()
	if !strings.Contains(got, "update: downloading a.tar.gz\n") || !strings.Contains(got, "update: downloaded 2.0 KB in ") ||
		strings.Index(got, "downloaded") > strings.Index(got, "checksum ok") {
		t.Fatalf("report = %q", got)
	}
}
