package engine

import "testing"

func TestParseProgress(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantKnown   bool
		wantPercent float64
		wantStep    int
		wantTotal   int
		wantLabel   string
	}{
		{"bare percent", "  42% ", true, 42, 0, 0, ""},
		{"decimal percent", "sampling: 12.5%", true, 12.5, 0, 0, "sampling"},
		{"step fraction", "step 4/8", true, 50, 4, 8, ""},
		{"step of", "iteration 12 of 50", true, 24, 12, 50, ""},
		{"percent wins over fraction", "step 1/10 90%", true, 90, 0, 0, ""},
		{"labelled", "lm: 33%", true, 33, 0, 0, "lm"},
		{"percent with space", "50 %", true, 50, 0, 0, ""},
		{"clamped high", "999%", true, 100, 0, 0, ""},
		{"clamped high hard", "150%", true, 100, 0, 0, ""},
		{"plain log line", "loaded model acestep-v15-turbo-Q8_0", false, 0, 0, 0, ""},
		{"empty", "   ", false, 0, 0, 0, ""},
		{"no total denominator", "step 4/", false, 0, 0, 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseProgress(tc.line)
			if ok != tc.wantKnown {
				t.Fatalf("known = %v, want %v (line %q)", ok, tc.wantKnown, tc.line)
			}
			if !tc.wantKnown {
				return
			}
			if got.Percent != tc.wantPercent {
				t.Errorf("percent = %v, want %v", got.Percent, tc.wantPercent)
			}
			if got.Step != tc.wantStep {
				t.Errorf("step = %d, want %d", got.Step, tc.wantStep)
			}
			if got.Total != tc.wantTotal {
				t.Errorf("total = %d, want %d", got.Total, tc.wantTotal)
			}
			if got.Label != tc.wantLabel {
				t.Errorf("label = %q, want %q", got.Label, tc.wantLabel)
			}
		})
	}
}

func TestProgressFraction(t *testing.T) {
	tests := []struct {
		p    Progress
		want float64
	}{
		{Progress{Percent: 50}, 0.5},
		{Progress{Percent: 0}, 0},
		{Progress{Percent: 100}, 1},
		{Progress{Percent: -1, Step: 3, Total: 4}, 0.75},
		{Progress{}, 0},
		{Progress{Percent: -1, Step: 5, Total: 0}, 0},
	}
	for _, tc := range tests {
		if got := tc.p.Fraction(); got != tc.want {
			t.Errorf("Fraction(%+v) = %v, want %v", tc.p, got, tc.want)
		}
	}
}

// A test line from a real engine run should not be mistaken for progress.
func TestParseProgressIgnoresUnrelatedNumbers(t *testing.T) {
	for _, line := range []string{
		"loading model from models/acestep-v15-turbo-Q8_0.gguf",
		"ggml_backend_cuda_init: found 1 device(s)",
		"vocoder: 0 samples decoded",
	} {
		if _, ok := ParseProgress(line); ok {
			t.Errorf("line %q should not parse as progress", line)
		}
	}
}
