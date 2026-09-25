package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"

	"github.com/azzenabidi/acestep-cli/pkg/config"
	"github.com/azzenabidi/acestep-cli/pkg/engine"
)

// Build metadata, overridable at link time with -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// globalFlags holds values shared by every subcommand.
type globalFlags struct {
	root      string
	noColor   bool
	jsonOut   bool
	quiet     bool
	verbose   bool
	noUI      bool
	rawLogs   bool
	keepWork  bool
	layout    config.Layout
	viper     *viper.Viper
	ctx       context.Context
	sigCancel context.CancelFunc
}

// resolved is populated by PersistentPreRun and read by subcommands.
var global globalFlags

// rootCmd is the base command.
var rootCmd = &cobra.Command{
	Use:   "acestep",
	Short: "Generate music locally with the acestep.cpp engine",
	Long: `acestep turns a text prompt into a finished track using the acestep.cpp
engine: a portable C++17/GGML implementation of ACE-Step 1.5 that runs on CPU,
CUDA, Metal and Vulkan.

Everything is local. No prompt, lyric or audio leaves the machine, and no
account or API key is required.

Typical first run:

  acestep setup
  acestep generate -p "lofi synthwave with relaxing piano"

Then press enter with no flags for an interactive wizard.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(c *cobra.Command, _ []string) error {
		return initGlobals()
	},
	PersistentPostRun: func(c *cobra.Command, _ []string) {
		if global.sigCancel != nil {
			global.sigCancel()
		}
	},
}

func init() {
	flags := rootCmd.PersistentFlags()
	flags.StringVarP(&global.root, "home", "H", "", "root directory for binaries, models and outputs (default $ACESTEP_HOME or the platform config dir)")
	flags.BoolVar(&global.noColor, "no-color", false, "disable ANSI colour output")
	flags.BoolVar(&global.jsonOut, "json", false, "emit machine-readable JSON on stdout")
	flags.BoolVarP(&global.quiet, "quiet", "q", false, "suppress non-essential output")
	flags.BoolVarP(&global.verbose, "verbose", "v", false, "stream raw engine logs")
	flags.BoolVar(&global.noUI, "no-ui", false, "disable interactive UI and use plain progress lines")
	flags.BoolVar(&global.keepWork, "keep-work", false, "keep intermediate request JSON files for inspection")
}

// initGlobals resolves the layout, binds config and installs signal handling.
func initGlobals() error {
	layout, err := config.NewLayout(global.root)
	if err != nil {
		return err
	}
	global.layout = layout

	v := viper.New()
	if err := config.Bind(v, layout); err != nil {
		return err
	}
	// A --home override on the command line outranks the config file and env.
	if global.root != "" {
		v.Set("root", layout.Root)
	}
	global.viper = v

	if global.noColor || os.Getenv("NO_COLOR") != "" {
		noColor()
	}

	// Ctrl+C cancels the context, which tears down the engine process group.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	global.sigCancel = cancel
	global.ctx = ctx
	return nil
}

// ctx returns the signal-cancelled context for the current invocation.
func signalContext() context.Context {
	if global.ctx == nil {
		return context.Background()
	}
	return global.ctx
}

// Execute runs the command tree and maps errors to exit codes.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		// A cancelled run is a normal outcome, not a failure.
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "\nacestep: cancelled")
			os.Exit(130)
		}
		fmt.Fprintf(os.Stderr, "acestep: %v\n", err)
		os.Exit(1)
	}
}

// isInteractive reports whether stdin and stdout are attached to a terminal.
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// useUI reports whether the full-screen UI should be shown.
func useUI() bool {
	return isInteractive() && !global.noUI && !global.jsonOut
}

// gracePeriod is how long an engine stage may take to exit after SIGTERM.
const gracePeriod = 5 * time.Second

// errorf builds a formatted error.
func errorf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// truncate shortens a long value for one-line output.
func truncate(s string, max int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

// engineError renders an engine failure with actionable advice.
func engineError(err error) error {
	var missing *engine.ErrMissingBinary
	if errors.As(err, &missing) {
		return fmt.Errorf("%w\n\nRun `acestep setup --binaries` to build or fetch the engine", err)
	}
	var failed *engine.ErrEngine
	if errors.As(err, &failed) {
		var hint string
		switch failed.Stage {
		case engine.StageLM:
			hint = "The planner LM could not run. Verify the LM and text-encoder GGUFs are present in " +
				global.layout.Models + " and that --lm-model names a file that exists there."
		case engine.StageSynth:
			hint = "The DiT/VAE stage could not run. Verify the DiT and VAE GGUFs are present in " +
				global.layout.Models + ". On low-VRAM cards try --vae-chunk 256 (see docs/HARDWARE_GUIDE.md)."
		}
		if hint != "" {
			return fmt.Errorf("%w\n\n%s", err, hint)
		}
	}
	return err
}
