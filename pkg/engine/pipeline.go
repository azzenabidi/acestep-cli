// Package engine supervises the acestep.cpp binaries.
//
// The engine is a two-stage pipe driven entirely by a JSON request document:
// ace-lm plans the song (lyrics, metadata, FSQ audio codes) and writes
// request{N}.json next to its input; ace-synth then renders those to audio as
// request{I}{B}.{ext}, where I indexes the LM variation and B the DiT
// variation. Neither binary writes into a caller-specified directory, so this
// package runs each stage with its working directory set to a private
// workspace and then collects the results.
package engine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azzenabidi/acestep-cli/pkg/request"
)

// Stage names an engine binary.
type Stage string

const (
	StageLM    Stage = "ace-lm"
	StageSynth Stage = "ace-synth"
)

// Options configures a pipeline run.
type Options struct {
	// LMPath and SynthPath are absolute paths to the engine binaries.
	LMPath    string
	SynthPath string
	// ModelsDir is passed as --models and scanned for GGUF files.
	ModelsDir string
	// AdaptersDir, when set, is passed as --adapters.
	AdaptersDir string
	// WorkDir holds request.json and the intermediate request{N}.json files.
	WorkDir string
	// OutputDir is where finished tracks are moved.
	OutputDir string
	// SRCAudio enables cover/repaint/lego via --src-audio.
	SRCAudio string
	// RefAudio enables timbre conditioning via --ref-audio.
	RefAudio string
	// VAEChunk and VAEOverlap tune the tiled VAE decoder for low memory.
	VAEChunk   int
	VAEOverlap int
	// GracePeriod is how long a stage may take to exit after SIGTERM before
	// it is killed.
	GracePeriod time.Duration
	// RawLogs echoes every engine log line to Stderr.
	RawLogs bool
	// StreamProgress enables progress parsing.
	StreamProgress bool
	// Log receives engine log lines. Defaults to discarding them.
	Log io.Writer
	// OnProgress receives progress observations.
	OnProgress func(Stage, Progress)
	// OnStage is called when a stage starts.
	OnStage func(Stage)
	// SkipLM runs ace-synth directly, for requests that already carry audio
	// codes (the dit-only path).
	SkipLM bool
}

// Pipeline runs generation jobs.
type Pipeline struct {
	opts Options
}

// Result reports what a run produced.
type Result struct {
	// Tracks are the finished audio files, named as requested.
	Tracks []string
	// Requests are the intermediate request{N}.json files.
	Requests []string
	// Elapsed is the wall time of both stages combined.
	Elapsed time.Duration
}

// ErrMissingBinary indicates a required engine binary is absent.
type ErrMissingBinary struct {
	Stage Stage
	Path  string
}

func (e *ErrMissingBinary) Error() string {
	return fmt.Sprintf("%s binary not found at %s (run `acestep setup`)", e.Stage, e.Path)
}

// ErrEngine wraps a non-zero exit from an engine stage.
type ErrEngine struct {
	Stage    Stage
	ExitCode int
	// Output is the tail of the stage's combined output, useful for
	// diagnosing a model that fails to load.
	Output string
}

func (e *ErrEngine) Error() string {
	msg := fmt.Sprintf("%s exited with status %d", e.Stage, e.ExitCode)
	if strings.TrimSpace(e.Output) != "" {
		msg += ": " + strings.TrimSpace(e.Output)
	}
	return msg
}

// New validates the options and returns a Pipeline.
func New(opts Options) (*Pipeline, error) {
	if opts.LMPath == "" || opts.SynthPath == "" {
		return nil, errors.New("engine: both ace-lm and ace-synth paths are required")
	}
	if opts.ModelsDir == "" {
		return nil, errors.New("engine: models directory is required")
	}
	if opts.WorkDir == "" {
		return nil, errors.New("engine: work directory is required")
	}
	if opts.OutputDir == "" {
		return nil, errors.New("engine: output directory is required")
	}
	if opts.GracePeriod <= 0 {
		opts.GracePeriod = 5 * time.Second
	}
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	return &Pipeline{opts: opts}, nil
}

// Check verifies the binaries and models are usable before a long run.
func (p *Pipeline) Check() error {
	if err := requireFile(p.opts.LMPath, StageLM); err != nil {
		return err
	}
	if err := requireFile(p.opts.SynthPath, StageSynth); err != nil {
		return err
	}
	if fi, err := os.Stat(p.opts.ModelsDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("engine: models directory %s is not readable (run `acestep setup --models %s`)",
			p.opts.ModelsDir, p.opts.ModelsDir)
	}
	entries, err := os.ReadDir(p.opts.ModelsDir)
	if err != nil {
		return fmt.Errorf("engine: read models directory: %w", err)
	}
	var gguf int
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".gguf") {
			gguf++
		}
	}
	if gguf == 0 {
		return fmt.Errorf("engine: no .gguf models in %s (run `acestep setup`)", p.opts.ModelsDir)
	}
	return nil
}

func requireFile(path string, stage Stage) error {
	fi, err := os.Stat(path)
	if err != nil {
		return &ErrMissingBinary{Stage: stage, Path: path}
	}
	if fi.IsDir() {
		return &ErrMissingBinary{Stage: stage, Path: path}
	}
	if !isExecutable(fi) {
		return fmt.Errorf("engine: %s at %s is not executable (chmod +x)", stage, path)
	}
	return nil
}

// Generate runs the full pipeline for one request and returns the tracks.
func (p *Pipeline) Generate(ctx context.Context, req *request.Request, baseName string) (*Result, error) {
	start := time.Now()
	if err := req.Validate(); err != nil {
		return nil, err
	}
	for _, dir := range []string{p.opts.WorkDir, p.opts.OutputDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("engine: create %s: %w", dir, err)
		}
	}

	jobDir, err := os.MkdirTemp(p.opts.WorkDir, "job-")
	if err != nil {
		return nil, fmt.Errorf("engine: create work dir: %w", err)
	}
	defer os.RemoveAll(jobDir)

	input := filepath.Join(jobDir, "request.json")
	if err := req.WriteFile(input); err != nil {
		return nil, err
	}

	var lmRequests []string
	if p.opts.SkipLM {
		lmRequests = []string{input}
	} else {
		if p.opts.OnStage != nil {
			p.opts.OnStage(StageLM)
		}
		if err := p.runStage(ctx, StageLM, p.lmArgs(input)); err != nil {
			return nil, err
		}
		lmRequests, err = collectRequests(jobDir)
		if err != nil {
			return nil, err
		}
		if len(lmRequests) == 0 {
			return nil, fmt.Errorf("engine: %s produced no request output (check model selection in the request)", StageLM)
		}
	}

	if p.opts.OnStage != nil {
		p.opts.OnStage(StageSynth)
	}
	if err := p.runStage(ctx, StageSynth, p.synthArgs(lmRequests)); err != nil {
		return nil, err
	}

	tracks, err := collectTracks(jobDir, p.opts.OutputDir, baseName, outputExt(req))
	if err != nil {
		return nil, err
	}
	if len(tracks) == 0 {
		return nil, fmt.Errorf("engine: %s produced no audio in %s", StageSynth, jobDir)
	}

	return &Result{
		Tracks:   tracks,
		Requests: lmRequests,
		Elapsed:  time.Since(start),
	}, nil
}

func outputExt(req *request.Request) string {
	if req.OutputFormat != nil {
		return request.OutputExtension(*req.OutputFormat)
	}
	return request.OutputExtension(request.FormatMP3)
}

func (p *Pipeline) lmArgs(requestPath string) []string {
	return []string{"--models", p.opts.ModelsDir, "--request", requestPath}
}

func (p *Pipeline) synthArgs(requests []string) []string {
	args := []string{"--models", p.opts.ModelsDir, "--request"}
	args = append(args, requests...)
	if p.opts.AdaptersDir != "" {
		args = append(args, "--adapters", p.opts.AdaptersDir)
	}
	if p.opts.SRCAudio != "" {
		args = append(args, "--src-audio", p.opts.SRCAudio)
	}
	if p.opts.RefAudio != "" {
		args = append(args, "--ref-audio", p.opts.RefAudio)
	}
	if p.opts.VAEChunk > 0 {
		args = append(args, "--vae-chunk", strconv.Itoa(p.opts.VAEChunk))
	}
	if p.opts.VAEOverlap > 0 {
		args = append(args, "--vae-overlap", strconv.Itoa(p.opts.VAEOverlap))
	}
	return args
}

// runStage executes one engine binary, streaming its output.
func (p *Pipeline) runStage(ctx context.Context, stage Stage, args []string) error {
	bin := p.opts.LMPath
	if stage == StageSynth {
		bin = p.opts.SynthPath
	}
	if err := requireFile(bin, stage); err != nil {
		return err
	}

	// The engine writes its output into the process working directory, so run
	// it inside the job directory.
	cmd := exec.Command(bin, args...)
	cmd.Dir = filepath.Dir(p.lastRequestPath(args))
	setProcAttr(cmd)

	// Forward the environment; the engine's own backend selection handles
	// CUDA/Vulkan/Metal.
	cmd.Env = os.Environ()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("engine: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("engine: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("engine: start %s: %w", stage, err)
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		tail []string
	)
	consume := func(r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		// Engine logs can carry very long debug lines.
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := strings.TrimRight(sc.Text(), "\r")
			mu.Lock()
			tail = append(tail, line)
			if len(tail) > 40 {
				tail = tail[len(tail)-40:]
			}
			mu.Unlock()

			if p.opts.RawLogs {
				fmt.Fprintln(p.opts.Log, line)
			}
			if p.opts.StreamProgress && p.opts.OnProgress != nil {
				if pr, ok := ParseProgress(line); ok {
					p.opts.OnProgress(stage, pr)
				}
			}
		}
	}
	wg.Add(2)
	go consume(stdout)
	go consume(stderr)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var waitErr error
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		// Ctrl+C or a programmatic cancel. Signal the whole process group so
		// any grandchildren (BLAS worker threads' children, shell wrappers)
		// die with the stage, then escalate to a hard kill.
		_ = terminateGroup(cmd)
		select {
		case waitErr = <-done:
		case <-time.After(p.opts.GracePeriod):
			_ = killGroup(cmd)
			<-done
		}
		wg.Wait()
		return ctx.Err()
	}
	wg.Wait()

	if waitErr != nil {
		mu.Lock()
		out := strings.Join(tail, "\n")
		mu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &ErrEngine{Stage: stage, ExitCode: exitCode(waitErr), Output: out}
	}
	return nil
}

// lastRequestPath returns the directory holding the request files, which is
// the directory the stage should run in.
func (p *Pipeline) lastRequestPath(args []string) string {
	for i, a := range args {
		if a == "--request" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return filepath.Join(p.opts.WorkDir, "request.json")
}

// collectRequests finds the request{N}.json files produced by ace-lm, sorted
// numerically so request2 precedes request10. The stage's own input
// (request.json) is never an output and is excluded.
func collectRequests(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("engine: read work dir: %w", err)
	}
	var found []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if e.Name() == "request.json" || !strings.HasPrefix(e.Name(), "request") {
			continue
		}
		found = append(found, filepath.Join(dir, e.Name()))
	}
	sortRequests(found)
	return found, nil
}

// sortRequests orders request{N}.json numerically by N.
func sortRequests(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		return requestIndex(paths[i]) < requestIndex(paths[j])
	})
}

func requestIndex(path string) int {
	base := strings.TrimSuffix(filepath.Base(path), ".json")
	n, err := strconv.Atoi(strings.TrimPrefix(base, "request"))
	if err != nil {
		return 1 << 30
	}
	return n
}

// collectTracks moves finished audio out of the job directory into outputDir,
// renaming to baseName plus an index when more than one track was produced.
func collectTracks(jobDir, outputDir, baseName, ext string) ([]string, error) {
	entries, err := os.ReadDir(jobDir)
	if err != nil {
		return nil, fmt.Errorf("engine: read work dir: %w", err)
	}
	var produced []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if isAudio(e.Name()) {
			produced = append(produced, filepath.Join(jobDir, e.Name()))
		}
	}
	if len(produced) == 0 {
		return nil, nil
	}
	sort.Strings(produced)

	var tracks []string
	for i, src := range produced {
		name := baseName + ext
		if len(produced) > 1 {
			name = fmt.Sprintf("%s-%02d%s", baseName, i+1, ext)
		}
		dst := filepath.Join(outputDir, name)
		if err := moveFile(src, dst); err != nil {
			return nil, err
		}
		tracks = append(tracks, dst)
	}
	return tracks, nil
}

func isAudio(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp3", ".wav", ".flac", ".ogg", ".opus":
		return true
	}
	return false
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// Fall back to copy+truncate for cross-device moves.
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("engine: open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("engine: create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("engine: copy to %s: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("engine: close %s: %w", dst, err)
	}
	_ = os.Remove(src)
	return nil
}
