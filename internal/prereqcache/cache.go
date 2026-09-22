// Package prereqcache handles downloading and caching of prerequisite installers.
// Prerequisites are cached in %LOCALAPPDATA%\msis\prerequisites\ to avoid
// repeated downloads across multiple projects.
//
// Nothing leaves this package unverified. Every download msis knows is pinned to a
// version-specific URL AND the SHA-256 of the bytes that URL served when the pin was
// recorded; a download is checked against that digest before it lands under its cache
// name, and a cached file is checked again every time it is reused, because the cache
// directory is writable by the user and by anything running as them (#30, decisions D5).
// A file the script supplies itself (<requires source=.../>) is the one thing not
// verified: msis has no digest for it, and the docs say so.
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
	URL      string // Pinned, version-specific download URL
	FileName string // Local file name
	SHA256   string // Expected SHA-256 of the file, lowercase hex. Required: nothing unpinned is downloaded.

	// FileVersion is the installer's own version resource, recorded when the pin was
	// taken. Informational — it is what a mismatch message can name so the reader knows
	// which release msis expected.
	FileVersion string
}

// DownloadURLs contains the prerequisites msis can download, each pinned to a
// version-specific Microsoft URL and the SHA-256 of the file it served.
//
// Pinned, not "latest": the aka.ms and fwlink URLs these entries used to name are
// mutable by design — the same URL serves a new redistributable when one ships — so
// a digest recorded against one of them disagrees with it sooner or later. A pinned
// URL and its digest stay in agreement, every machine that builds the same msis
// release chains the same bytes, and a newer redistributable arrives with an msis
// release that re-pins (or through <requires source=...>). That trade is decisions D5.
//
// How each digest was obtained (2026-09-22): each file was downloaded over TLS from
// the URL below, hashed, and its Authenticode signature checked with
// Get-AuthenticodeSignature — Status Valid, signer CN=Microsoft Corporation, for all
// eight. For the Visual Studio CDN the URL path itself carries the file's SHA-256
// (the upper-case segment before the file name); every recorded digest equals it,
// and TestEveryDownloadIsPinned keeps that so. Microsoft publishes no separate digest
// list for these files, so the pin is that download, on that date, signed by that
// signer — not a third party's attestation.
//
// The .NET Framework entries are the OFFLINE installers. Before pinning, 4.8.1 and
// 4.7.2 pointed at fwlinks that resolve to the 1.4 MB WEB installers (NDP481-Web.exe,
// ndp472-kb4054531-web.exe) while naming the cached file after the offline one, so a
// bundle carried an installer that needs the network at install time.
var DownloadURLs = map[string]map[string]map[string]PrerequisiteURL{
	"vcredist": {
		"2022": {
			"x64": {
				Type: "vcredist", Version: "2022", Arch: "x64",
				URL:         "https://download.visualstudio.microsoft.com/download/pr/bd1c8d9d-ba95-4eee-bc6e-df1fcc876373/CC0FF0EB1DC3F5188AE6300FAEF32BF5BEEBA4BDD6E8E445A9184072096B713B/VC_redist.x64.exe",
				FileName:    "vc_redist.x64.exe",
				SHA256:      "cc0ff0eb1dc3f5188ae6300faef32bf5beeba4bdd6e8e445a9184072096b713b",
				FileVersion: "14.44.35211.0",
			},
			"x86": {
				Type: "vcredist", Version: "2022", Arch: "x86",
				URL:         "https://download.visualstudio.microsoft.com/download/pr/bd1c8d9d-ba95-4eee-bc6e-df1fcc876373/0C09F2611660441084CE0DF425C51C11E147E6447963C3690F97E0B25C55ED64/VC_redist.x86.exe",
				FileName:    "vc_redist.x86.exe",
				SHA256:      "0c09f2611660441084ce0df425c51c11e147e6447963c3690f97e0b25c55ed64",
				FileVersion: "14.44.35211.0",
			},
			"arm64": {
				Type: "vcredist", Version: "2022", Arch: "arm64",
				URL:         "https://download.visualstudio.microsoft.com/download/pr/d7450eb5-03e1-436d-9e7e-deb5fe4759b3/5139E1440C3A20B92153A4DB561C069A0175AAF76C276C3E5B6F56099EDCF4B0/VC_redist.arm64.exe",
				FileName:    "vc_redist.arm64.exe",
				SHA256:      "5139e1440c3a20b92153a4db561c069a0175aaf76c276c3e5b6f56099edcf4b0",
				FileVersion: "14.44.35211.0",
			},
		},
		"2019": {
			"x64": {
				Type: "vcredist", Version: "2019", Arch: "x64",
				URL:         "https://download.visualstudio.microsoft.com/download/pr/b6c6fac1-c78c-4c3c-9461-d8e2c68ac8b4/6AFAE68A783F11292149175844AED0E2CE3F247BC0250F6CB18C931295B3F399/VC_redist.x64.exe",
				FileName:    "vc_redist.x64.exe",
				SHA256:      "6afae68a783f11292149175844aed0e2ce3f247bc0250f6cb18c931295b3f399",
				FileVersion: "14.29.30157.0",
			},
			"x86": {
				Type: "vcredist", Version: "2019", Arch: "x86",
				URL:         "https://download.visualstudio.microsoft.com/download/pr/8a78e61f-9368-484b-b0c1-5628ff392121/38C9437E6E9EF1DB2671B3F0C879FEBEC08521BD2C23231199F626B69AE1C65E/VC_redist.x86.exe",
				FileName:    "vc_redist.x86.exe",
				SHA256:      "38c9437e6e9ef1db2671b3f0c879febec08521bd2c23231199f626b69ae1c65e",
				FileVersion: "14.29.30157.0",
			},
		},
	},
	"netfx": {
		"4.8.1": {
			"": { // arch-neutral; offline installer (fwlink 2203305)
				Type: "netfx", Version: "4.8.1", Arch: "",
				URL:         "https://download.microsoft.com/download/4/b/2/cd00d4ed-ebdd-49ee-8a33-eabc3d1030e3/NDP481-x86-x64-AllOS-ENU.exe",
				FileName:    "ndp481-x86-x64-allos-enu.exe",
				SHA256:      "c0ca2e0c9cd18a24a0a77369a13fae2c2c4e8bc83355dd24e5ddc00f9d791fe3",
				FileVersion: "4.8.09195.10",
			},
		},
		"4.8": {
			"": { // offline installer (fwlink 2088631)
				Type: "netfx", Version: "4.8", Arch: "",
				URL:         "https://download.microsoft.com/download/f/3/a/f3a6af84-da23-40a5-8d1c-49cc10c8e76f/NDP48-x86-x64-AllOS-ENU.exe",
				FileName:    "ndp48-x86-x64-allos-enu.exe",
				SHA256:      "0a3a390c47e639d0f7fc65b21195fee6b7f65b066f80f70c60fab191d14b7e40",
				FileVersion: "4.8.04115.00",
			},
		},
		"4.7.2": {
			"": { // offline installer (fwlink 863265)
				Type: "netfx", Version: "4.7.2", Arch: "",
				URL:         "https://download.microsoft.com/download/f/3/a/f3a6af84-da23-40a5-8d1c-49cc10c8e76f/NDP472-KB4054530-x86-x64-AllOS-ENU.exe",
				FileName:    "ndp472-kb4054530-x86-x64-allos-enu.exe",
				SHA256:      "84ea476eb3a03ab878c14b160495f071dd29f9e5d031e713623ee8635638355f",
				FileVersion: "4.7.03081.00",
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

// EnsurePrerequisite ensures a prerequisite is available in the cache and returns its path.
//
// A customSource is returned as it is, after an existence check and nothing else: msis has
// no digest for a file the script supplied, so it cannot verify one, and the documentation
// says that the author is responsible for it.
//
// Everything else is verified against the pinned digest, in both directions:
//
//   - a cached file is hashed on EVERY reuse, not only after download. The cache directory
//     is writable by the user and by anything running as them, so a file that verified
//     yesterday proves nothing about today's bytes. A cached file that no longer matches is
//     deleted and downloaded again, once — that is also how a cache entry from before the
//     pins (the 4.8.1 and 4.7.2 web installers) heals itself.
//   - a download is hashed while it still has its temporary name, and only a matching file
//     is renamed into place. A corrupted or substituted download therefore never exists
//     under the name the next build would trust.
//
// A second mismatch — the fresh download does not match either — refuses the build with
// both digests, because at that point the file Microsoft serves is not the file this msis
// release pinned, and chaining it into a customer's installer unverified is the failure
// mode #30 exists to prevent.
func (c *Cache) EnsurePrerequisite(prereqType, version, arch, customSource string, progress func(msg string)) (string, error) {
	report := func(format string, args ...any) {
		if progress != nil {
			progress(fmt.Sprintf(format, args...))
		}
	}

	// If custom source is provided, use it directly (no caching, no verification)
	if customSource != "" {
		if _, err := os.Stat(customSource); err != nil {
			return "", fmt.Errorf("custom source not found: %s", customSource)
		}
		return customSource, nil
	}

	// Look up the pinned download
	urlInfo := LookupDownloadURL(prereqType, version, arch)
	if urlInfo == nil {
		return "", fmt.Errorf("no download URL for %s %s (%s); %s", prereqType, version, arch, getAvailableVersionsHint(prereqType))
	}
	if urlInfo.SHA256 == "" {
		// Unreachable for the built-in table (TestEveryDownloadIsPinned), but DownloadURLs is
		// a variable: an entry added without a digest must not become an unverified download.
		return "", fmt.Errorf("no digest is pinned for %s %s (%s); msis downloads nothing it cannot verify — supply the installer with <requires ... source=\"...\"/>",
			prereqType, version, arch)
	}

	subDir := filepath.Join(c.CacheDir, prereqType, version)
	destPath := filepath.Join(subDir, urlInfo.FileName)

	// Reuse the cached file only if it still matches the pin
	if _, err := os.Stat(destPath); err == nil {
		if err := verifyHash(destPath, urlInfo.SHA256); err == nil {
			report("Using cached: %s (SHA-256 verified)", urlInfo.FileName)
			return destPath, nil
		} else {
			report("Cached %s does not match the digest msis pins for it (%v); discarding it and downloading again", urlInfo.FileName, err)
			if err := os.Remove(destPath); err != nil {
				return "", fmt.Errorf("removing mismatching cached %s: %w", urlInfo.FileName, err)
			}
		}
	}

	if err := os.MkdirAll(subDir, 0755); err != nil {
		return "", fmt.Errorf("creating cache subdirectory: %w", err)
	}

	// Download to a temporary file, verify, and only then let it become the cached file.
	//
	// The temporary file is created EXCLUSIVELY, with a name of its own, rather than at a
	// fixed "<name>.download": two builds fetching the same prerequisite at once (a CI box
	// building x64 and x86 side by side, say) would otherwise share the path, and the one
	// that had already verified its bytes could rename the OTHER's unchecked bytes into the
	// cache. Each build now verifies and publishes only what it downloaded itself, and
	// cleans up only its own file.
	report("Downloading: %s", urlInfo.FileName)
	tmp, err := os.CreateTemp(subDir, urlInfo.FileName+".*.download")
	if err != nil {
		return "", fmt.Errorf("creating a temporary file for %s: %w", urlInfo.FileName, err)
	}
	tempPath := tmp.Name()
	if err := downloadInto(urlInfo.URL, tmp, progress); err != nil {
		os.Remove(tempPath)
		return "", fmt.Errorf("downloading %s: %w", urlInfo.FileName, err)
	}
	if err := verifyHash(tempPath, urlInfo.SHA256); err != nil {
		os.Remove(tempPath)
		return "", fmt.Errorf("downloaded %s does not match the digest msis pins for %s %s (%s, file version %s): %w. "+
			"Either the download was corrupted in transit, or Microsoft has republished the file since this msis release pinned it. "+
			"Retry the build; if it persists, supply the installer yourself with <requires ... source=\"...\"/> or update msis",
			urlInfo.FileName, prereqType, version, arch, urlInfo.FileVersion, err)
	}
	if err := os.Rename(tempPath, destPath); err != nil {
		os.Remove(tempPath)
		return "", fmt.Errorf("placing %s in the cache: %w", urlInfo.FileName, err)
	}

	report("Cached: %s (SHA-256 verified)", urlInfo.FileName)
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

// downloadInto streams URL into an already-open file, which it closes, with progress
// reporting. The caller created the file — exclusively, under a temporary name — and decides,
// after verifying the bytes, whether they become the cached file.
func downloadInto(url string, out *os.File, progress func(msg string)) error {
	defer out.Close()

	client := &http.Client{
		Timeout: DownloadTimeout,
	}

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
	if err := out.Close(); err != nil {
		return err
	}

	if progress != nil {
		progress(fmt.Sprintf("Downloaded: %.1f MB", float64(written)/(1024*1024)))
	}

	return nil
}

// verifyHash checks a file against its expected SHA-256, comparing case-insensitively so
// a pin copied from a URL path (upper case) and one from sha256sum (lower case) both work.
func verifyHash(filePath, expectedHash string) error {
	actualHash, err := fileSHA256(filePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actualHash, expectedHash) {
		return fmt.Errorf("expected %s, got %s", expectedHash, actualHash)
	}
	return nil
}

// fileSHA256 returns the lowercase hex SHA-256 of a file.
func fileSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ClearCache removes all cached prerequisites.
func (c *Cache) ClearCache() error {
	return os.RemoveAll(c.CacheDir)
}

// CachedEntry is one file in the cache and what a check of it found. Detail is empty for a
// verified file; otherwise it says why the file would not be reused as it is.
type CachedEntry struct {
	RelPath  string // type/version/file, relative to the cache directory
	Verified bool
	Detail   string
}

// VerifyCached checks every cached file against the digest msis pins for it. It is the
// /STATUS view of the cache, and it reports the same thing a build would find: a file that
// verifies here is one a build reuses, and one that does not is one a build discards and
// downloads again. A file no pin knows — a stray, or an entry from a version of msis with a
// different table — is reported as such rather than silently listed as if it were trusted.
// Nothing is deleted here; that is the build's decision, at the moment it needs the file.
func (c *Cache) VerifyCached() ([]CachedEntry, error) {
	relPaths, err := c.ListCached()
	if err != nil {
		return nil, err
	}
	entries := make([]CachedEntry, 0, len(relPaths))
	for _, rel := range relPaths {
		entry := CachedEntry{RelPath: rel}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		var pin *PrerequisiteURL
		if len(parts) == 3 {
			pin = lookupByFileName(parts[0], parts[1], parts[2])
		}
		switch {
		case pin == nil:
			entry.Detail = "no pinned download has this name; a build would not use it"
		default:
			if err := verifyHash(filepath.Join(c.CacheDir, rel), pin.SHA256); err != nil {
				entry.Detail = "does not match its pinned digest (" + err.Error() + "); a build would discard and re-download it"
			} else {
				entry.Verified = true
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// lookupByFileName finds the pinned download whose cached name is fileName, for a type and
// version. The cache path carries no architecture, so this is the reverse of
// LookupDownloadURL: which entry does this file on disk belong to.
func lookupByFileName(prereqType, version, fileName string) *PrerequisiteURL {
	arches, ok := DownloadURLs[prereqType][version]
	if !ok {
		return nil
	}
	for _, info := range arches {
		if strings.EqualFold(info.FileName, fileName) {
			return &info
		}
	}
	return nil
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
