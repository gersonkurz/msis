// Package prereqcache handles downloading and caching of prerequisite installers.
// Prerequisites are cached in %LOCALAPPDATA%\msis\prerequisites\ to avoid
// repeated downloads across multiple projects.
package prereqcache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PrerequisiteURL defines download information for a prerequisite.
type PrerequisiteURL struct {
	Type     string // vcredist, netfx
	Version  string // 2022, 4.8, etc.
	Arch     string // x64, x86, arm64, or "" for arch-neutral
	URL      string // Download URL
	FileName string // Local file name
	SHA256   string // Expected SHA256 hash (optional, for verification)
}

// DownloadURLs contains official Microsoft download URLs for prerequisites.
// Note: These URLs may change over time; Microsoft periodically updates them.
var DownloadURLs = map[string]map[string]map[string]PrerequisiteURL{
	"vcredist": {
		"2022": {
			"x64": {
				Type: "vcredist", Version: "2022", Arch: "x64",
				URL:      "https://aka.ms/vs/17/release/vc_redist.x64.exe",
				FileName: "vc_redist.x64.exe",
			},
			"x86": {
				Type: "vcredist", Version: "2022", Arch: "x86",
				URL:      "https://aka.ms/vs/17/release/vc_redist.x86.exe",
				FileName: "vc_redist.x86.exe",
			},
			"arm64": {
				Type: "vcredist", Version: "2022", Arch: "arm64",
				URL:      "https://aka.ms/vs/17/release/vc_redist.arm64.exe",
				FileName: "vc_redist.arm64.exe",
			},
		},
		"2019": {
			"x64": {
				Type: "vcredist", Version: "2019", Arch: "x64",
				URL:      "https://aka.ms/vs/16/release/vc_redist.x64.exe",
				FileName: "vc_redist.x64.exe",
			},
			"x86": {
				Type: "vcredist", Version: "2019", Arch: "x86",
				URL:      "https://aka.ms/vs/16/release/vc_redist.x86.exe",
				FileName: "vc_redist.x86.exe",
			},
		},
	},
	"netfx": {
		"4.8.1": {
			"": { // arch-neutral
				Type: "netfx", Version: "4.8.1", Arch: "",
				URL:      "https://go.microsoft.com/fwlink/?linkid=2203304",
				FileName: "ndp481-x86-x64-allos-enu.exe",
			},
		},
		"4.8": {
			"": {
				Type: "netfx", Version: "4.8", Arch: "",
				URL:      "https://go.microsoft.com/fwlink/?linkid=2088631",
				FileName: "ndp48-x86-x64-allos-enu.exe",
			},
		},
		"4.7.2": {
			"": {
				Type: "netfx", Version: "4.7.2", Arch: "",
				URL:      "https://go.microsoft.com/fwlink/?LinkId=863262",
				FileName: "ndp472-kb4054530-x86-x64-allos-enu.exe",
			},
		},
	},
}

// Cache manages the local prerequisite cache.
type Cache struct {
	CacheDir string
}

// NewCache creates a cache manager using the default cache directory.
// Creates the directory if it doesn't exist.
func NewCache() (*Cache, error) {
	cacheDir := GetDefaultCacheDir()
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("creating cache directory: %w", err)
	}
	return &Cache{CacheDir: cacheDir}, nil
}

// NewCacheReadOnly creates a cache manager for reading only.
// Does not create directories; returns nil if the cache directory doesn't exist.
func NewCacheReadOnly() *Cache {
	cacheDir := GetDefaultCacheDir()
	if _, err := os.Stat(cacheDir); err != nil {
		return nil
	}
	return &Cache{CacheDir: cacheDir}
}

// GetDefaultCacheDir returns the default cache directory path.
func GetDefaultCacheDir() string {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		// Fallback for non-Windows systems
		home, _ := os.UserHomeDir()
		localAppData = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(localAppData, "msis", "prerequisites")
}

// GetCachedPath returns the cached file path for a prerequisite.
// Returns empty string if not cached.
func (c *Cache) GetCachedPath(prereqType, version, arch string) string {
	// Build subdirectory path: type/version/
	subDir := filepath.Join(c.CacheDir, prereqType, version)

	// Look up the expected filename
	fileName := getExpectedFileName(prereqType, version, arch)
	if fileName == "" {
		return ""
	}

	filePath := filepath.Join(subDir, fileName)
	if _, err := os.Stat(filePath); err == nil {
		return filePath
	}
	return ""
}

// EnsurePrerequisite ensures a prerequisite is available in the cache.
// If customSource is provided, it's used instead of downloading.
// Returns the path to the cached file.
func (c *Cache) EnsurePrerequisite(prereqType, version, arch, customSource string, progress func(msg string)) (string, error) {
	// If custom source is provided, use it directly (no caching)
	if customSource != "" {
		if _, err := os.Stat(customSource); err != nil {
			return "", fmt.Errorf("custom source not found: %s", customSource)
		}
		return customSource, nil
	}

	// Check if already cached
	cachedPath := c.GetCachedPath(prereqType, version, arch)
	if cachedPath != "" {
		if progress != nil {
			progress(fmt.Sprintf("Using cached: %s", filepath.Base(cachedPath)))
		}
		return cachedPath, nil
	}

	// Look up download URL
	urlInfo := LookupDownloadURL(prereqType, version, arch)
	if urlInfo == nil {
		return "", fmt.Errorf("no download URL for %s %s (%s); %s", prereqType, version, arch, getAvailableVersionsHint(prereqType))
	}

	// Create cache subdirectory
	subDir := filepath.Join(c.CacheDir, prereqType, version)
	if err := os.MkdirAll(subDir, 0755); err != nil {
		return "", fmt.Errorf("creating cache subdirectory: %w", err)
	}

	// Download file
	destPath := filepath.Join(subDir, urlInfo.FileName)
	if progress != nil {
		progress(fmt.Sprintf("Downloading: %s", urlInfo.FileName))
	}

	if err := downloadFile(urlInfo.URL, destPath, progress); err != nil {
		return "", fmt.Errorf("downloading %s: %w", urlInfo.FileName, err)
	}

	// Verify hash if provided
	if urlInfo.SHA256 != "" {
		if err := verifyHash(destPath, urlInfo.SHA256); err != nil {
			os.Remove(destPath) // Remove corrupted file
			return "", fmt.Errorf("hash verification failed: %w", err)
		}
	} else if progress != nil {
		// Warn that hash verification is not available
		progress(fmt.Sprintf("Warning: No SHA256 hash available for %s (integrity not verified)", urlInfo.FileName))
	}

	if progress != nil {
		progress(fmt.Sprintf("Cached: %s", urlInfo.FileName))
	}

	return destPath, nil
}

// LookupDownloadURL finds the download URL for a prerequisite.
func LookupDownloadURL(prereqType, version, arch string) *PrerequisiteURL {
	if versions, ok := DownloadURLs[prereqType]; ok {
		if arches, ok := versions[version]; ok {
			// Try exact arch match first
			if info, ok := arches[arch]; ok {
				return &info
			}
			// Try arch-neutral (empty string)
			if info, ok := arches[""]; ok {
				return &info
			}
		}
	}
	return nil
}

// getAvailableVersionsHint returns a hint about available versions for error messages.
func getAvailableVersionsHint(prereqType string) string {
	if versions, ok := DownloadURLs[prereqType]; ok {
		var available []string
		for v := range versions {
			available = append(available, v)
		}
		return fmt.Sprintf("available %s versions with auto-download: %s", prereqType, strings.Join(available, ", "))
	}
	var types []string
	for t := range DownloadURLs {
		types = append(types, t)
	}
	return fmt.Sprintf("unknown type '%s'; available types: %s", prereqType, strings.Join(types, ", "))
}

// getExpectedFileName returns the expected file name for a cached prerequisite.
func getExpectedFileName(prereqType, version, arch string) string {
	urlInfo := LookupDownloadURL(prereqType, version, arch)
	if urlInfo != nil {
		return urlInfo.FileName
	}
	return ""
}

// DownloadTimeout is the timeout for HTTP requests when downloading prerequisites.
// Defaults to 5 minutes to accommodate large files on slower connections.
var DownloadTimeout = 5 * time.Minute

// downloadFile downloads a file from URL to destPath with progress reporting.
func downloadFile(url, destPath string, progress func(msg string)) error {
	// Create temporary file
	tempPath := destPath + ".download"
	out, err := os.Create(tempPath)
	if err != nil {
		return err
	}
	defer func() {
		out.Close()
		os.Remove(tempPath) // Clean up temp file on error
	}()

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: DownloadTimeout,
	}

	// Download
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Copy with progress (simplified - just reports completion)
	written, err := io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	out.Close()

	// Move temp file to final destination
	if err := os.Rename(tempPath, destPath); err != nil {
		return err
	}

	if progress != nil {
		progress(fmt.Sprintf("Downloaded: %.1f MB", float64(written)/(1024*1024)))
	}

	return nil
}

// verifyHash verifies the SHA256 hash of a file.
func verifyHash(filePath, expectedHash string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	actualHash := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(actualHash, expectedHash) {
		return fmt.Errorf("expected %s, got %s", expectedHash, actualHash)
	}

	return nil
}

// ClearCache removes all cached prerequisites.
func (c *Cache) ClearCache() error {
	return os.RemoveAll(c.CacheDir)
}

// ListCached returns a list of cached prerequisites.
func (c *Cache) ListCached() ([]string, error) {
	var cached []string

	err := filepath.Walk(c.CacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if !info.IsDir() && strings.HasSuffix(path, ".exe") {
			relPath, _ := filepath.Rel(c.CacheDir, path)
			cached = append(cached, relPath)
		}
		return nil
	})

	return cached, err
}
