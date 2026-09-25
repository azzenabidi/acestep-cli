package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/azzenabidi/acestep-cli/pkg/request"
)

// The pipeline is a contract with two external C++ binaries. To test it
// without compiling them, the test binary re-executes itself as a stand-in
// engine: TestMain dispatches on ACESTEP_FAKE_ENGINE and the binary's own
// name decides whether it plays ace-lm or ace-synth.
const fakeEnv = "ACESTEP_FAKE_ENGINE"

func TestMain(m *testing.M) {
	switch os.Getenv(fakeEnv) {
	case "fail-lm":
		fmt.Fprintln(os.Stdout, "error: failed to load LM")
		os.Exit(3)
	case "hang-lm":
		// Simulate a long-running stage so cancellation can be tested.
		time.Sleep(10 * time.Minute)
		os.Exit(0)
	case "ok":
		os.Exit(runFakeEngine())
	}
	os.Exit(m.Run())
}

// runFakeEngine mimics ace-lm and ace-synth closely enough to exercise the
// supervisor: numbered outputs, the request0.json -> request00.mp3 naming
// scheme, and progress lines on both streams.
func runFakeEngine() int {
	name := strings.ToLower(filepath.Base(os.Args[0]))

	var requests []string
	for i, a := range os.Args {
		if a == "--request" {
			for _, next := range os.Args[i+1:] {
				if strings.HasPrefix(next, "--") {
					break
				}
				requests = append(requests, next)
			}
			break
		}
	}

	switch {
	case strings.Contains(name, "ace-lm"):
		return fakeLM(requests)
	case strings.Contains(name, "ace-synth"):
		return fakeSynth(requests)
	}
	fmt.Fprintf(os.Stderr, "fake engine: unrecognised name %q\n", name)
	return 2
}

func fakeLM(requests []string) int {
	if len(requests) == 0 {
		fmt.Fprintln(os.Stderr, "ace-lm: --request is required")
		return 2
	}
	for _, path := range requests {
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ace-lm: %v\n", err)
			return 1
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			fmt.Fprintf(os.Stderr, "ace-lm: bad request: %v\n", err)
			return 1
		}
		batch := 1
		if v, ok := doc["lm_batch_size"].(float64); ok && v > 0 {
			batch = int(v)
		}
		// Progress on stdout, diagnostics on stderr: the supervisor must
		// consume both.
		fmt.Fprintln(os.Stderr, "loading models from --models dir")
		for i := 0; i <= batch; i++ {
			fmt.Fprintf(os.Stdout, "step %d/%d\n", i, batch)
		}
		for i := 0; i < batch; i++ {
			doc["lyrics"] = "generated line one\ngenerated line two"
			doc["bpm"] = 120
			doc["audio_codes"] = "1,2,3,4"
			doc["keyscale"] = "C major"
			out := filepath.Join(filepath.Dir(path), fmt.Sprintf("request%d.json", i))
			body, _ := json.MarshalIndent(doc, "", "  ")
			if err := os.WriteFile(out, append(body, '\n'), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "ace-lm: %v\n", err)
				return 1
			}
		}
	}
	return 0
}

func fakeSynth(requests []string) int {
	if len(requests) == 0 {
		fmt.Fprintln(os.Stderr, "ace-synth: --request is required")
		return 2
	}
	// The engine writes into its working directory, which the supervisor
	// sets to the job directory.
	for lmIdx, path := range requests {
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ace-synth: %v\n", err)
			return 1
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			fmt.Fprintf(os.Stderr, "ace-synth: bad request: %v\n", err)
			return 1
		}
		format, _ := doc["output_format"].(string)
		ext := request.OutputExtension(format)
		batch := 1
		if v, ok := doc["synth_batch_size"].(float64); ok && v > 0 {
			batch = int(v)
		}
		for b := 0; b < batch; b++ {
			fmt.Fprintf(os.Stdout, "sampling %d/%d  %d%%\n", b+1, batch, (b+1)*100/batch)
			name := fmt.Sprintf("request%d%d%s", lmIdx, b, ext)
			// A real file so the collector treats it as a track.
			if err := os.WriteFile(name, []byte("RIFF....WAVE fake audio"), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "ace-synth: %v\n", err)
				return 1
			}
		}
	}
	return 0
}

// fakeEngine installs the test binary as ace-lm and ace-synth in a temp bin
// directory and returns the paths.
func fakeEngine(t *testing.T, mode string) (lmPath, synthPath string) {
	t.Helper()
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		t.Skip("cannot re-exec the test binary on " + runtime.GOOS)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	bin := t.TempDir()
	lmPath = filepath.Join(bin, "ace-lm")
	synthPath = filepath.Join(bin, "ace-synth")
	if runtime.GOOS == "windows" {
		lmPath += ".exe"
		synthPath += ".exe"
	}
	for _, dst := range []string{lmPath, synthPath} {
		body, err := os.ReadFile(self)
		if err != nil {
			t.Fatalf("read test binary: %v", err)
		}
		if err := os.WriteFile(dst, body, 0o755); err != nil {
			t.Fatalf("install fake engine: %v", err)
		}
	}
	t.Setenv(fakeEnv, mode)
	return lmPath, synthPath
}

// newTestPipeline wires a pipeline over the fake engine and temp dirs.
func newTestPipeline(t *testing.T, mode string, mutate func(*Options)) *Pipeline {
	t.Helper()
	lm, synth := fakeEngine(t, mode)
	models := t.TempDir()
	for _, name := range []string{
		"acestep-5Hz-lm-4B-Q8_0.gguf",
		"acestep-v15-turbo-Q8_0.gguf",
		"vae-BF16.gguf",
		"Qwen3-Embedding-0.6B-Q8_0.gguf",
	} {
		if err := os.WriteFile(filepath.Join(models, name), []byte("GGUF"), 0o644); err != nil {
			t.Fatalf("seed model: %v", err)
		}
	}
	opts := Options{
		LMPath:         lm,
		SynthPath:      synth,
		ModelsDir:      models,
		WorkDir:        t.TempDir(),
		OutputDir:      t.TempDir(),
		GracePeriod:    2 * time.Second,
		StreamProgress: true,
	}
	if mutate != nil {
		mutate(&opts)
	}
	p, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestGenerateHappyPath(t *testing.T) {
	var progressLines int
	p := newTestPipeline(t, "ok", func(o *Options) {
		o.OnProgress = func(Stage, Progress) { progressLines++ }
	})
	if err := p.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}

	req := request.New("lofi synthwave with relaxing piano")
	req.SetOutputFormat(request.FormatMP3)
	req.SetDuration(30)

	res, err := p.Generate(context.Background(), req, "my-track")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(res.Tracks) != 1 {
		t.Fatalf("got %d tracks, want 1: %v", len(res.Tracks), res.Tracks)
	}
	got := filepath.Base(res.Tracks[0])
	if got != "my-track.mp3" {
		t.Errorf("track name = %q, want my-track.mp3", got)
	}
	if _, err := os.Stat(res.Tracks[0]); err != nil {
		t.Errorf("track not on disk: %v", err)
	}
	if progressLines == 0 {
		t.Error("expected progress observations from the fake engine logs")
	}
	if len(res.Requests) != 1 {
		t.Errorf("got %d intermediate requests, want 1: %v", len(res.Requests), res.Requests)
	}
}

// request0.json -> request00.mp3 and request1.json -> request10.mp3.
func TestGenerateVariationsAreAllCollected(t *testing.T) {
	p := newTestPipeline(t, "ok", nil)

	req := request.New("jazz trio")
	req.SetOutputFormat(request.FormatMP3)
	req.SetBatchSizes(3, 1)

	res, err := p.Generate(context.Background(), req, "jazz")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(res.Requests) != 3 {
		t.Errorf("got %d intermediate requests, want 3: %v", len(res.Requests), res.Requests)
	}
	if len(res.Tracks) != 3 {
		t.Fatalf("got %d tracks, want 3: %v", len(res.Tracks), res.Tracks)
	}
	// Multiple tracks must be suffixed so they do not overwrite each other.
	seen := map[string]bool{}
	for _, tr := range res.Tracks {
		if seen[tr] {
			t.Errorf("duplicate track path %s", tr)
		}
		seen[tr] = true
		if !strings.HasPrefix(filepath.Base(tr), "jazz-") {
			t.Errorf("track %q is not suffixed", filepath.Base(tr))
		}
	}
}

func TestGenerateRespectsOutputFormat(t *testing.T) {
	p := newTestPipeline(t, "ok", nil)
	req := request.New("fanfare")
	req.SetOutputFormat(request.FormatWAV24)

	res, err := p.Generate(context.Background(), req, "fanfare")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := filepath.Ext(res.Tracks[0]); got != ".wav" {
		t.Errorf("extension = %q, want .wav", got)
	}
}

// The intermediate request JSON is the engine's contract, so it must land in
// the workspace and be cleaned up afterwards.
func TestGenerateCleansUpWorkspace(t *testing.T) {
	p := newTestPipeline(t, "ok", nil)
	req := request.New("test")
	if _, err := p.Generate(context.Background(), req, "t"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	entries, err := os.ReadDir(p.opts.WorkDir)
	if err != nil {
		t.Fatalf("read work dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("work dir not cleaned: %v", names)
	}
}

func TestGeneratePropagatesEngineFailure(t *testing.T) {
	p := newTestPipeline(t, "fail-lm", nil)
	_, err := p.Generate(context.Background(), request.New("test"), "t")
	if err == nil {
		t.Fatal("expected an error from a failing ace-lm")
	}
	var ee *ErrEngine
	if !asEngineError(err, &ee) {
		t.Fatalf("error %T is not *ErrEngine: %v", err, err)
	}
	if ee.Stage != StageLM {
		t.Errorf("stage = %q, want %q", ee.Stage, StageLM)
	}
	if ee.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", ee.ExitCode)
	}
	// The captured tail should make the failure diagnosable.
	if !strings.Contains(ee.Output, "failed to load LM") {
		t.Errorf("error output did not capture the engine message: %q", ee.Output)
	}
}

// A cancelled run must return promptly and leave no engine process behind.
func TestGenerateCancellationIsPrompt(t *testing.T) {
	p := newTestPipeline(t, "hang-lm", nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.Generate(ctx, request.New("test"), "t")
		done <- err
	}()

	// Let the stage start before cancelling.
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a cancellation error")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Generate did not return within 15s of cancellation; the engine was probably not signalled")
	}
}

func TestCheckReportsMissingBinary(t *testing.T) {
	dir := t.TempDir()
	models := t.TempDir()
	if err := os.WriteFile(filepath.Join(models, "a.gguf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := New(Options{
		LMPath:      filepath.Join(dir, "ace-lm"),
		SynthPath:   filepath.Join(dir, "ace-synth"),
		ModelsDir:   models,
		WorkDir:     dir,
		OutputDir:   dir,
		GracePeriod: time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = p.Check()
	var missing *ErrMissingBinary
	if !asMissingBinary(err, &missing) {
		t.Fatalf("expected ErrMissingBinary, got %v", err)
	}
	if missing.Stage != StageLM {
		t.Errorf("stage = %q, want %q", missing.Stage, StageLM)
	}
}

func TestCheckRequiresModels(t *testing.T) {
	lm, synth := fakeEngine(t, "ok")
	empty := t.TempDir()
	p, err := New(Options{
		LMPath: lm, SynthPath: synth,
		ModelsDir: empty, WorkDir: empty, OutputDir: empty,
		GracePeriod: time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Check(); err == nil {
		t.Fatal("expected Check to fail with no .gguf models present")
	}
}

func TestNewValidatesOptions(t *testing.T) {
	cases := map[string]Options{
		"no lm":     {SynthPath: "s", ModelsDir: "m", WorkDir: "w", OutputDir: "o"},
		"no synth":  {LMPath: "l", ModelsDir: "m", WorkDir: "w", OutputDir: "o"},
		"no models": {LMPath: "l", SynthPath: "s", WorkDir: "w", OutputDir: "o"},
		"no work":   {LMPath: "l", SynthPath: "s", ModelsDir: "m", OutputDir: "o"},
		"no output": {LMPath: "l", SynthPath: "s", ModelsDir: "m", WorkDir: "w"},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(opts); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestCollectRequestsSortsNumerically(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"request.json", "request0.json", "request2.json", "request10.json", "request1.json", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := collectRequests(dir)
	if err != nil {
		t.Fatalf("collectRequests: %v", err)
	}
	want := []string{"request0.json", "request1.json", "request2.json", "request10.json"}
	if len(got) != len(want) {
		t.Fatalf("got %d files %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if filepath.Base(got[i]) != want[i] {
			t.Errorf("position %d = %s, want %s", i, filepath.Base(got[i]), want[i])
		}
	}
}

func TestSkipLMRunsSynthDirectly(t *testing.T) {
	p := newTestPipeline(t, "ok", func(o *Options) { o.SkipLM = true })
	req := request.New("direct render")
	codes := "9,8,7"
	req.AudioCodes = &codes

	res, err := p.Generate(context.Background(), req, "direct")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(res.Tracks) != 1 {
		t.Errorf("got %d tracks, want 1", len(res.Tracks))
	}
}

func TestSynthArgsIncludeOptionalFlags(t *testing.T) {
	p := newTestPipeline(t, "ok", func(o *Options) {
		o.SRCAudio = "in.wav"
		o.RefAudio = "ref.wav"
		o.AdaptersDir = "adapters"
		o.VAEChunk = 256
		o.VAEOverlap = 32
	})
	args := p.synthArgs([]string{"request0.json"})
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"--models", "--request request0.json",
		"--src-audio in.wav", "--ref-audio ref.wav",
		"--adapters adapters", "--vae-chunk 256", "--vae-overlap 32",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("synth args %q missing %q", joined, want)
		}
	}

	// Optional flags must not appear when unset.
	bare := newTestPipeline(t, "ok", nil)
	bareArgs := strings.Join(bare.synthArgs([]string{"r0.json"}), " ")
	for _, unwanted := range []string{"--src-audio", "--ref-audio", "--adapters", "--vae-chunk"} {
		if strings.Contains(bareArgs, unwanted) {
			t.Errorf("synth args %q should omit %q", bareArgs, unwanted)
		}
	}
}

// asEngineError and asMissingBinary keep the tests readable without importing
// errors into every call site.
func asEngineError(err error, target **ErrEngine) bool { return errors.As(err, target) }
func asMissingBinary(err error, target **ErrMissingBinary) bool {
	return errors.As(err, target)
}
