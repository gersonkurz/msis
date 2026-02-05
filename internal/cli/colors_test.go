package cli

import (
	"os"
	"testing"
)

func TestColorFunctions(t *testing.T) {
	// Force colors enabled for testing
	ColorsEnabled = true

	tests := []struct {
		name     string
		fn       func(string) string
		input    string
		contains string
	}{
		{"Error", Error, "test", "\033[31m"},
		{"Success", Success, "test", "\033[32m"},
		{"Warning", Warning, "test", "\033[33m"},
		{"Info", Info, "test", "\033[36m"},
		{"Bold", Bold, "test", "\033[1m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.fn(tt.input)
			if !containsStr(result, tt.contains) {
				t.Errorf("%s(%q) = %q, expected to contain %q", tt.name, tt.input, result, tt.contains)
			}
			if !containsStr(result, tt.input) {
				t.Errorf("%s(%q) = %q, expected to contain input text", tt.name, tt.input, result)
			}
			if !containsStr(result, reset) {
				t.Errorf("%s(%q) = %q, expected to contain reset code", tt.name, tt.input, result)
			}
		})
	}
}

func TestColorsDisabled(t *testing.T) {
	// Disable colors
	ColorsEnabled = false
	defer func() { ColorsEnabled = true }()

	result := Error("test")
	if result != "test" {
		t.Errorf("Error with colors disabled: expected 'test', got %q", result)
	}

	result = Success("test")
	if result != "test" {
		t.Errorf("Success with colors disabled: expected 'test', got %q", result)
	}

	result = Warning("test")
	if result != "test" {
		t.Errorf("Warning with colors disabled: expected 'test', got %q", result)
	}

	result = Bold("test")
	if result != "test" {
		t.Errorf("Bold with colors disabled: expected 'test', got %q", result)
	}
}

func TestNoColorEnv(t *testing.T) {
	// Save original state
	originalEnabled := ColorsEnabled
	originalNoColor := os.Getenv("NO_COLOR")
	defer func() {
		ColorsEnabled = originalEnabled
		if originalNoColor == "" {
			os.Unsetenv("NO_COLOR")
		} else {
			os.Setenv("NO_COLOR", originalNoColor)
		}
	}()

	// Set NO_COLOR and re-enable
	os.Setenv("NO_COLOR", "1")
	EnableColors()

	if ColorsEnabled {
		t.Error("expected colors to be disabled when NO_COLOR is set")
	}
}

func TestDisableEnableColors(t *testing.T) {
	// Test DisableColors
	ColorsEnabled = true
	DisableColors()
	if ColorsEnabled {
		t.Error("DisableColors should set ColorsEnabled to false")
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
