// Package config resolves on-disk locations and persistent settings.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/viper"
)

// Layout holds every directory the CLI writes to.
type Layout struct {
	Root    string
	Bin     string
	Models  string
	Outputs string
	Work    string
}

// NewLayout resolves the directory layout. An explicit root wins; otherwise
// the env var ACESTEP_HOME, then the platform data dir, then ~/.acestep.
func NewLayout(root string) (Layout, error) {
	if root == "" {
		root = os.Getenv("ACESTEP_HOME")
	}
	if root == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			root = filepath.Join(dir, "acestep")
		}
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Layout{}, fmt.Errorf("locate home directory: %w", err)
		}
		root = filepath.Join(home, ".acestep")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Layout{}, fmt.Errorf("resolve %s: %w", root, err)
	}
	layout := Layout{
		Root:    abs,
		Bin:     filepath.Join(abs, "bin"),
		Models:  filepath.Join(abs, "models"),
		Outputs: defaultOutputDir(),
		Work:    filepath.Join(abs, "work"),
	}
	// A home directory we could not resolve leaves the output path empty;
	// fall back inside the root so the layout is always complete.
	if layout.Outputs == "" {
		layout.Outputs = filepath.Join(abs, "outputs")
	}
	return layout, nil
}

// defaultOutputDir puts finished tracks in the user's music folder, where they
// are easy to find and easy for a desktop to index. Audio in a config
// directory would be surprising and easy to lose on cleanup.
func defaultOutputDir() string {
	if dir := os.Getenv("ACESTEP_OUTPUT_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Music", "acestep")
}

// EnsureDirs creates every layout directory with owner-only permissions.
func (l Layout) EnsureDirs() error {
	for _, dir := range []string{l.Root, l.Bin, l.Models, l.Outputs, l.Work} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}

// BinExt returns the executable suffix for the running platform.
func BinExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// BinaryName returns the platform-suffixed name of an engine binary.
func BinaryName(base string) string {
	return base + BinExt()
}

// Bind wires the Viper instance to the layout and reads defaults, environment
// variables and the optional config file.
func Bind(v *viper.Viper, l Layout) error {
	v.SetEnvPrefix("acestep")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()

	v.SetDefault("root", l.Root)
	v.SetDefault("bin-dir", l.Bin)
	v.SetDefault("models-dir", l.Models)
	v.SetDefault("output-dir", l.Outputs)
	v.SetDefault("work-dir", l.Work)

	// The engine binary names are configurable so a user can point at a build
	// from a fork without touching the filesystem layout. Viper's env key
	// replacer maps "binaries.ace-lm" to ACESTEP_BINARIES_ACE_LM.
	v.SetDefault("binaries.ace-lm", BinaryName("ace-lm"))
	v.SetDefault("binaries.ace-synth", BinaryName("ace-synth"))

	// Generation defaults. See docs/ARCHITECTURE.md for the engine semantics.
	v.SetDefault("generate.duration", 60)
	v.SetDefault("generate.output-format", "mp3")
	v.SetDefault("generate.steps", 0)
	v.SetDefault("generate.shift", 0.0)
	v.SetDefault("generate.lm-model", "")
	v.SetDefault("generate.synth-model", "")
	v.SetDefault("generate.vocal-language", "")
	v.SetDefault("generate.lm-batch-size", 1)
	v.SetDefault("generate.synth-batch-size", 1)
	v.SetDefault("generate.log-raw", false)
	v.SetDefault("generate.stream-progress", true)

	v.SetDefault("models.repo", "Serveurperso/ACE-Step-1.5-GGUF")
	v.SetDefault("models.preset", "essential")

	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(l.Root)
	v.AddConfigPath(".")

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !isNotFound(err, &notFound) {
			return fmt.Errorf("read config: %w", err)
		}
	}
	return nil
}

func isNotFound(err error, target *viper.ConfigFileNotFoundError) bool {
	if e, ok := err.(viper.ConfigFileNotFoundError); ok {
		*target = e
		return true
	}
	return false
}
