package engine

import (
	"regexp"
	"strconv"
	"strings"
)

// progress patterns matched against engine log lines. The C++ binaries do not
// emit a machine-readable progress channel, so these patterns are
// deliberately permissive: any match refines the bar, and a line that matches
// nothing is still forwarded verbatim to the user.
var (
	// "42%", " 42.5 %"
	rePercent = regexp.MustCompile(`(\d{1,3}(?:\.\d+)?)\s*%`)
	// "step 4/8", "step 4 of 8", "iter 12/50"
	reStepOf = regexp.MustCompile(`(?i)\b(?:step|steps|iter|iteration|chunk|sample)\s*(\d+)\s*(?:of|/)\s*(\d+)`)
	// "sampling: 12.5%", "lm = 40%"
	reBarLike = regexp.MustCompile(`(?i)\b([a-z_][a-z_ -]{1,23}?)\s*[:=]\s*(\d{1,3}(?:\.\d+)?)\s*%`)
)

// Progress is a single progress observation extracted from a log line.
type Progress struct {
	// Percent is completion in [0,100], or -1 when the line carried no
	// percentage at all.
	Percent float64
	// Step and Total come from "n/m" style counters, or 0 when absent.
	Step  int
	Total int
	// Label is an optional stage name such as "sampling" or "lm".
	Label string
	// Raw is the originating log line.
	Raw string
}

// Fraction returns completion in [0,1], or 0 when unknown. An explicit
// step/total counter takes precedence over a percentage, since it is the more
// precise signal.
func (p Progress) Fraction() float64 {
	if p.Total > 0 {
		return clamp(float64(p.Step)/float64(p.Total), 0, 1)
	}
	if p.Percent >= 0 {
		return clamp(p.Percent/100, 0, 1)
	}
	return 0
}

// Known reports whether the line carried usable progress information.
func (p Progress) Known() bool {
	return p.Percent >= 0 || p.Total > 0
}

// ParseProgress extracts progress from one log line. It returns ok=false when
// the line carries no progress signal, so callers can still echo the line
// without touching their bar.
func ParseProgress(line string) (Progress, bool) {
	p := Progress{Percent: -1, Raw: line}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return p, false
	}

	if m := reStepOf.FindStringSubmatch(trimmed); m != nil {
		step, err1 := strconv.Atoi(m[1])
		total, err2 := strconv.Atoi(m[2])
		if err1 == nil && err2 == nil && total > 0 {
			p.Step, p.Total = step, total
		}
	}

	if m := rePercent.FindStringSubmatch(trimmed); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			p.Percent = clamp(v, 0, 100)
		}
	}

	if m := reBarLike.FindStringSubmatch(trimmed); m != nil {
		p.Label = strings.TrimSpace(m[1])
		if p.Percent < 0 {
			if v, err := strconv.ParseFloat(m[2], 64); err == nil {
				p.Percent = clamp(v, 0, 100)
			}
		}
	}

	// An explicit percentage and an n/m counter are alternative encodings of
	// the same value. Keep the percentage and drop the counters so Fraction
	// cannot disagree with the line the user saw.
	if p.Percent >= 0 {
		p.Step, p.Total = 0, 0
	} else if p.Total > 0 {
		p.Percent = clamp(float64(p.Step)/float64(p.Total)*100, 0, 100)
	}

	if !p.Known() {
		return p, false
	}
	return p, true
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
