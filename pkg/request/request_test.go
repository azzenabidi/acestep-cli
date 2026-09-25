package request

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The engine treats an omitted field as "use the default", so the critical
// invariant is that an unset field never reaches the JSON while an explicit
// zero always does.
func TestOmittedFieldsUseEngineDefaults(t *testing.T) {
	r := New("lofi synthwave")
	data, err := r.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Only caption should be present.
	if len(got) != 1 {
		t.Fatalf("expected only caption in %s, got keys %v", data, keys(got))
	}
	if got["caption"] != "lofi synthwave" {
		t.Errorf("caption = %v, want %q", got["caption"], "lofi synthwave")
	}
}

// Several fields have non-zero engine defaults, so an explicit zero must be
// transmitted. Dropping it would silently change the user's request.
func TestExplicitZeroIsTransmitted(t *testing.T) {
	cases := []struct {
		name  string
		apply func(*Request)
		key   string
		want  any
	}{
		{"seed", func(r *Request) { r.SetSeed(0) }, "seed", float64(0)},
		{"peak_clip", func(r *Request) { v := 0; r.PeakClip = &v }, "peak_clip", float64(0)},
		{"mp3_bitrate", func(r *Request) { v := 320; r.MP3Bitrate = &v }, "mp3_bitrate", float64(320)},
		{"latent_rescale", func(r *Request) { v := 0.0; r.LatentRescale = &v }, "latent_rescale", float64(0)},
		{"audio_cover_strength", func(r *Request) { v := 0.0; r.AudioCoverStrength = &v }, "audio_cover_strength", float64(0)},
		{"guidance_scale", func(r *Request) { v := 0.0; r.GuidanceScale = &v }, "guidance_scale", float64(0)},
		{"shift", func(r *Request) { v := 0.0; r.Shift = &v }, "shift", float64(0)},
		{"duration", func(r *Request) { r.SetDuration(0) }, "duration", float64(0)},
		{"lyrics", func(r *Request) { r.SetLyrics("") }, "lyrics", ""},
		{"use_cot_caption", func(r *Request) { v := false; r.UseCoTCaption = &v }, "use_cot_caption", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New("test")
			tc.apply(r)
			data, err := r.JSON()
			if err != nil {
				t.Fatalf("JSON: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			v, ok := got[tc.key]
			if !ok {
				t.Fatalf("%s missing from %s", tc.key, data)
			}
			if v != tc.want {
				t.Errorf("%s = %#v, want %#v", tc.key, v, tc.want)
			}
		})
	}
}

// Field names are a hard contract with the C++ engine; a rename is a silent
// runtime break, so pin the exact spelling.
func TestJSONFieldNamesMatchEngineContract(t *testing.T) {
	r := New("test")
	r.SetLyrics("[Instrumental]")
	r.SetDuration(30)
	r.SetBPM(120)
	r.SetKeyScale("C major")
	r.SetTimeSignature("4")
	r.SetVocalLanguage("fr")
	r.SetSeed(42)
	r.SetOutputFormat(FormatWAV24)
	r.SetLMModel("acestep-5Hz-lm-4B-Q8_0")
	r.SetSynthModel("acestep-v15-turbo-Q8_0")
	r.SetInferenceSteps(8)
	r.SetBatchSizes(2, 3)

	data, err := r.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	for _, key := range []string{
		"caption", "lyrics", "duration", "bpm", "keyscale", "timesignature",
		"vocal_language", "seed", "output_format", "lm_model", "synth_model",
		"inference_steps", "lm_batch_size", "synth_batch_size",
	} {
		if !strings.Contains(string(data), `"`+key+`"`) {
			t.Errorf("missing %q in marshalled request:\n%s", key, data)
		}
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		req  *Request
		want string
	}{
		{"empty caption", &Request{}, "caption"},
		{"negative duration", func() *Request { r := New("x"); r.SetDuration(-1); return r }(), "duration"},
		{"duration too long", func() *Request { r := New("x"); r.SetDuration(601); return r }(), "duration"},
		{"bad task", func() *Request { r := New("x"); v := "sing"; r.TaskType = &v; return r }(), "task_type"},
		{"bad format", func() *Request { r := New("x"); v := "flac"; r.OutputFormat = &v; return r }(), "output_format"},
		{"bad lm mode", func() *Request { r := New("x"); v := "guess"; r.LMMode = &v; return r }(), "lm_mode"},
		{"zero batch", func() *Request { r := New("x"); r.SetBatchSizes(0, 1); return r }(), "lm_batch_size"},
		{"cover without source", func() *Request { r := New("x"); v := TaskCover; r.TaskType = &v; return r }(), "src-audio"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.Validate()
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestValidateAcceptsDurationBounds(t *testing.T) {
	// The engine's FSM constrains the LM to [10,600]s.
	for _, d := range []float64{0, 10, 300, 600} {
		r := New("x")
		r.SetDuration(d)
		if err := r.Validate(); err != nil {
			t.Errorf("duration %g: unexpected error %v", d, err)
		}
	}
}

// Instrumental is a magic string the DiT was trained on; it must survive
// round-tripping exactly.
func TestInstrumentalSentinel(t *testing.T) {
	r := New("test")
	if r.IsInstrumental() {
		t.Error("a fresh request should not be instrumental")
	}
	r.SetInstrumental()
	if !r.IsInstrumental() {
		t.Error("SetInstrumental did not take effect")
	}
	data, err := r.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(string(data), Instrumental) {
		t.Errorf("marshalled request lost the instrumental sentinel:\n%s", data)
	}
}

func TestReadFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "request0.json")

	orig := New("orchestral fanfare")
	orig.SetLyrics("line one\nline two")
	orig.SetDuration(45)
	orig.SetBPM(90)
	orig.SetSeed(1234)
	if err := orig.WriteFile(path); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got.Caption != orig.Caption {
		t.Errorf("caption = %q, want %q", got.Caption, orig.Caption)
	}
	if got.Lyrics == nil || *got.Lyrics != *orig.Lyrics {
		t.Errorf("lyrics = %v, want %q", got.Lyrics, *orig.Lyrics)
	}
	if got.Duration == nil || *got.Duration != 45 {
		t.Errorf("duration = %v, want 45", got.Duration)
	}
	if got.Seed == nil || *got.Seed != 1234 {
		t.Errorf("seed = %v, want 1234", got.Seed)
	}
}

func TestWriteFileCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "request.json")
	if err := New("x").WriteFile(path); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat: %v", err)
	}
}

func TestOutputExtension(t *testing.T) {
	for format, want := range map[string]string{
		FormatMP3:   ".mp3",
		FormatWAV16: ".wav",
		FormatWAV24: ".wav",
		FormatWAV32: ".wav",
	} {
		if got := OutputExtension(format); got != want {
			t.Errorf("OutputExtension(%q) = %q, want %q", format, got, want)
		}
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
