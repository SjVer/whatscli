//go:build windows

package messages

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// serverJob holds llama-server: Windows ends it when whatscli ends, also when
// whatscli is killed, as the job is closed then
var serverJob windows.Handle

// hideWindow runs a program without the console of whatscli, and without a
// window of its own
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}

// startServerProcess starts llama-server without the console of whatscli,
// which the terminal would keep waiting for, and ends it with whatscli
func startServerProcess(cmd *exec.Cmd) error {
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	if serverJob == 0 {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return nil // it is still stopped when whatscli closes, see stopModel
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
		}
		windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
		serverJob = job
	}
	if process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid)); err == nil {
		windows.AssignProcessToJobObject(serverJob, process)
		windows.CloseHandle(process)
	}
	return nil
}
