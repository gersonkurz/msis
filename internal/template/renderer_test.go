package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/generator"
	"github.com/gersonkurz/msis/internal/variables"
)

func TestGetLCID(t *testing.T) {
	vars := variables.New()
	r := NewRenderer(vars, "", "", nil)

	tests := []struct {
		language string
		wantLCID string
	}{
		{"en-us", "1033"},
		{"English", "1033"},
		{"en-gb", "2057"},
		{"de-de", "1031"},
		{"German", "1031"},
		{"fr-fr", "1036"},
		{"fr-ca", "3084"},
		{"es-es", "3082"},
		{"ja-jp", "1041"},
		{"zh-cn", "2052"},
		{"ru-ru", "1049"},
		{"pl-pl", "1045"},
		{"unknown", "1033"}, // Default to English
	}

	for _, tt := range tests {
		t.Run(tt.language, func(t *testing.T) {
			vars["LANGUAGE"] = tt.language
			got := r.getLCID()
			if got != tt.wantLCID {
				t.Errorf("getLCID(%q) = %q, want %q", tt.language, got, tt.wantLCID)
			}
		})
	}
}

func TestGetCodepage(t *testing.T) {
	vars := variables.New()
	r := NewRenderer(vars, "", "", nil)

	tests := []struct {
		language     string
		wantCodepage string
	}{
		{"en-us", "1252"},      // Western European
		{"de-de", "1252"},      // Western European
		{"pl-pl", "1250"},      // Central European
		{"ru-ru", "1251"},      // Cyrillic
		{"ja-jp", "932"},       // Japanese Shift-JIS
		{"zh-cn", "936"},       // Simplified Chinese GBK
		{"ko-kr", "949"},       // Korean
		{"unknown", "1252"},    // Default
	}

	for _, tt := range tests {
		t.Run(tt.language, func(t *testing.T) {
			vars["LANGUAGE"] = tt.language
			got := r.getCodepage()
			if got != tt.wantCodepage {
				t.Errorf("getCodepage(%q) = %q, want %q", tt.language, got, tt.wantCodepage)
			}
		})
	}
}

func TestGetTemplatePath(t *testing.T) {
	// Create temp directory to avoid platform-specific path issues
	tmpDir, err := os.MkdirTemp("", "template-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	vars := variables.New()
	r := NewRenderer(vars, tmpDir, "", nil)

	tests := []struct {
		platform   string
		silent     bool
		wantSuffix string
	}{
		{"x64", false, "x64/template.wxs"},
		{"x64", true, "x64/template-silent.wxs"},
		{"x86", false, "x86/template.wxs"},
		{"X64", false, "x64/template.wxs"}, // Case insensitive
	}

	for _, tt := range tests {
		got := r.getTemplatePath(tt.platform, tt.silent)
		// Normalize path separators for comparison
		got = filepath.ToSlash(got)
		if !strings.HasSuffix(got, tt.wantSuffix) {
			t.Errorf("getTemplatePath(%q, %v) = %q, want suffix %q", tt.platform, tt.silent, got, tt.wantSuffix)
		}
	}
}

func TestBuildContext(t *testing.T) {
	vars := variables.New()
	vars["PRODUCT_NAME"] = "Test Product"
	vars["PRODUCT_VERSION"] = "1.0.0"
	vars["LANGUAGE"] = "en-us"

	data := &generator.GeneratedOutput{
		DirectoryXML: "<Directory Id='TEST'/>",
		FeatureXML:   "<Feature Id='FEATURE_00000'/>",
	}

	r := NewRenderer(vars, "/templates", "", data)
	ctx := r.buildContext()

	// Check variables are copied
	if ctx["PRODUCT_NAME"] != "Test Product" {
		t.Error("expected PRODUCT_NAME to be copied to context")
	}

	// Check LCID
	if ctx["LCID"] != "1033" {
		t.Errorf("LCID = %v, want 1033", ctx["LCID"])
	}

	// Check generated content
	if ctx["FEATURES"] != "<Feature Id='FEATURE_00000'/>" {
		t.Errorf("FEATURES = %v, want feature XML", ctx["FEATURES"])
	}

	if ctx["INSTALLDIR_FILES"] != "<Directory Id='TEST'/>" {
		t.Errorf("INSTALLDIR_FILES = %v, want directory XML", ctx["INSTALLDIR_FILES"])
	}
}

func TestLogoDefaultsNoPrefix(t *testing.T) {
	// Create temp directory to avoid platform-specific path issues
	tmpDir, err := os.MkdirTemp("", "logo-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	vars := variables.New()
	// No LOGO_BANNER, LOGO_DIALOG, LOGO_BOOTSTRAP set
	// No LOGO_PREFIX set - logos should be empty (WiX uses its defaults)

	data := &generator.GeneratedOutput{}
	r := NewRenderer(vars, tmpDir, "", data)
	ctx := r.buildContext()

	// Without LOGO_PREFIX, no logo defaults should be set
	if ctx["LOGO_BANNER"] != nil {
		t.Errorf("LOGO_BANNER = %v, want nil (no default)", ctx["LOGO_BANNER"])
	}
	if ctx["LOGO_DIALOG"] != nil {
		t.Errorf("LOGO_DIALOG = %v, want nil (no default)", ctx["LOGO_DIALOG"])
	}
	if ctx["LOGO_BOOTSTRAP"] != nil {
		t.Errorf("LOGO_BOOTSTRAP = %v, want nil (no default)", ctx["LOGO_BOOTSTRAP"])
	}
}

func TestLogoDefaultsWithPrefix(t *testing.T) {
	// Create temp directory and logo files
	tmpDir, err := os.MkdirTemp("", "logo-prefix-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create the logo files so they can be found
	logoFiles := []string{
		"CUSTOM_WixUiBanner.bmp",
		"CUSTOM_WixUiDialog.bmp",
		"CUSTOM_LogoBootstrap.bmp",
	}
	for _, f := range logoFiles {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte("dummy"), 0644); err != nil {
			t.Fatalf("failed to create logo file %s: %v", f, err)
		}
	}

	vars := variables.New()
	vars["LOGO_PREFIX"] = "CUSTOM"

	data := &generator.GeneratedOutput{}
	r := NewRenderer(vars, tmpDir, "", data)
	ctx := r.buildContext()

	// Should use custom prefix - check suffix since path is absolute
	bannerStr, _ := ctx["LOGO_BANNER"].(string)
	if !strings.HasSuffix(filepath.ToSlash(bannerStr), "CUSTOM_WixUiBanner.bmp") {
		t.Errorf("LOGO_BANNER = %v, want suffix CUSTOM_WixUiBanner.bmp", bannerStr)
	}
}

func TestLogoDefaultsNotOverridden(t *testing.T) {
	vars := variables.New()
	vars["LOGO_BANNER"] = "/explicit/path/banner.bmp"
	vars["LOGO_DIALOG"] = "/explicit/path/dialog.bmp"

	data := &generator.GeneratedOutput{}
	r := NewRenderer(vars, "/templates", "", data)
	ctx := r.buildContext()

	// Explicit values should be preserved
	if ctx["LOGO_BANNER"] != "/explicit/path/banner.bmp" {
		t.Errorf("LOGO_BANNER = %v, should preserve explicit value", ctx["LOGO_BANNER"])
	}
	if ctx["LOGO_DIALOG"] != "/explicit/path/dialog.bmp" {
		t.Errorf("LOGO_DIALOG = %v, should preserve explicit value", ctx["LOGO_DIALOG"])
	}
}

func TestCustomTemplateOverride(t *testing.T) {
	// Create a temp directory with a custom template
	tmpDir, err := os.MkdirTemp("", "msis-template-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Write custom template
	customTemplate := `CUSTOM:{{PRODUCT_NAME}}`
	customPath := filepath.Join(tmpDir, "custom.wxs")
	if err := os.WriteFile(customPath, []byte(customTemplate), 0644); err != nil {
		t.Fatalf("failed to write custom template: %v", err)
	}

	vars := variables.New()
	vars["PRODUCT_NAME"] = "MyApp"

	data := &generator.GeneratedOutput{}
	r := NewRenderer(vars, tmpDir, "", data)
	r.SetCustomTemplate(customPath)

	result, err := r.Render()
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if result != "CUSTOM:MyApp" {
		t.Errorf("expected 'CUSTOM:MyApp', got %q", result)
	}
}

func TestRenderWithMinimalTemplate(t *testing.T) {
	// Create a minimal template in a temp directory
	tmpDir, err := os.MkdirTemp("", "msis-template-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create x64 directory
	x64Dir := filepath.Join(tmpDir, "x64")
	if err := os.MkdirAll(x64Dir, 0755); err != nil {
		t.Fatalf("failed to create x64 dir: %v", err)
	}

	// Write minimal template
	minimalTemplate := `<?xml version="1.0" encoding="utf-8"?>
<Wix xmlns="http://wixtoolset.org/schemas/v4/wxs">
    <Package Name="{{PRODUCT_NAME}}" Version="{{PRODUCT_VERSION}}">
        {{{FEATURES}}}
        <StandardDirectory Id="ProgramFiles64Folder">
            {{{INSTALLDIR_FILES}}}
        </StandardDirectory>
    </Package>
</Wix>`

	templatePath := filepath.Join(x64Dir, "template.wxs")
	if err := os.WriteFile(templatePath, []byte(minimalTemplate), 0644); err != nil {
		t.Fatalf("failed to write template: %v", err)
	}

	// Create renderer
	vars := variables.New()
	vars["PRODUCT_NAME"] = "Test App"
	vars["PRODUCT_VERSION"] = "2.0.0"
	vars["PLATFORM"] = "x64"

	data := &generator.GeneratedOutput{
		DirectoryXML: "<Directory Id='INSTALLDIR' Name='TestApp'/>",
		FeatureXML:   "<Feature Id='FEATURE_00000' Title='Main'/>",
	}

	r := NewRenderer(vars, tmpDir, "", data)

	result, err := r.Render()
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	// Verify output
	if !strings.Contains(result, "Name=\"Test App\"") {
		t.Error("expected output to contain product name")
	}
	if !strings.Contains(result, "Version=\"2.0.0\"") {
		t.Error("expected output to contain version")
	}
	if !strings.Contains(result, "FEATURE_00000") {
		t.Error("expected output to contain feature")
	}
	if !strings.Contains(result, "INSTALLDIR") {
		t.Error("expected output to contain install directory")
	}
}
