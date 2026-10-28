//go:build windows

package procgroup

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// StopSignals are the signals that ask docflow to stop a running child.
var StopSignals = []os.Signal{os.Interrupt}

// Detach is a no-op on Windows.
func Detach(cmd *exec.Cmd) {}

// Start assigns the job before the child can spawn descendants. Assigning an
// already running process does not bring its existing children into the job.
func Start(cmd *exec.Cmd) (*Watch, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	flags := cmd.SysProcAttr.CreationFlags
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	err := cmd.Start()
	cmd.SysProcAttr.CreationFlags = flags
	if err != nil {
		return nil, err
	}
	watch, _ := Track(cmd.Process) // Preserve taskkill fallback if jobs are unavailable.
	if err := resume(cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		watch.Close()
		_ = cmd.Wait()
		return nil, fmt.Errorf("resume child: %w", err)
	}
	return watch, nil
}

// Go closes CreateProcess's primary thread handle. Find that thread while the
// process is still suspended, before any child code or descendants can run.
func resume(pid int) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != uint32(pid) {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		return err
	}
	return err
}

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
