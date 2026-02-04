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
