//go:build !linux && !darwin

package main

import (
	"fmt"
	"os"
	"runtime"
)

// statDir returns the FileInfo for a directory path.
func statDir(path string) (os.FileInfo, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", path)
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
	if runtime.GOOS == "windows" {
		return fileExists(path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode()&0o100 != 0
}

// freeSpace is not implemented on this platform; doctor reports it as
// unavailable rather than guessing.
func freeSpace(string) (uint64, error) {
	return 0, fmt.Errorf("free space detection is not supported on %s", runtime.GOOS)
}

// totalMemory returns 0 when it cannot be determined.
func totalMemory() uint64 { return 0 }
