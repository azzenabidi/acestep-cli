package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// colourEnabled caches whether ANSI escapes should be written.
var colourEnabled = func() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return term.IsTerminal(int(os.Stderr.Fd()))
}()

// noColor disables colour for the rest of the process.
func noColor() { colourEnabled = false }

// styler returns a decorator that is a no-op when colour is disabled.
func styler(code string) func(string) string {
	if !colourEnabled {
		return func(s string) string { return s }
	}
	return func(s string) string { return "\x1b[" + code + "m" + s + "\x1b[0m" }
}

var (
	bold  = styler("1")
	dim   = styler("2")
	red   = styler("31")
	green = styler("32")
	cyan  = styler("36")
)

// stdout is where normal command output goes.
func stdout() io.Writer { return os.Stdout }

// stderr is where progress and diagnostics go, keeping stdout clean for pipes.
func stderr() io.Writer { return os.Stderr }

// info prints a progress line to stderr unless --quiet.
func info(format string, args ...any) {
	if global.quiet {
		return
	}
	fmt.Fprintf(stderr(), format+"\n", args...)
}

// step prints a numbered step to stderr unless --quiet.
func step(n int, format string, args ...any) {
	if global.quiet {
		return
	}
	fmt.Fprintf(stderr(), "%s %d.%s %s\n", dim("›"), n, dim(""), fmt.Sprintf(format, args...))
}

// warn prints a warning to stderr.
func warn(format string, args ...any) {
	fmt.Fprintf(stderr(), "%s %s\n", yellow(), fmt.Sprintf(format, args...))
}

func yellow() string {
	if !colourEnabled {
		return "warning:"
	}
	return "\x1b[33mwarning:\x1b[0m"
}

// success prints a completed item to stderr.
func success(format string, args ...any) {
	if global.quiet {
		return
	}
	fmt.Fprintf(stderr(), "%s %s\n", green("✓"), fmt.Sprintf(format, args...))
}

// fail prints an error to stderr.
func fail(format string, args ...any) {
	fmt.Fprintf(stderr(), "%s %s\n", red("✗"), fmt.Sprintf(format, args...))
}

// hint prints a dimmed suggestion line.
func hint(format string, args ...any) {
	fmt.Fprintf(stderr(), "%s\n", dim(fmt.Sprintf(format, args...)))
}

// heading prints a section header to stderr.
func heading(s string) {
	if global.quiet {
		return
	}
	fmt.Fprintf(stderr(), "\n%s\n%s\n", bold(s), dim(strings.Repeat("─", len([]rune(s)))))
}

// emitJSON writes a value to stdout as indented JSON, honouring --json.
func emitJSON(v any) error {
	enc := json.NewEncoder(stdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
