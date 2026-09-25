//go:build unix

package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file the user forgot to chmod must be reported as unrunnable rather than
// failing later with an opaque exec error.
func TestRequireFileRejectsNonExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ace-lm")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := requireFile(path, StageLM)
	if err == nil {
		t.Fatal("a mode 0644 file should not be accepted as an engine binary")
	}
	if !strings.Contains(err.Error(), "not executable") {
		t.Errorf("error = %v, want it to mention the missing execute bit", err)
	}
	if _, ok := err.(*ErrMissingBinary); ok {
		t.Errorf("error = %T, want a plain error; the file does exist", err)
	}
}

func TestRequireFileAcceptsExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ace-lm")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := requireFile(path, StageLM); err != nil {
		t.Errorf("requireFile on a 0755 file = %v, want nil", err)
	}
}

// A directory must never be mistaken for a binary, whatever its mode.
func TestRequireFileRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if _, ok := requireFile(dir, StageLM).(*ErrMissingBinary); !ok {
		t.Error("a directory should be reported as a missing binary")
	}
}

func TestRequireFileRejectsAbsentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope")
	if _, ok := requireFile(path, StageSynth).(*ErrMissingBinary); !ok {
		t.Error("an absent path should be reported as a missing binary")
	}
}
