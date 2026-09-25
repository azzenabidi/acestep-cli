//go:build windows

package engine

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// isExecutable always reports true. Windows has no execute permission bit:
// a file with a recognised extension runs regardless of its mode, so
// checking mode bits here would reject every binary on the platform.
func isExecutable(_ os.FileInfo) bool {
	return true
}

// setProcAttr gives the child its own console process group so Ctrl+C is not
// delivered to it twice, and so CREATE_NEW_PROCESS_GROUP lets us terminate
// the whole tree later.
func setProcAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// signalGroup has no process-group concept on Windows. os.Process.Signal only
// supports Kill, so anything other than a hard kill is a no-op here and the
// grace period effectively collapses.
func signalGroup(cmd *exec.Cmd, sig os.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	if sig == os.Kill {
		return killTree(cmd.Process.Pid)
	}
	return nil
}

func terminateGroup(cmd *exec.Cmd) error { return cmd.Process.Kill() }

// killTree force-kills the child and any descendants via taskkill /T, which is
// the only reliable way to avoid leaving orphaned engine processes on Windows.
func killTree(pid int) error {
	// taskkill returns non-zero when the process already exited, which is fine.
	kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := kill.Run(); err == nil {
		return nil
	}
	return nil
}

func killGroup(cmd *exec.Cmd) error { return killTree(cmd.Process.Pid) }

// exitCode extracts a conventional exit status from a finished process.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
