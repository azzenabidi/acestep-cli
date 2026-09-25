package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/azzenabidi/acestep-cli/pkg/downloader"
)

// engineRepo is the upstream C++ engine this CLI supervises.
const engineRepo = "https://github.com/ace-step/acestep.cpp"

// engineRef pins the engine commit/tag built by setup. Empty means the
// default branch tip.
var engineRef string

// engineBackends selects the CMake compute backend.
var (
	setupCPU    bool
	setupCUDA   bool
	setupVulkan bool
	setupROCm   bool
	setupAll    bool
	setupModels string
	setupForce  bool
	setupSkip   bool
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Build the engine and download the models",
	Long: `Prepare everything needed to generate music.

The command builds acestep.cpp from source with CMake, installs ace-lm and
ace-synth into the bin directory, then downloads the GGUF weights.

  acestep setup                      # CPU build + the essential preset
  acestep setup --cuda               # NVIDIA GPU
  acestep setup --vulkan             # AMD/Intel GPU
  acestep setup --all                # CPU + CUDA + Vulkan, backend chosen at runtime
  acestep setup --models lowvram     # smaller weights for 4 GB machines
  acestep setup --skip-binaries      # models only

Use --list-presets to see the model bundles and their sizes.`,
	Args: cobra.NoArgs,
	RunE: runSetup,
}

func init() {
	f := setupCmd.Flags()
	f.BoolVar(&setupCPU, "cpu", true, "build the CPU backend")
	f.BoolVar(&setupCUDA, "cuda", false, "build the CUDA backend (NVIDIA)")
	f.BoolVar(&setupVulkan, "vulkan", false, "build the Vulkan backend (AMD/Intel)")
	f.BoolVar(&setupROCm, "rocm", false, "build the ROCm backend (AMD)")
	f.BoolVar(&setupAll, "all", false, "build every backend and select at runtime")
	f.StringVar(&setupModels, "models", "essential", "model bundle: lowvram, standard, essential, quality, or a comma-separated list of roles/filenames")
	f.BoolVar(&setupForce, "force", false, "re-download models that are already present")
	f.BoolVar(&setupSkip, "skip-binaries", false, "download models without building the engine")
	f.StringVar(&engineRef, "ref", "", "engine git ref to build (default: the upstream default branch)")
	setupCmd.Flags().Bool("list-presets", false, "list the model bundles and exit")
	setupCmd.Flags().Bool("binaries-only", false, "build the engine without downloading models")

	rootCmd.AddCommand(setupCmd)
}

func runSetup(cmd *cobra.Command, _ []string) error {
	if list, _ := cmd.Flags().GetBool("list-presets"); list {
		printPresets()
		return nil
	}
	binariesOnly, _ := cmd.Flags().GetBool("binaries-only")

	if err := global.layout.EnsureDirs(); err != nil {
		return err
	}
	ctx := signalContext()

	// Always make sure the bin directory is on PATH for the session's
	// children, since the engine binaries live there.
	heading("Environment")
	envTable := [][2]string{
		{"root", global.layout.Root},
		{"bin", global.layout.Bin},
		{"models", global.layout.Models},
		{"outputs", global.layout.Outputs},
		{"work", global.layout.Work},
	}
	for _, kv := range envTable {
		info("  %-8s %s", dim(kv[0]), kv[1])
	}
	info("")
	info("  %s %s", dim("platform"), runtime.GOOS+"/"+runtime.GOARCH)

	if !setupSkip {
		heading("Engine")
		if err := buildEngine(ctx, cmd); err != nil {
			return err
		}
	}
	if binariesOnly {
		return verifyBinaries()
	}

	heading("Models")
	models, err := resolveModelSpec(cmd)
	if err != nil {
		return err
	}
	if err := fetchModels(ctx, models); err != nil {
		return err
	}

	heading("Ready")
	success("engine binaries in %s", global.layout.Bin)
	success("%d model file(s) in %s", len(models), global.layout.Models)
	info("")
	info("Try it:")
	info("  %s %s", cyan("acestep generate"), "-p \"lofi synthwave with relaxing piano\"")
	return nil
}

// resolveModelSpec takes --models when it was given, otherwise the
// `models.preset` config value, otherwise the flag default.
func resolveModelSpec(cmd *cobra.Command) ([]downloader.Model, error) {
	spec := setupModels
	if !cmd.Flags().Changed("models") && global.viper != nil {
		if fromConfig := global.viper.GetString("models.preset"); fromConfig != "" {
			spec = fromConfig
		}
	}
	return downloader.Resolve(spec)
}

// printPresets writes the model bundle table.
func printPresets() {
	if global.jsonOut {
		_ = emitJSON(downloader.Presets)
		return
	}
	heading("Model bundles")
	for _, p := range downloader.Presets {
		fmt.Fprintf(stdout(), "  %s %-10s %s\n", bold(p.Name), p.ApproxSize, dim(p.Description))
		for _, m := range p.Models {
			fmt.Fprintf(stdout(), "      %-42s %-11s %s\n", m.Name, m.Role, dim(m.Description))
		}
		fmt.Fprintln(stdout())
	}
	hint("Specify one with `acestep setup --models <name>`.")
	hint("Roles (%s) and exact .gguf filenames are also accepted.",
		strings.Join([]string{"lm", "dit", "vae", "text-encoder"}, ", "))
}

// engineSourceDir is where the C++ sources are cloned and built.
func engineSourceDir() string {
	return filepath.Join(global.layout.Root, ".build", "acestep.cpp")
}

// buildEngine clones and compiles acestep.cpp, installing only the two
// binaries this CLI drives.
func buildEngine(ctx context.Context, cmd *cobra.Command) error {
	if err := requireTools("git", "cmake"); err != nil {
		return err
	}
	src := engineSourceDir()
	if _, err := os.Stat(filepath.Join(src, "CMakeLists.txt")); err != nil {
		step(1, "cloning %s", engineRepo)
		if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
			return errorf("create build parent: %w", err)
		}
		args := []string{"clone", "--recurse-submodules", "--depth", "1"}
		if engineRef != "" {
			args = append(args, "--branch", engineRef)
		}
		args = append(args, engineRepo, src)
		if err := runCommand(ctx, cmd, ".", "git", args...); err != nil {
			return errorf("clone engine: %w", err)
		}
	} else {
		step(1, "engine sources already present at %s", src)
	}

	buildDir := filepath.Join(src, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return errorf("create build dir: %w", err)
	}

	step(2, "configuring with %s", strings.Join(cmakeFlags(), " "))
	if err := runCommand(ctx, cmd, buildDir, "cmake", append([]string{"..", "-DCMAKE_BUILD_TYPE=Release"}, cmakeFlags()...)...); err != nil {
		return errorf("cmake configure: %w", err)
	}

	jobs := runtime.NumCPU()
	if jobs < 1 {
		jobs = 1
	}
	step(3, "compiling with %d job(s); this takes a while", jobs)
	if err := runCommand(ctx, cmd, buildDir, "cmake", "--build", ".", "--config", "Release", "-j", fmt.Sprint(jobs)); err != nil {
		return errorf("cmake build: %w\n\nIf this is a CUDA or Vulkan build, check that the SDK is installed and on PATH.", err)
	}

	step(4, "installing binaries into %s", global.layout.Bin)
	return installBinaries(src, buildDir)
}

// cmakeFlags translates the backend flags into CMake options. The
// GGML_CPU_ALL_VARIANTS + GGML_BACKEND_DL combination builds every backend
// with runtime loading, which is what --all asks for.
func cmakeFlags() []string {
	if setupAll {
		return []string{
			"-DGGML_CPU_ALL_VARIANTS=ON",
			"-DGGML_CUDA=ON",
			"-DGGML_VULKAN=ON",
			"-DGGML_BACKEND_DL=ON",
		}
	}
	var flags []string
	if setupCUDA {
		flags = append(flags, "-DGGML_CUDA=ON")
	}
	if setupVulkan {
		flags = append(flags, "-DGGML_VULKAN=ON")
	}
	if setupROCm {
		flags = append(flags, "-DGGML_HIP=ON")
	}
	// A build with no accelerator flags is a plain CPU build, which needs no
	// extra option. setupCPU exists so `--cpu` can turn accelerators off.
	if setupCPU && len(flags) == 0 {
		flags = append(flags, "-DGGML_NATIVE=ON")
	}
	return flags
}

// engineBinaries lists the artifacts this CLI needs.
var engineBinaries = []string{"ace-lm", "ace-synth"}

// installBinaries copies the built executables into the layout's bin dir.
func installBinaries(src, buildDir string) error {
	candidates := []string{
		filepath.Join(buildDir, "bin"),
		buildDir,
		filepath.Join(src, "build", "Release", "bin"),
		filepath.Join(src, "bin"),
	}
	for _, name := range engineBinaries {
		srcPath, ok := findBinary(candidates, name)
		if !ok {
			return errorf("build finished but %s was not produced (looked in %s)",
				name, strings.Join(candidates, ", "))
		}
		dst := filepath.Join(global.layout.Bin, name+"BinSuffix")
		if err := copyExecutable(srcPath, dst); err != nil {
			return err
		}
		success("installed %s", dst)
	}
	return nil
}

// findBinary looks for a built executable across the usual output layouts.
func findBinary(dirs []string, name string) (string, bool) {
	for _, dir := range dirs {
		for _, candidate := range []string{
			filepath.Join(dir, name),
			filepath.Join(dir, name+".exe"),
		} {
			if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
				return candidate, true
			}
		}
	}
	return "", false
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return errorf("open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return errorf("create %s: %w", dst, err)
	}
	if _, err := out.ReadFrom(in); err != nil {
		out.Close()
		return errorf("copy %s: %w", dst, err)
	}
	return out.Close()
}

// verifyBinaries confirms both engines are present and runnable.
func verifyBinaries() error {
	heading("Verification")
	var missing []string
	for _, name := range engineBinaries {
		path, ok := downloader.EngineBinary(global.layout.Bin, name)
		if !ok {
			missing = append(missing, name)
			fail("%s not found at %s", name, path)
			continue
		}
		success("%s", path)
	}
	if len(missing) > 0 {
		return errorf("%d engine binary/binararies missing", len(missing))
	}
	return nil
}

// fetchModels downloads the requested weights, skipping ones already present.
func fetchModels(ctx context.Context, models []downloader.Model) error {
	d := downloader.New(global.viper.GetString("models.repo"), global.layout.Models)
	d.Force = setupForce
	d.Progress = !global.jsonOut && isInteractive()
	d.Quiet = global.quiet

	repo := d.Repo
	info("  %s %s", dim("repo"), repo)
	info("  %s %s", dim("total"), humanBytes(downloader.TotalSize(models)))
	info("")

	for i, m := range models {
		if s := d.Inspect(m); s.Present && s.Complete && !setupForce {
			info("%s %-42s %s", green("✓"), m.Name, dim("already present"))
			continue
		}
		step(i+1, "downloading %s (%s)", m.Name, humanBytes(m.Size))
		if _, err := d.Fetch(ctx, m); err != nil {
			return errorf("download %s: %w", m.Name, err)
		}
		success("%s", m.Name)
	}
	return nil
}

// runCommand runs an external tool, streaming its output, and respects
// cancellation.
func runCommand(ctx context.Context, cmd *cobra.Command, dir, name string, args ...string) error {
	info("  %s %s", dim("$"), strings.Join(append([]string{name}, args...), " "))
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	c.Stdout = stderr()
	c.Stderr = stderr()
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// requireTools fails early with an actionable message when a build tool is
// missing, rather than deep inside a subprocess.
func requireTools(tools ...string) error {
	var missing []string
	for _, t := range tools {
		if _, err := exec.LookPath(t); err != nil {
			missing = append(missing, t)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("missing build tool(s): " + strings.Join(missing, ", "))
	b.WriteString("\n\nInstall them and re-run:")
	if runtime.GOOS == "darwin" {
		b.WriteString("  brew install git cmake")
	} else if runtime.GOOS == "windows" {
		b.WriteString("  winget install Git.Git Kitware.CMake")
	} else {
		b.WriteString("  sudo apt install git cmake        # Debian/Ubuntu")
		b.WriteString("  sudo dnf install git cmake        # Fedora")
	}
	b.WriteString("\n\nOr skip the build and use prebuilt binaries with:\n")
	b.WriteString("  acestep setup --skip-binaries     # then place ace-lm and ace-synth in " + global.layout.Bin)
	return errorf("%s", b.String())
}

// humanBytes renders a byte count for humans.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

var _ = time.Second
