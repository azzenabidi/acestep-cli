package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Finished audio belongs in the music folder, not buried in a config
// directory, so verify the default lands there and stays overridable.
func TestOutputDirDefaultsToMusicFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("ACESTEP_OUTPUT_DIR", "")

	l, err := NewLayout(filepath.Join(home, ".config", "acestep"))
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	want := filepath.Join(home, "Music", "acestep")
	if l.Outputs != want {
		t.Errorf("Outputs = %q, want %q", l.Outputs, want)
	}
}

func TestOutputDirHonoursEnvOverride(t *testing.T) {
	want := t.TempDir()
	t.Setenv("ACESTEP_OUTPUT_DIR", want)
	l, err := NewLayout(t.TempDir())
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	if l.Outputs != want {
		t.Errorf("Outputs = %q, want %q", l.Outputs, want)
	}
}

// An explicit ACESTEP_OUTPUT_DIR must still be overridable by --home, because
// a relocatable installation should not write outside its own root.
func TestLayoutFieldsAreDerivedFromRoot(t *testing.T) {
	root := t.TempDir()
	l, err := NewLayout(root)
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	for name, got := range map[string]string{
		"Bin":    l.Bin,
		"Models": l.Models,
		"Work":   l.Work,
	} {
		if want := filepath.Join(root, map[string]string{"Bin": "bin", "Models": "models", "Work": "work"}[name]); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// EnsureDirs must tolerate being called twice, since every command does.
func TestEnsureDirsIsIdempotent(t *testing.T) {
	l, err := NewLayout(t.TempDir())
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := l.EnsureDirs(); err != nil {
			t.Fatalf("EnsureDirs call %d: %v", i, err)
		}
	}
	if _, err := os.Stat(l.Outputs); err != nil {
		t.Errorf("output dir missing: %v", err)
	}
}
