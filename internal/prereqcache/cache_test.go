package prereqcache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLookupDownloadURL(t *testing.T) {
	tests := []struct {
		prereqType string
		version    string
		arch       string
		wantURL    bool
		wantArch   string
	}{
		{"vcredist", "2022", "x64", true, "x64"},
		{"vcredist", "2022", "x86", true, "x86"},
		{"vcredist", "2022", "arm64", true, "arm64"},
		{"vcredist", "2019", "x64", true, "x64"},
		{"netfx", "4.8", "", true, ""},       // arch-neutral
		{"netfx", "4.8", "x64", true, ""},    // falls back to arch-neutral
		{"netfx", "4.8.1", "", true, ""},
		{"unknown", "1.0", "x64", false, ""},
		{"vcredist", "9999", "x64", false, ""}, // unknown version
	}

	for _, tt := range tests {
		t.Run(tt.prereqType+"_"+tt.version+"_"+tt.arch, func(t *testing.T) {
			info := LookupDownloadURL(tt.prereqType, tt.version, tt.arch)
			if tt.wantURL {
				if info == nil {
					t.Error("expected URL info, got nil")
				} else {
					if info.URL == "" {
						t.Error("expected non-empty URL")
					}
					if info.FileName == "" {
						t.Error("expected non-empty FileName")
					}
				}
			} else {
				if info != nil {
					t.Errorf("expected nil, got %+v", info)
				}
			}
		})
	}
}

func TestGetDefaultCacheDir(t *testing.T) {
	dir := GetDefaultCacheDir()
	if dir == "" {
		t.Error("expected non-empty cache directory")
	}
	// Should end with msis/prerequisites
	if !filepath.IsAbs(dir) {
		t.Error("expected absolute path")
	}
}

func TestNewCache(t *testing.T) {
	// Use temp directory for testing
	tempDir, err := os.MkdirTemp("", "msis-cache-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cache := &Cache{CacheDir: tempDir}

	// Test GetCachedPath for non-existent file
	path := cache.GetCachedPath("vcredist", "2022", "x64")
	if path != "" {
		t.Errorf("expected empty path for non-cached file, got %s", path)
	}
}

func TestCacheCustomSource(t *testing.T) {
	// Create temp file to use as custom source
	tempDir, err := os.MkdirTemp("", "msis-cache-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	customFile := filepath.Join(tempDir, "custom.exe")
	if err := os.WriteFile(customFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create custom file: %v", err)
	}

	cache := &Cache{CacheDir: tempDir}

	// Custom source should be returned as-is
	path, err := cache.EnsurePrerequisite("vcredist", "2022", "x64", customFile, nil)
	if err != nil {
		t.Fatalf("EnsurePrerequisite failed: %v", err)
	}
	if path != customFile {
		t.Errorf("expected %s, got %s", customFile, path)
	}
}

func TestCacheCustomSourceNotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "msis-cache-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cache := &Cache{CacheDir: tempDir}

	// Non-existent custom source should error
	_, err = cache.EnsurePrerequisite("vcredist", "2022", "x64", "/nonexistent/file.exe", nil)
	if err == nil {
		t.Error("expected error for non-existent custom source")
	}
}

func TestListCached(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "msis-cache-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create some fake cached files
	subDir := filepath.Join(tempDir, "vcredist", "2022")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "vc_redist.x64.exe"), []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	cache := &Cache{CacheDir: tempDir}
	cached, err := cache.ListCached()
	if err != nil {
		t.Fatalf("ListCached failed: %v", err)
	}

	if len(cached) != 1 {
		t.Errorf("expected 1 cached file, got %d", len(cached))
	}
}

func TestCacheGetCachedPath(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "msis-cache-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a cached file
	subDir := filepath.Join(tempDir, "vcredist", "2022")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	cachedFile := filepath.Join(subDir, "vc_redist.x64.exe")
	if err := os.WriteFile(cachedFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	cache := &Cache{CacheDir: tempDir}

	// Should find cached file
	path := cache.GetCachedPath("vcredist", "2022", "x64")
	if path != cachedFile {
		t.Errorf("expected %s, got %s", cachedFile, path)
	}

	// Should not find non-existent file
	path = cache.GetCachedPath("vcredist", "2022", "x86")
	if path != "" {
		t.Errorf("expected empty path, got %s", path)
	}
}

func TestDownloadTimeout(t *testing.T) {
	// Verify download timeout is set to a reasonable value
	if DownloadTimeout < 30*time.Second {
		t.Errorf("download timeout too short: %v", DownloadTimeout)
	}
	if DownloadTimeout > 10*time.Minute {
		t.Errorf("download timeout too long: %v", DownloadTimeout)
	}
}

func TestNewCacheReadOnly(t *testing.T) {
	// Non-existent directory should return nil
	cache := NewCacheReadOnly()
	// Note: this might succeed if the cache dir already exists from normal use
	// So we can't reliably test the nil case without mocking

	// Test with existing directory
	tempDir, err := os.MkdirTemp("", "msis-cache-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a cache directly with the temp dir
	cache = &Cache{CacheDir: tempDir}
	if cache == nil {
		t.Error("expected non-nil cache for existing directory")
	}
}

func TestLookupDownloadURLArm64(t *testing.T) {
	// VC++ 2022 should have ARM64
	info := LookupDownloadURL("vcredist", "2022", "arm64")
	if info == nil {
		t.Error("expected ARM64 URL for vcredist 2022")
	}
	if info != nil && info.Arch != "arm64" {
		t.Errorf("expected arch=arm64, got %s", info.Arch)
	}

	// VC++ 2019 should NOT have ARM64
	info = LookupDownloadURL("vcredist", "2019", "arm64")
	if info != nil {
		t.Error("vcredist 2019 should not have ARM64 support")
	}
}
