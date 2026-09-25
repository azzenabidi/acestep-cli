// Package request models the AceRequest JSON document consumed by the
// acestep.cpp CLI binaries (ace-lm, ace-synth).
//
// Every optional field is a pointer. A nil pointer is omitted from the
// marshalled JSON, which the engine treats as "use the field default".
// This distinction matters: several fields have non-zero defaults
// (seed is -1, latent_rescale is 1.0, peak_clip is 10), so an explicit
// zero must be transmitted rather than silently dropped.
package request

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Task types accepted by the engine's `task_type` field.
const (
	TaskText2Music = "text2music"
	TaskCover      = "cover"
	TaskRepaint    = "repaint"
	TaskLego       = "lego"
	TaskExtract    = "extract"
	TaskComplete   = "complete"
)

// LM instruction modes selected by the `lm_mode` field.
const (
	LMModeGenerate = "generate"
	LMModeInspire  = "inspire"
	LMModeFormat   = "format"
)

// Audio encoders selected by the `output_format` field.
const (
	FormatMP3   = "mp3"
	FormatWAV16 = "wav16"
	FormatWAV24 = "wav24"
	FormatWAV32 = "wav32"
)

// Instrumental is the sentinel lyrics value the DiT was trained on to mean
// "no vocals". It is passed through verbatim; it is not a flag.
const Instrumental = "[Instrumental]"

// Documented engine-side defaults, mirrored here for validation and help text.
const (
	DefaultDuration    = 120.0
	MinDuration        = 10.0
	MaxDuration        = 600.0
	DefaultLMModel     = "acestep-5Hz-lm-4B-Q8_0"
	DefaultSynthModel  = "acestep-v15-turbo-Q8_0"
	DefaultTextEncoder = "Qwen3-Embedding-0.6B-Q8_0"
	DefaultVAE         = "vae-BF16"
)

// Request is the on-disk request document. Field names and JSON tags mirror
// acestep.cpp docs/ARCHITECTURE.md exactly; renaming one breaks the engine
// contract.
type Request struct {
	// Text conditioning.
	Caption        string  `json:"caption"`
	Lyrics         *string `json:"lyrics,omitempty"`
	VocalLanguage  *string `json:"vocal_language,omitempty"`
	UseCoTCaption  *bool   `json:"use_cot_caption,omitempty"`
	LMNegativeText *string `json:"lm_negative_prompt,omitempty"`

	// Metadata. A nil/empty value asks the LLM to fill it in.
	BPM           *int     `json:"bpm,omitempty"`
	Duration      *float64 `json:"duration,omitempty"`
	KeyScale      *string  `json:"keyscale,omitempty"`
	TimeSignature *string  `json:"timesignature,omitempty"`

	// Sampling and batching.
	Seed            *int64   `json:"seed,omitempty"`
	LMBatchSize     *int     `json:"lm_batch_size,omitempty"`
	SynthBatchSize  *int     `json:"synth_batch_size,omitempty"`
	LMTemperature   *float64 `json:"lm_temperature,omitempty"`
	LMCFGScale      *float64 `json:"lm_cfg_scale,omitempty"`
	LMTopP          *float64 `json:"lm_top_p,omitempty"`
	LMTopK          *int     `json:"lm_top_k,omitempty"`
	InferenceSteps  *int     `json:"inference_steps,omitempty"`
	GuidanceScale   *float64 `json:"guidance_scale,omitempty"`
	Shift           *float64 `json:"shift,omitempty"`
	DCWScaler       *float64 `json:"dcw_scaler,omitempty"`
	DCWHighScaler   *float64 `json:"dcw_high_scaler,omitempty"`
	DCWMode         *string  `json:"dcw_mode,omitempty"`
	CustomTimesteps *string  `json:"custom_timesteps,omitempty"`
	Solver          *string  `json:"solver,omitempty"`

	// Cover / repaint / lego conditioning.
	AudioCodes         *string  `json:"audio_codes,omitempty"`
	AudioCoverStrength *float64 `json:"audio_cover_strength,omitempty"`
	CoverNoiseStrength *float64 `json:"cover_noise_strength,omitempty"`
	RepaintingStart    *int     `json:"repainting_start,omitempty"`
	RepaintingEnd      *int     `json:"repainting_end,omitempty"`
	LatentShift        *float64 `json:"latent_shift,omitempty"`
	LatentRescale      *float64 `json:"latent_rescale,omitempty"`

	// Routing and output.
	TaskType       *string  `json:"task_type,omitempty"`
	Track          *string  `json:"track,omitempty"`
	LMMode         *string  `json:"lm_mode,omitempty"`
	OutputFormat   *string  `json:"output_format,omitempty"`
	PeakClip       *int     `json:"peak_clip,omitempty"`
	MP3Bitrate     *int     `json:"mp3_bitrate,omitempty"`
	SynthModel     *string  `json:"synth_model,omitempty"`
	LMModel        *string  `json:"lm_model,omitempty"`
	Adapter        *string  `json:"adapter,omitempty"`
	AdapterScale   *float64 `json:"adapter_scale,omitempty"`
	MaxSeqOverride *int     `json:"-"`
}

// New returns a request with only the required caption set.
func New(caption string) *Request {
	return &Request{Caption: strings.TrimSpace(caption)}
}

// SetCaption replaces the caption.
func (r *Request) SetCaption(caption string) {
	r.Caption = strings.TrimSpace(caption)
}

// SetLyrics sets the lyrics field. Passing Instrumental suppresses vocals;
// passing "" asks the LLM to write lyrics from the caption.
func (r *Request) SetLyrics(lyrics string) {
	r.Lyrics = &lyrics
}

// SetInstrumental marks the track as having no vocals.
func (r *Request) SetInstrumental() {
	s := Instrumental
	r.Lyrics = &s
}

// SetDuration sets the target duration in seconds.
func (r *Request) SetDuration(seconds float64) {
	r.Duration = &seconds
}

// SetBPM sets the tempo. Zero leaves the choice to the LLM.
func (r *Request) SetBPM(bpm int) {
	r.BPM = &bpm
}

// SetKeyScale sets the musical key, e.g. "C major".
func (r *Request) SetKeyScale(key string) {
	r.KeyScale = &key
}

// SetTimeSignature sets the numerator, e.g. "4" for 4/4.
func (r *Request) SetTimeSignature(ts string) {
	r.TimeSignature = &ts
}

// SetVocalLanguage sets a BCP-47 code, or "unknown" for no specific language.
func (r *Request) SetVocalLanguage(lang string) {
	r.VocalLanguage = &lang
}

// SetSeed fixes the RNG seed for the DiT pipeline.
func (r *Request) SetSeed(seed int64) {
	r.Seed = &seed
}

// SetOutputFormat selects the audio encoder (see the Format* constants).
func (r *Request) SetOutputFormat(format string) {
	r.OutputFormat = &format
}

// SetLMMode selects the LM instruction (see the LMMode* constants).
func (r *Request) SetLMMode(mode string) {
	r.LMMode = &mode
}

// SetLMModel selects the planner LM by GGUF filename without extension.
func (r *Request) SetLMModel(name string) {
	r.LMModel = &name
}

// SetSynthModel selects the DiT by GGUF filename without extension.
func (r *Request) SetSynthModel(name string) {
	r.SynthModel = &name
}

// SetInferenceSteps overrides the DiT step count (8 for turbo, 50 for sft).
func (r *Request) SetInferenceSteps(steps int) {
	r.InferenceSteps = &steps
}

// SetBatchSizes sets the LM and DiT variation counts. The engine emits
// LMBatchSize * SynthBatchSize tracks.
func (r *Request) SetBatchSizes(lm, synth int) {
	r.LMBatchSize = &lm
	r.SynthBatchSize = &synth
}

// IsInstrumental reports whether the request suppresses vocals.
func (r *Request) IsInstrumental() bool {
	return r.Lyrics != nil && *r.Lyrics == Instrumental
}

// ValidTaskType reports whether t is a task type the engine accepts.
func ValidTaskType(t string) bool {
	switch t {
	case TaskText2Music, TaskCover, TaskRepaint, TaskLego, TaskExtract, TaskComplete:
		return true
	}
	return false
}

// ValidOutputFormat reports whether f is a supported audio encoder.
func ValidOutputFormat(f string) bool {
	switch f {
	case FormatMP3, FormatWAV16, FormatWAV24, FormatWAV32:
		return true
	}
	return false
}

// ValidLMMode reports whether m is a supported LM instruction mode.
func ValidLMMode(m string) bool {
	switch m {
	case LMModeGenerate, LMModeInspire, LMModeFormat:
		return true
	}
	return false
}

// OutputExtension maps an output format to its file extension. The engine
// picks the encoder from this field, not from the file name.
func OutputExtension(format string) string {
	switch format {
	case FormatWAV16, FormatWAV24, FormatWAV32:
		return ".wav"
	case FormatMP3:
		return ".mp3"
	}
	return ".mp3"
}

// Validate checks the invariants the engine relies on.
func (r *Request) Validate() error {
	if strings.TrimSpace(r.Caption) == "" {
		return fmt.Errorf("caption is required")
	}
	if r.Duration != nil {
		switch {
		case *r.Duration < 0:
			return fmt.Errorf("duration must be >= 0 (0 defers to the model), got %g", *r.Duration)
		case *r.Duration > 0 && (*r.Duration < MinDuration || *r.Duration > MaxDuration):
			return fmt.Errorf("duration %gs is outside the %g-%gs range the LM constrains to",
				*r.Duration, MinDuration, MaxDuration)
		}
	}
	if r.TaskType != nil && !ValidTaskType(*r.TaskType) {
		return fmt.Errorf("invalid task_type %q (want one of %s)", *r.TaskType, taskList())
	}
	if r.OutputFormat != nil && !ValidOutputFormat(*r.OutputFormat) {
		return fmt.Errorf("invalid output_format %q (want one of mp3, wav16, wav24, wav32)", *r.OutputFormat)
	}
	if r.LMMode != nil && !ValidLMMode(*r.LMMode) {
		return fmt.Errorf("invalid lm_mode %q (want generate, inspire or format)", *r.LMMode)
	}
	if r.LMBatchSize != nil && *r.LMBatchSize < 1 {
		return fmt.Errorf("lm_batch_size must be >= 1, got %d", *r.LMBatchSize)
	}
	if r.SynthBatchSize != nil && *r.SynthBatchSize < 1 {
		return fmt.Errorf("synth_batch_size must be >= 1, got %d", *r.SynthBatchSize)
	}
	needsSource := r.TaskType != nil &&
		(*r.TaskType == TaskCover || *r.TaskType == TaskRepaint ||
			*r.TaskType == TaskLego || *r.TaskType == TaskExtract || *r.TaskType == TaskComplete)
	if needsSource && r.AudioCodes == nil && (r.Track == nil || *r.Track == "") {
		return fmt.Errorf("task_type %q needs source audio (--src-audio) or pre-computed codes", *r.TaskType)
	}
	return nil
}

func taskList() string {
	names := []string{TaskText2Music, TaskCover, TaskRepaint, TaskLego, TaskExtract, TaskComplete}
	return strings.Join(names, ", ")
}

// JSON renders the request as the indented JSON document handed to the engine.
func (r *Request) JSON() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	buf, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return append(buf, '\n'), nil
}

// WriteFile serialises the request to path.
func (r *Request) WriteFile(path string) error {
	data, err := r.JSON()
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// ReadFile parses a request document previously produced by the engine
// (ace-lm writes request{N}.json with lyrics, metadata and audio codes filled
// in). Unknown fields are preserved so round-tripping does not lose data.
func ReadFile(path string) (*Request, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var r Request
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &r, nil
}
