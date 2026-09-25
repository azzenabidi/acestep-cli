//go:build linux || darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// statDir returns the FileInfo for a directory path.
func statDir(path string) (os.FileInfo, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, &os.PathError{Op: "stat", Path: path, Err: syscall.ENOTDIR}
	}
	return fi, nil
}

// fileExists reports whether path is an existing regular file.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// executable reports whether the owner execute bit is set.
func executable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode()&0o100 != 0
}

// freeSpace reports the bytes available to this user on the volume holding
// path, walking up to the nearest existing ancestor.
func freeSpace(path string) (uint64, error) {
	probe := path
	for {
		if _, err := os.Stat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return 0, fmt.Errorf("no existing ancestor of %s", path)
		}
		probe = parent
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(probe, &st); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", probe, err)
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// totalMemory returns the machine's physical RAM in bytes, or 0 when unknown.
func totalMemory() uint64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	// "MemTotal:       16316360 kB"
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0
			}
			return kb * 1024
		}
	}
	return 0
}
