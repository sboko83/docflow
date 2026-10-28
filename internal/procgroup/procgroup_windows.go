//go:build windows

package procgroup

import (
	"os"
	"os/exec"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// StopSignals are the signals that ask docflow to stop a running child.
var StopSignals = []os.Signal{os.Interrupt}

// Detach is a no-op on Windows.
func Detach(cmd *exec.Cmd) {}

// Watch carries the kill-on-close job of one agent call. A nil Watch is valid
// everywhere: the kill path falls back to the taskkill tree walk and Close is
// a no-op.
type Watch struct{ job windows.Handle }

// Track starts watching the process for a later Kill: it puts the process and
// everything it spawns later into a kill-on-close job object. taskkill /T
// walks live Win32 parent links, and MSYS2 breaks them: its children are
// created through a short-lived helper, so they survive a tree kill and keep
// the call pipes open. Job membership follows process creation, not
// parenthood, so the job covers those descendants; kill-on-close also cleans
// them up when docflow itself dies.
func Track(p *os.Process) (*Watch, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(handle)
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	if err := windows.AssignProcessToJobObject(job, handle); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	return &Watch{job: job}, nil
}

// Close releases the job handle: any process still inside dies.
func (w *Watch) Close() {
	if w == nil || w.job == 0 {
		return
	}
	windows.CloseHandle(w.job)
}

// Kill ends cmd and its descendants: the job when there is one, taskkill /T /F
// when the job could not be created (it still covers native trees), and the
// bare process when taskkill fails. It then waits up to grace for done, which
// must deliver the result of cmd.Wait.
func Kill(cmd *exec.Cmd, watch *Watch, done <-chan error, grace time.Duration) error {
	if watch != nil {
		_ = windows.TerminateJobObject(watch.job, 1)
	} else if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
		_ = cmd.Process.Kill()
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		_ = cmd.Process.Kill()
		return <-done
	}
}

// Reraise ends the process after a stop signal.
func Reraise(sig os.Signal) { os.Exit(1) }
