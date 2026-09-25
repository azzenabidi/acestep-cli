package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/azzenabidi/acestep-cli/pkg/engine"
	"github.com/azzenabidi/acestep-cli/pkg/request"
	"github.com/azzenabidi/acestep-cli/pkg/tui"
)

// generateFlags holds every knob on the generate subcommand.
type generateFlags struct {
	prompt     string
	lyrics     string
	lyricsFile string
	duration   float64
	output     string
	format     string
	style      string
	bpm        int
	keyScale   string
	timeSig    string
	language   string
	instrument bool
	variations int
	steps      int
	shift      float64
	seed       int64
	lmModel    string
	synthModel string
	taskType   string
	srcAudio   string
	refAudio   string
	audioCodes string
	adapter    string
	vaeChunk   int
	vaeOverlap int
	skipLM     bool
}

var gen generateFlags

// unsafeName matches path separators and characters that break shells.
var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate a track from a text prompt",
	Long: `Generate a track from a text description.

With no flags the command launches an interactive wizard. Every flag skips one
step of it, so -p alone is enough to start:

  acestep generate -p "lofi synthwave with relaxing piano"

Duration defaults to 60 seconds. Pass --duration 0 to let the model choose,
and use --instrumental for a track with no vocals.`,
	Example: `  # quick instrumental sketch
  acestep generate -p "ambient drone, slow strings" -d 30 --instrumental

  # full song with your own lyrics
  acestep generate -p "90s alt rock, gritty guitars" -l lyrics.txt -d 180

  # four variations at once
  acestep generate -p "jazz trio, upright bass" -n 4

  # lossless output
  acestep generate -p "orchestral fanfare" --format wav24 -o fanfare.wav`,
	Args: cobra.NoArgs,
	RunE: runGenerate,
}

func init() {
	f := generateCmd.Flags()
	f.StringVarP(&gen.prompt, "prompt", "p", "", "text description of the song (style, mood, instruments)")
	f.StringVarP(&gen.lyrics, "lyrics", "l", "", "lyrics: inline text or a path to a .txt file")
	f.Float64VarP(&gen.duration, "duration", "d", 0, "target length in seconds (0 = let the model choose)")
	f.StringVarP(&gen.output, "output", "o", "", "output file path (default: <slug>.wav under the output dir)")
	f.StringVarP(&gen.format, "format", "F", "", "audio format: mp3, wav16, wav24 or wav32")
	f.StringVar(&gen.style, "style", "", "extra style tag appended to the prompt")
	f.IntVar(&gen.bpm, "bpm", -1, "tempo in BPM (0 = let the model choose)")
	f.StringVar(&gen.keyScale, "key", "", "musical key and scale, e.g. \"C major\"")
	f.StringVar(&gen.timeSig, "time-signature", "", "time signature numerator, e.g. 4 for 4/4")
	f.StringVar(&gen.language, "language", "", "BCP-47 code for vocals, e.g. en, fr, ja (default: auto-detect)")
	f.BoolVar(&gen.instrument, "instrumental", false, "no vocals; equivalent to lyrics \"[Instrumental]\"")
	f.IntVarP(&gen.variations, "variations", "n", 1, "number of variations to generate")
	f.IntVar(&gen.steps, "steps", 0, "DiT inference steps (0 = model default: 8 for turbo, 50 for sft)")
	f.Float64Var(&gen.shift, "shift", 0, "flow-matching timestep shift (0 = model default)")
	f.Int64Var(&gen.seed, "seed", 0, "RNG seed for reproducible DiT noise (0 = random)")
	f.StringVar(&gen.lmModel, "lm-model", "", "planner LM GGUF name without extension")
	f.StringVar(&gen.synthModel, "synth-model", "", "DiT GGUF name without extension")
	f.StringVar(&gen.taskType, "task", request.TaskText2Music, "task type: text2music, cover, repaint, lego, extract or complete")
	f.StringVar(&gen.srcAudio, "src-audio", "", "source audio for cover/repaint/lego/extract/complete")
	f.StringVar(&gen.refAudio, "ref-audio", "", "reference audio for timbre conditioning")
	f.StringVar(&gen.audioCodes, "codes", "", "pre-computed FSQ audio codes (skips the LM stage)")
	f.StringVar(&gen.adapter, "adapter", "", "adapter (LoRA) name, resolved from the adapters dir")
	f.IntVar(&gen.vaeChunk, "vae-chunk", 0, "latent frames per VAE tile; lower it on low-memory machines")
	f.IntVar(&gen.vaeOverlap, "vae-overlap", 0, "VAE tile overlap frames")
	f.BoolVar(&gen.skipLM, "skip-lm", false, "run only ace-synth, using the request as given")

	rootCmd.AddCommand(generateCmd)
}

func runGenerate(cmd *cobra.Command, _ []string) error {
	ctx := signalContext()
	flags := cmd.Flags()

	// Interactive mode kicks in only when nothing meaningful was passed.
	interactive := !flags.Changed("prompt") && !flags.Changed("lyrics") && !flags.Changed("duration")
	if interactive && useUI() {
		answer, err := runWizard(ctx, cmd)
		if err != nil {
			return err
		}
		if answer == nil {
			info("cancelled")
			return nil
		}
		applyAnswer(*answer)
	} else if strings.TrimSpace(gen.prompt) == "" {
		return errorf("no prompt given: pass --prompt or run `acestep generate` in a terminal for the interactive wizard")
	}

	req, baseName, err := buildRequest(cmd)
	if err != nil {
		return err
	}
	return executeGeneration(ctx, req, baseName)
}

// runWizard launches the interactive wizard and handles a cancellation.
func runWizard(ctx context.Context, cmd *cobra.Command) (*tui.Answer, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil
	}
	info("")
	answer, err := tui.RunWizard(tui.Answer{
		Duration: 60,
		Format:   "mp3",
	})
	if err != nil {
		return nil, errorf("interactive wizard failed: %w", err)
	}
	if answer.Caption == "" {
		return nil, nil
	}
	return &answer, nil
}

// applyAnswer maps wizard results onto the flag struct.
func applyAnswer(a tui.Answer) {
	gen.prompt = strings.TrimSpace(a.Caption + " " + a.Style)
	if a.Lyrics != "" {
		gen.lyrics = a.Lyrics
	}
	if a.Instrumental {
		gen.instrument = true
	}
	gen.duration = a.Duration
	if a.Format != "" {
		gen.format = a.Format
	}
}

// buildRequest assembles the AceRequest and derives the output base name.
func buildRequest(cmd *cobra.Command) (*request.Request, string, error) {
	flags := cmd.Flags()

	prompt := strings.TrimSpace(gen.prompt)
	if gen.style != "" {
		prompt = strings.TrimSpace(prompt + " " + gen.style)
	}
	req := request.New(prompt)

	// Lyrics: a file path is read, anything else is treated as inline text.
	if flags.Changed("lyrics") {
		lyrics := gen.lyrics
		if path, ok := lyricsFile(lyrics); ok {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, "", errorf("read lyrics file: %w", err)
			}
			lyrics = string(data)
		}
		req.SetLyrics(strings.TrimSpace(lyrics))
	}
	if gen.instrument {
		if flags.Changed("lyrics") && !gen.instrument {
			return nil, "", errorf("--instrumental conflicts with --lyrics")
		}
		req.SetInstrumental()
	}

	// Duration. The engine's FSM constrains this to [10,600]s and treats 0 as
	// "model decides", so only send it when the user actually chose.
	if flags.Changed("duration") {
		if gen.duration < 0 {
			return nil, "", errorf("--duration must be >= 0")
		}
		req.SetDuration(gen.duration)
	} else if gen.duration > 0 {
		req.SetDuration(gen.duration)
	}

	if flags.Changed("bpm") && gen.bpm >= 0 {
		req.SetBPM(gen.bpm)
	}
	if flags.Changed("key") {
		req.SetKeyScale(gen.keyScale)
	}
	if flags.Changed("time-signature") {
		req.SetTimeSignature(gen.timeSig)
	}
	if flags.Changed("language") {
		req.SetVocalLanguage(gen.language)
	}
	if flags.Changed("seed") {
		req.SetSeed(gen.seed)
	}
	if flags.Changed("steps") {
		req.SetInferenceSteps(gen.steps)
	}
	if flags.Changed("shift") {
		v := gen.shift
		req.Shift = &v
	}
	if gen.lmModel != "" {
		req.SetLMModel(gen.lmModel)
	}
	if gen.synthModel != "" {
		req.SetSynthModel(gen.synthModel)
	}
	if gen.adapter != "" {
		a := gen.adapter
		req.Adapter = &a
	}
	if gen.audioCodes != "" {
		c := gen.audioCodes
		req.AudioCodes = &c
		req.SetLMMode(request.LMModeFormat)
	}
	if gen.taskType != "" && gen.taskType != request.TaskText2Music {
		if !request.ValidTaskType(gen.taskType) {
			return nil, "", errorf("invalid --task %q", gen.taskType)
		}
		t := gen.taskType
		req.TaskType = &t
	}
	if gen.taskType != request.TaskText2Music && gen.srcAudio == "" && gen.audioCodes == "" {
		return nil, "", errorf("--task %s needs --src-audio (or --codes)", gen.taskType)
	}

	format := resolveFormat(cmd)
	req.SetOutputFormat(format)
	if gen.variations > 1 {
		req.SetBatchSizes(gen.variations, 1)
	}

	if err := req.Validate(); err != nil {
		return nil, "", err
	}
	return req, baseNameFor(cmd), nil
}

// resolveFormat picks the audio encoder from the flag, then config, then the
// engine default.
func resolveFormat(cmd *cobra.Command) string {
	format := gen.format
	if !cmd.Flags().Changed("format") && global.viper != nil {
		format = global.viper.GetString("generate.output-format")
	}
	if format == "" {
		format = request.FormatMP3
	}
	if !request.ValidOutputFormat(format) {
		// Fail loudly here rather than at engine exit.
		panicFreeWarn(format)
	}
	return format
}

// panicFreeWarn reports an invalid format and falls back to the engine default.
func panicFreeWarn(format string) {
	warn("unknown output format %q, falling back to %s", format, request.FormatMP3)
}

// baseNameFor derives the output filename stem, honouring --output. The
// extension comes from the engine's output_format, not from the file name.
func baseNameFor(cmd *cobra.Command) string {
	if gen.output != "" {
		if filepath.Ext(gen.output) != "" {
			return strings.TrimSuffix(gen.output, filepath.Ext(gen.output))
		}
		return gen.output
	}
	// A timestamp keeps successive runs from clobbering each other.
	slug := slugify(gen.prompt)
	if slug == "" {
		slug = "track"
	}
	if len(slug) > 60 {
		slug = slug[:60]
	}
	slug += "-" + time.Now().Format("20060102-150405")
	return filepath.Join(global.layout.Outputs, slug)
}

var slugPattern = unsafeName

// slugify reduces a prompt to a filesystem-safe name.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugPattern.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	return s
}

// lyricsFile reports whether a lyrics value should be treated as a path and
// returns the resolved path.
func lyricsFile(value string) (string, bool) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", false
	}
	// Only treat it as a path if it looks like one and actually exists; an
	// inline lyric such as "hello, world" must not be stat'd into an error.
	if !strings.ContainsAny(v, "/\\") && !strings.HasSuffix(strings.ToLower(v), ".txt") {
		return "", false
	}
	if _, err := os.Stat(v); err != nil {
		return "", false
	}
	return v, true
}

// executeGeneration runs the engine with the appropriate progress renderer.
func executeGeneration(ctx context.Context, req *request.Request, baseName string) error {
	if err := global.layout.EnsureDirs(); err != nil {
		return err
	}

	opts, err := buildEngineOptions()
	if err != nil {
		return err
	}
	pipe, err := engine.New(opts)
	if err != nil {
		return err
	}
	if err := pipe.Check(); err != nil {
		return engineError(err)
	}

	if global.jsonOut {
		return runGenerationJSON(ctx, pipe, req, baseName)
	}

	reporter := tui.NewReporter()
	opts.OnStage = reporter.OnStage
	opts.OnProgress = reporter.OnProgress
	pipe, err = engine.New(opts)
	if err != nil {
		return err
	}

	runErr := make(chan error, 1)
	go func() {
		res, err := pipe.Generate(ctx, req, baseName)
		if err != nil {
			reporter.Done(nil, 0, err)
			runErr <- err
			return
		}
		for _, t := range res.Tracks {
			reporter.Track(t)
		}
		reporter.Done(res.Tracks, res.Elapsed, nil)
		runErr <- nil
	}()

	viewErr := tui.RunGenerationView(ctx, reporter, func() error {
		plain := tui.NewPlain(stderr())
		opts.OnStage = plain.OnStage
		opts.OnProgress = plain.OnProgress
		p, err := engine.New(opts)
		if err != nil {
			return err
		}
		_, perr := p.Generate(ctx, req, baseName)
		plain.Done()
		return perr
	})

	genErr := <-runErr
	if genErr != nil {
		if errors.Is(genErr, context.Canceled) {
			return genErr
		}
		return engineError(genErr)
	}
	if viewErr != nil {
		hint("progress view stopped early: %v", viewErr)
	}
	return nil
}

// runGenerationJSON emits a machine-readable result.
func runGenerationJSON(ctx context.Context, pipe *engine.Pipeline, req *request.Request, baseName string) error {
	res, err := pipe.Generate(ctx, req, baseName)
	if err != nil {
		return engineError(err)
	}
	return emitJSON(struct {
		Tracks  []string `json:"tracks"`
		Elapsed string   `json:"elapsed"`
		Seed    int64    `json:"seed"`
		Request any      `json:"request"`
	}{
		Tracks:  res.Tracks,
		Elapsed: res.Elapsed.Round(time.Millisecond).String(),
		Seed:    derefInt64(req.Seed),
		Request: req,
	})
}

// buildEngineOptions turns flags and config into engine options.
func buildEngineOptions() (engine.Options, error) {
	v := global.viper
	lmName := v.GetString("binaries.ace-lm")
	synthName := v.GetString("binaries.ace-synth")
	opts := engine.Options{
		LMPath:         filepath.Join(global.layout.Bin, lmName),
		SynthPath:      filepath.Join(global.layout.Bin, synthName),
		ModelsDir:      global.layout.Models,
		WorkDir:        global.layout.Work,
		OutputDir:      global.layout.Outputs,
		SRCAudio:       gen.srcAudio,
		RefAudio:       gen.refAudio,
		VAEChunk:       gen.vaeChunk,
		VAEOverlap:     gen.vaeOverlap,
		GracePeriod:    gracePeriod,
		RawLogs:        global.verbose || v.GetBool("generate.log-raw"),
		StreamProgress: v.GetBool("generate.stream-progress"),
		SkipLM:         gen.skipLM || gen.audioCodes != "",
		AdaptersDir:    v.GetString("adapters-dir"),
	}
	if !useUI() {
		opts.RawLogs = opts.RawLogs || global.verbose
	}
	if opts.AdaptersDir == "" && gen.adapter != "" {
		opts.AdaptersDir = filepath.Join(global.layout.Root, "adapters")
	}
	if opts.StreamProgress && global.quiet {
		opts.StreamProgress = false
	}
	return opts, nil
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

// formatSeconds renders a duration for user-facing text.
func formatSeconds(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64) + "s"
}

var _ = fmt.Sprintf
