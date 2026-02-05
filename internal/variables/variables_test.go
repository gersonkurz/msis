package variables

import (
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
)

func TestNewDictionaryHasDefaults(t *testing.T) {
	d := New()

	if d.Get("PLATFORM") != "x64" {
		t.Errorf("expected PLATFORM=x64, got %s", d.Get("PLATFORM"))
	}

	if d.Get("ADD_TO_PATH") != "False" {
		t.Errorf("expected ADD_TO_PATH=False, got %s", d.Get("ADD_TO_PATH"))
	}
}

func TestLoadFromSetup(t *testing.T) {
	d := New()

	setup := &ir.Setup{
		Sets: []ir.Set{
			{Name: "PRODUCT_NAME", Value: "Test Product"},
			{Name: "PRODUCT_VERSION", Value: "1.0.0"},
			{Name: "PLATFORM", Value: "x86"}, // Override default
		},
	}

	d.LoadFromSetup(setup)

	if d.Get("PRODUCT_NAME") != "Test Product" {
		t.Errorf("expected 'Test Product', got %s", d.Get("PRODUCT_NAME"))
	}

	if d.Get("PLATFORM") != "x86" {
		t.Errorf("expected PLATFORM=x86 (overridden), got %s", d.Get("PLATFORM"))
	}
}

func TestResolve(t *testing.T) {
	d := New()
	d.Set("PRODUCT_NAME", "Test Product")
	d.Set("PRODUCT_VERSION", "1.0.0")

	result, err := d.Resolve("{{PRODUCT_NAME}} v{{PRODUCT_VERSION}}")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	expected := "Test Product v1.0.0"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestResolveAll(t *testing.T) {
	d := New()
	d.Set("PRODUCT_NAME", "Test Product")
	d.Set("PRODUCT_VERSION", "1.0.0")
	d.Set("FULL_NAME", "{{PRODUCT_NAME}} v{{PRODUCT_VERSION}}")

	err := d.ResolveAll()
	if err != nil {
		t.Fatalf("ResolveAll failed: %v", err)
	}

	expected := "Test Product v1.0.0"
	if d.Get("FULL_NAME") != expected {
		t.Errorf("expected %q, got %q", expected, d.Get("FULL_NAME"))
	}
}

func TestGetBool(t *testing.T) {
	d := New()

	tests := []struct {
		name     string
		value    string
		expected bool
	}{
		{"True", "True", true},
		{"true", "true", true},
		{"TRUE", "TRUE", true},
		{"Yes", "Yes", true},
		{"yes", "yes", true},
		{"On", "On", true},
		{"1", "1", true},
		{"False", "False", false},
		{"false", "false", false},
		{"No", "No", false},
		{"no", "no", false},
		{"Off", "Off", false},
		{"0", "0", false},
		{"empty", "", false},
		{"invalid", "invalid", false},
		// Mixed case (Codex review: must be case-insensitive)
		{"tRuE", "tRuE", true},
		{"yEs", "yEs", true},
		{"oN", "oN", true},
		{"FaLsE", "FaLsE", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d.Set("TEST_VAR", tt.value)
			result := d.GetBool("TEST_VAR")
			if result != tt.expected {
				t.Errorf("GetBool(%q) = %v, want %v", tt.value, result, tt.expected)
			}
		})
	}

	// Test missing variable
	if d.GetBool("NONEXISTENT") != false {
		t.Error("expected false for missing variable")
	}
}

func TestConvenienceMethods(t *testing.T) {
	d := New()
	d.Set("PRODUCT_NAME", "Test Product")
	d.Set("PRODUCT_VERSION", "1.0.0")
	d.Set("UPGRADE_CODE", "12345678-1234-1234-1234-123456789012")
	d.Set("MANUFACTURER", "Test Corp")
	d.Set("INSTALLDIR", "TestApp")
	d.Set("BUILD_TARGET", "test.msi")

	if d.ProductName() != "Test Product" {
		t.Errorf("expected 'Test Product', got %s", d.ProductName())
	}

	if d.ProductVersion() != "1.0.0" {
		t.Errorf("expected '1.0.0', got %s", d.ProductVersion())
	}

	if d.UpgradeCode() != "12345678-1234-1234-1234-123456789012" {
		t.Errorf("unexpected upgrade code")
	}

	if d.Manufacturer() != "Test Corp" {
		t.Errorf("expected 'Test Corp', got %s", d.Manufacturer())
	}

	if d.InstallDir() != "TestApp" {
		t.Errorf("expected 'TestApp', got %s", d.InstallDir())
	}

	if d.BuildTarget() != "test.msi" {
		t.Errorf("expected 'test.msi', got %s", d.BuildTarget())
	}
}

func TestContainsTemplate(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"{{VAR}}", true},
		{"Hello {{NAME}}", true},
		{"No template", false},
		{"{single brace}", false},
		{"", false},
		{"{", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := containsTemplate(tt.input)
			if result != tt.expected {
				t.Errorf("containsTemplate(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCheckDeprecated(t *testing.T) {
	// Test with no deprecated variables
	d := New()
	warnings := d.CheckDeprecated()
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for new dictionary, got %d", len(warnings))
	}

	// Test with INCLUDE_VCREDIST set
	d.Set("INCLUDE_VCREDIST", "True")
	warnings = d.CheckDeprecated()
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning for INCLUDE_VCREDIST, got %d", len(warnings))
	}
	if len(warnings) > 0 && !contains(warnings[0], "INCLUDE_VCREDIST") {
		t.Errorf("expected warning about INCLUDE_VCREDIST, got: %s", warnings[0])
	}
	if len(warnings) > 0 && !contains(warnings[0], "<requires") {
		t.Errorf("expected migration hint to <requires>, got: %s", warnings[0])
	}

	// Test with multiple deprecated variables
	d.Set("INCLUDE_VC140", "True")
	warnings = d.CheckDeprecated()
	if len(warnings) != 2 {
		t.Errorf("expected 2 warnings, got %d", len(warnings))
	}
	// Check that VC140 warning mentions backward compatibility
	foundVC140Warning := false
	for _, w := range warnings {
		if contains(w, "INCLUDE_VC140") && contains(w, "backward-compatible") {
			foundVC140Warning = true
			break
		}
	}
	if !foundVC140Warning {
		t.Error("expected VC140 warning to mention backward compatibility")
	}

	// Test with deprecated variable set to False (should not warn)
	d2 := New()
	d2.Set("INCLUDE_VCREDIST", "False")
	warnings = d2.CheckDeprecated()
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for deprecated variable set to False, got %d", len(warnings))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
