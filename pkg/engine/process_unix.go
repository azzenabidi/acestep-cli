//go:build unix

package engine

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// isExecutable reports whether the file may be run directly. On Unix this is
// the execute bit, which is the thing a user can actually get wrong.
func isExecutable(fi os.FileInfo) bool {
	return fi.Mode()&0o111 != 0
}

// setProcAttr puts the child in its own process group so the whole tree can be
// signalled at once. Without this, Ctrl+C in the parent terminal never reaches
// the C++ binary (it is not in the foreground group) and a cancelled run
// leaves orphaned processes holding VRAM.
func setProcAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalGroup sends sig to the child's process group, falling back to the
// child alone when the group is already gone.
func signalGroup(cmd *exec.Cmd, sig os.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	s, ok := sig.(syscall.Signal)
	if !ok {
		return cmd.Process.Signal(sig)
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return cmd.Process.Signal(sig)
	}
	// Negative pid targets the whole group. A stale group id can hit an
	// unrelated process, so guard against pgid <= 1.
	if pgid > 1 {
		if err := syscall.Kill(-pgid, s); err == nil {
			return nil
		}
	}
	return cmd.Process.Signal(sig)
}

// terminateGroup asks a still-running group to exit.
func terminateGroup(cmd *exec.Cmd) error { return signalGroup(cmd, syscall.SIGTERM) }

// killGroup is the last resort after the grace period.
func killGroup(cmd *exec.Cmd) error { return signalGroup(cmd, syscall.SIGKILL) }

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
