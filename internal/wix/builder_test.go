package wix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/variables"
)

func TestNewBuilder(t *testing.T) {
	vars := variables.New()
	vars["BUILD_TARGET"] = "output.msi"
	vars["PLATFORM"] = "x64"
	vars["LANGUAGE"] = "en-us"

	b := NewBuilder(vars, "test.wxs", "/templates", "", "", false)

	if b.WxsFile != "test.wxs" {
		t.Errorf("WxsFile = %q, want %q", b.WxsFile, "test.wxs")
	}
	if b.OutputFile != "output.msi" {
		t.Errorf("OutputFile = %q, want %q", b.OutputFile, "output.msi")
	}
	if b.Platform != "x64" {
		t.Errorf("Platform = %q, want %q", b.Platform, "x64")
	}
	if b.Language != "en-us" {
		t.Errorf("Language = %q, want %q", b.Language, "en-us")
	}
	if b.RetainWxs != false {
		t.Error("RetainWxs should be false")
	}
}

func TestNewBuilderWithRetainWxs(t *testing.T) {
	vars := variables.New()
	vars["BUILD_TARGET"] = "output.msi"

	b := NewBuilder(vars, "test.wxs", "/templates", "", "", true)

	if b.RetainWxs != true {
		t.Error("RetainWxs should be true")
	}
}

func TestGetLocalizationFile(t *testing.T) {
	// Create temp directory with mock localization files
	tmpDir, err := os.MkdirTemp("", "wix-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create wixlib directory
	wixlibDir := filepath.Join(tmpDir, "wixlib")
	if err := os.MkdirAll(wixlibDir, 0755); err != nil {
		t.Fatalf("failed to create wixlib dir: %v", err)
	}

	// Create mock localization file
	locFile := filepath.Join(wixlibDir, "en-us.wxl")
	if err := os.WriteFile(locFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write loc file: %v", err)
	}

	vars := variables.New()
	vars["BUILD_TARGET"] = "output.msi"
	vars["LANGUAGE"] = "en-us"

	b := NewBuilder(vars, "test.wxs", tmpDir, "", "", false)

	result := b.getLocalizationFile()
	if result != locFile {
		t.Errorf("getLocalizationFile() = %q, want %q", result, locFile)
	}
}

func TestGetLocalizationFileNotFound(t *testing.T) {
	vars := variables.New()
	vars["BUILD_TARGET"] = "output.msi"
	vars["LANGUAGE"] = "nonexistent"

	b := NewBuilder(vars, "test.wxs", "/nonexistent/path", "", "", false)

	result := b.getLocalizationFile()
	if result != "" {
		t.Errorf("getLocalizationFile() = %q, want empty string", result)
	}
}

func TestGetLocalizationFileNoLanguage(t *testing.T) {
	vars := variables.New()
	vars["BUILD_TARGET"] = "output.msi"

	b := NewBuilder(vars, "test.wxs", "/templates", "", "", false)

	result := b.getLocalizationFile()
	if result != "" {
		t.Errorf("getLocalizationFile() = %q, want empty string", result)
	}
}

func TestCleanupWithRetainWxs(t *testing.T) {
	// Create temp directory with mock files
	tmpDir, err := os.MkdirTemp("", "wix-cleanup-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	wxsFile := filepath.Join(tmpDir, "test.wxs")
	msiFile := filepath.Join(tmpDir, "test.msi")
	pdbFile := filepath.Join(tmpDir, "test.wixpdb")

	// Create mock files
	os.WriteFile(wxsFile, []byte("test"), 0644)
	os.WriteFile(pdbFile, []byte("test"), 0644)

	vars := variables.New()
	vars["BUILD_TARGET"] = msiFile

	b := NewBuilder(vars, wxsFile, tmpDir, "", "", true) // retain wxs
	b.cleanup()

	// WXS should still exist (retained)
	if _, err := os.Stat(wxsFile); os.IsNotExist(err) {
		t.Error("wxs file should be retained")
	}

	// PDB should be deleted
	if _, err := os.Stat(pdbFile); !os.IsNotExist(err) {
		t.Error("wixpdb file should be deleted")
	}
}

func TestCleanupWithoutRetainWxs(t *testing.T) {
	// Create temp directory with mock files
	tmpDir, err := os.MkdirTemp("", "wix-cleanup-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	wxsFile := filepath.Join(tmpDir, "test.wxs")
	msiFile := filepath.Join(tmpDir, "test.msi")
	pdbFile := filepath.Join(tmpDir, "test.wixpdb")

	// Create mock files
	os.WriteFile(wxsFile, []byte("test"), 0644)
	os.WriteFile(pdbFile, []byte("test"), 0644)

	vars := variables.New()
	vars["BUILD_TARGET"] = msiFile

	b := NewBuilder(vars, wxsFile, tmpDir, "", "", false) // don't retain wxs
	b.cleanup()

	// WXS should be deleted
	if _, err := os.Stat(wxsFile); !os.IsNotExist(err) {
		t.Error("wxs file should be deleted")
	}

	// PDB should be deleted
	if _, err := os.Stat(pdbFile); !os.IsNotExist(err) {
		t.Error("wixpdb file should be deleted")
	}
}

func TestIsWixAvailable(t *testing.T) {
	// This test just verifies the function doesn't panic
	// The result depends on whether WiX is installed
	result := IsWixAvailable()
	t.Logf("IsWixAvailable() = %v", result)
}

func TestPlatformLowercase(t *testing.T) {
	// Verify platform is normalized to lowercase for wix CLI
	vars := variables.New()
	vars["BUILD_TARGET"] = "output.msi"
	vars["PLATFORM"] = "X64" // uppercase

	b := NewBuilder(vars, "test.wxs", "/templates", "", "", false)

	// Platform should be stored as-is
	if b.Platform != "X64" {
		t.Errorf("Platform = %q, want %q", b.Platform, "X64")
	}

	// The runWixBuild will convert to lowercase when building args
	// We can't test that directly without mocking exec, but we can
	// verify the builder stores the value correctly
	if !strings.EqualFold(b.Platform, "x64") {
		t.Errorf("Platform should be case-insensitively equal to x64")
	}
}
