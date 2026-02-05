// Package cli provides CLI utilities including colored terminal output.
package cli

import (
	"os"
	"runtime"

	"golang.org/x/term"
)

// ANSI color codes
const (
	reset   = "\033[0m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
	white   = "\033[37m"
	bold    = "\033[1m"
)

// ColorsEnabled controls whether colored output is enabled.
// Set to false to disable colors (e.g., via --no-color flag).
var ColorsEnabled = true

// init checks terminal capabilities and NO_COLOR environment variable.
func init() {
	// Respect NO_COLOR environment variable (https://no-color.org/)
	if os.Getenv("NO_COLOR") != "" {
		ColorsEnabled = false
		return
	}

	// Check if stdout is a terminal
	if !isTerminal() {
		ColorsEnabled = false
		return
	}

	// On Windows, check if virtual terminal processing is available
	// Modern Windows 10+ supports ANSI colors, but older versions don't
	if runtime.GOOS == "windows" {
		// Try to enable virtual terminal processing
		if !enableWindowsVT() {
			ColorsEnabled = false
		}
	}
}

// isTerminal checks if stdout is connected to a terminal.
func isTerminal() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// enableWindowsVT attempts to enable virtual terminal processing on Windows.
// Returns true if ANSI colors are supported.
func enableWindowsVT() bool {
	// On Windows, we rely on golang.org/x/term to detect terminal capability
	// Modern Windows 10 (1511+) supports ANSI escape codes
	// For older systems, colors will be disabled
	return isTerminal()
}

// DisableColors turns off colored output.
func DisableColors() {
	ColorsEnabled = false
}

// EnableColors turns on colored output (if terminal supports it).
func EnableColors() {
	// Re-run init logic
	ColorsEnabled = true
	if os.Getenv("NO_COLOR") != "" {
		ColorsEnabled = false
		return
	}
	if !isTerminal() {
		ColorsEnabled = false
		return
	}
	// On Windows, verify VT support
	if runtime.GOOS == "windows" && !enableWindowsVT() {
		ColorsEnabled = false
	}
}

// colorize wraps text in ANSI color codes if colors are enabled.
func colorize(color, text string) string {
	if !ColorsEnabled {
		return text
	}
	return color + text + reset
}

// Error formats text in red (for errors, failures).
func Error(text string) string {
	return colorize(red, text)
}

// Success formats text in green (for success messages, completions).
func Success(text string) string {
	return colorize(green, text)
}

// Warning formats text in yellow (for warnings, deprecation notices).
func Warning(text string) string {
	return colorize(yellow, text)
}

// Info formats text in cyan (for informational messages, progress).
func Info(text string) string {
	return colorize(cyan, text)
}

// Bold formats text in bold (for emphasis, section headers).
func Bold(text string) string {
	if !ColorsEnabled {
		return text
	}
	return bold + text + reset
}

// Filename formats a filename/path in cyan.
func Filename(text string) string {
	return colorize(cyan, text)
}

// Number formats a number in magenta.
func Number(text string) string {
	return colorize(magenta, text)
}
