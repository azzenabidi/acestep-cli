package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azzenabidi/acestep-cli/pkg/config"
	"github.com/azzenabidi/acestep-cli/pkg/downloader"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"lofi synthwave with relaxing piano", "lofi-synthwave-with-relaxing-piano"},
		{"Drum & Bass, 1999!", "drum-bass-1999"},
		{"  spaced  out  ", "spaced-out"},
		{"UPPER Case", "upper-case"},
		{"emoji 🎵 test", "emoji-test"},
		{"already-a-slug", "already-a-slug"},
		{"../../etc/passwd", "etc-passwd"},
		{"a/b\\c", "a-b-c"},
		{"", ""},
		{"...", ""},
	}
	for _, tc := range tests {
		if got := slugify(tc.in); got != tc.want {
			t.Errorf("slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A slug is used as a filename, so it must never contain a path separator.
func TestSlugifyIsPathSafe(t *testing.T) {
	got := slugify("../../../etc/shadow")
	if strings.ContainsAny(got, `/\`) || strings.Contains(got, "..") {
		t.Errorf("slugify produced an unsafe filename: %q", got)
	}
}

// Inline lyrics must not be mistaken for a file path.
func TestLyricsFile(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "song.txt")
	if err := os.WriteFile(real, []byte("line one\nline two"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		in         string
		wantPath   string
		wantIsFile bool
	}{
		{"existing file", real, real, true},
		{"inline multi-word lyric", "hello, world", "", false},
		{"inline with slash that does not exist", "and/or/so", "", false},
		{"empty", "", "", false},
		{"nonexistent .txt", "missing.txt", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := lyricsFile(tc.in)
			if ok != tc.wantIsFile {
				t.Fatalf("lyricsFile(%q) ok = %v, want %v (path %q)", tc.in, ok, tc.wantIsFile, got)
			}
			if ok && got != tc.wantPath {
				t.Errorf("path = %q, want %q", got, tc.wantPath)
			}
		})
	}
}

func TestRoleOf(t *testing.T) {
	tests := map[string]string{
		"Qwen3-Embedding-0.6B-Q8_0.gguf":           "text-encoder",
		"vae-BF16.gguf":                            "vae",
		"acestep-5Hz-lm-4B-Q8_0.gguf":              "lm",
		"acestep-5Hz-lm-0.6B-Q5_K_M.gguf":          "lm",
		"acestep-v15-turbo-Q8_0.gguf":              "dit",
		"acestep-v15-xl-sft-Q8_0.gguf":             "dit",
		"acestep-v15-turbo-continuous-Q4_K_M.gguf": "dit",
		"mystery.gguf":                             "unknown",
	}
	for name, want := range tests {
		if got := roleOf(name); got != want {
			t.Errorf("roleOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	tests := map[int64]string{
		0:       "0 B",
		512:     "512 B",
		1024:    "1.0 KiB",
		1536:    "1.5 KiB",
		1 << 20: "1.0 MiB",
		1 << 30: "1.0 GiB",
	}
	for in, want := range tests {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("a\nb", 10); got != "a b" {
		t.Errorf("truncate should collapse newlines, got %q", got)
	}
	got := truncate("abcdefghij", 5)
	if len([]rune(got)) != 5 {
		t.Errorf("truncate length = %d runes, want 5: %q", len([]rune(got)), got)
	}
}

// Engine binaries are named with the platform suffix.
func TestBinaryName(t *testing.T) {
	got := config.BinaryName("ace-lm")
	want := "ace-lm"
	if !strings.HasPrefix(got, want) {
		t.Errorf("BinaryName = %q, want prefix %q", got, want)
	}
}

func TestLayoutEnsureDirs(t *testing.T) {
	l, err := config.NewLayout(t.TempDir())
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	if err := l.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, dir := range []string{l.Root, l.Bin, l.Models, l.Outputs, l.Work} {
		fi, err := os.Stat(dir)
		if err != nil || !fi.IsDir() {
			t.Errorf("%s was not created: %v", dir, err)
		}
	}
}

// ACESTEP_HOME must win over the platform default so users can relocate the
// whole installation onto another volume.
func TestLayoutHonoursEnvHome(t *testing.T) {
	want := t.TempDir()
	t.Setenv("ACESTEP_HOME", want)
	l, err := config.NewLayout("")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	if l.Root != want {
		t.Errorf("root = %q, want %q", l.Root, want)
	}
}

func TestLayoutExplicitRootWinsOverEnv(t *testing.T) {
	t.Setenv("ACESTEP_HOME", t.TempDir())
	want := t.TempDir()
	l, err := config.NewLayout(want)
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	if l.Root != want {
		t.Errorf("root = %q, want %q", l.Root, want)
	}
}

func TestPresetsResolveForEveryListedName(t *testing.T) {
	for _, name := range downloader.PresetNames() {
		models, err := downloader.Resolve(name)
		if err != nil {
			t.Errorf("Resolve(%q): %v", name, err)
			continue
		}
		if len(models) < 4 {
			t.Errorf("preset %q resolved to %d models, want at least 4", name, len(models))
		}
	}
}

func TestCmakeFlags(t *testing.T) {
	t.Run("default is a cpu build", func(t *testing.T) {
		setupCPU, setupCUDA, setupVulkan, setupROCm, setupAll = true, false, false, false, false
		flags := strings.Join(cmakeFlags(), " ")
		if !strings.Contains(flags, "GGML_NATIVE=ON") {
			t.Errorf("cpu-only flags = %q, want GGML_NATIVE=ON", flags)
		}
		for _, unwanted := range []string{"CUDA", "VULKAN", "HIP"} {
			if strings.Contains(flags, unwanted) {
				t.Errorf("cpu-only flags should not mention %s: %q", unwanted, flags)
			}
		}
	})

	t.Run("cuda", func(t *testing.T) {
		setupCPU, setupCUDA, setupVulkan, setupROCm, setupAll = true, true, false, false, false
		if !strings.Contains(strings.Join(cmakeFlags(), " "), "GGML_CUDA=ON") {
			t.Error("--cuda should enable GGML_CUDA")
		}
	})

	t.Run("vulkan", func(t *testing.T) {
		setupCPU, setupCUDA, setupVulkan, setupROCm, setupAll = false, false, true, false, false
		if !strings.Contains(strings.Join(cmakeFlags(), " "), "GGML_VULKAN=ON") {
			t.Error("--vulkan should enable GGML_VULKAN")
		}
	})

	t.Run("all", func(t *testing.T) {
		setupCPU, setupCUDA, setupVulkan, setupROCm, setupAll = true, true, true, false, true
		flags := strings.Join(cmakeFlags(), " ")
		for _, want := range []string{"GGML_CPU_ALL_VARIANTS=ON", "GGML_CUDA=ON", "GGML_VULKAN=ON", "GGML_BACKEND_DL=ON"} {
			if !strings.Contains(flags, want) {
				t.Errorf("--all flags %q missing %q", flags, want)
			}
		}
	})

	// Restore defaults for any later test.
	setupCPU, setupCUDA, setupVulkan, setupROCm, setupAll = true, false, false, false, false
}
