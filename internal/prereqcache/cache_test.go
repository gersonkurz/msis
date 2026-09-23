package prereqcache

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
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
		{"netfx", "4.8", "", true, ""},    // arch-neutral
		{"netfx", "4.8", "x64", true, ""}, // falls back to arch-neutral
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

// --- Integrity (#30) -------------------------------------------------------------------

// TestEveryDownloadIsPinned: the table is the contract. Every entry names a direct,
// version-specific Microsoft URL over TLS — not an aka.ms or fwlink alias, which serve a
// different file when a new release ships — and a full SHA-256, so that nothing msis
// downloads is unverified. For the Visual Studio CDN the URL path carries the file's own
// SHA-256 as the upper-case segment before the file name; the pinned digest must be that
// value, which ties msis's pin to Microsoft's statement of it rather than to a transcription.
func TestEveryDownloadIsPinned(t *testing.T) {
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	count := 0
	for typ, versions := range DownloadURLs {
		for version, arches := range versions {
			for arch, e := range arches {
				count++
				name := typ + " " + version + " " + arch
				if e.Type != typ || e.Version != version || e.Arch != arch {
					t.Errorf("%s: entry describes itself as %s %s %q", name, e.Type, e.Version, e.Arch)
				}
				if e.FileName == "" {
					t.Errorf("%s: no FileName", name)
				}
				if !hex64.MatchString(e.SHA256) {
					t.Errorf("%s: SHA256 %q is not 64 lowercase hex digits", name, e.SHA256)
				}
				if e.FileVersion == "" {
					t.Errorf("%s: no FileVersion recorded; a mismatch message could not say which release was expected", name)
				}
				switch {
				case strings.HasPrefix(e.URL, "https://download.visualstudio.microsoft.com/download/pr/"):
					if !strings.Contains(e.URL, "/"+strings.ToUpper(e.SHA256)+"/") {
						t.Errorf("%s: the Visual Studio CDN path does not carry the pinned digest %s: %s", name, e.SHA256, e.URL)
					}
				case strings.HasPrefix(e.URL, "https://download.microsoft.com/download/"):
					// download.microsoft.com paths are version-specific but carry no digest.
				default:
					t.Errorf("%s: URL is not a direct Microsoft download over TLS: %s", name, e.URL)
				}
				if strings.Contains(e.URL, "aka.ms") || strings.Contains(e.URL, "fwlink") {
					t.Errorf("%s: URL is a mutable alias, which a pinned digest disagrees with as soon as Microsoft ships a new file: %s", name, e.URL)
				}
				// The alias is the other half of the pin (#49): the mutable link the URL was
				// resolved from, which `just repin-check` follows to see whether Microsoft has
				// moved on. It must be one, and must not be the pin itself.
				switch {
				case e.Alias == "":
					t.Errorf("%s: no Alias; re-pinning cannot tell where to look for a newer build", name)
				case !strings.HasPrefix(e.Alias, "https://aka.ms/") && !strings.HasPrefix(e.Alias, "https://go.microsoft.com/fwlink/"):
					t.Errorf("%s: Alias is not an aka.ms or fwlink address: %s", name, e.Alias)
				case e.Alias == e.URL:
					t.Errorf("%s: Alias equals the pinned URL, so it cannot reveal a move", name)
				}
			}
		}
	}
	if count != 8 {
		t.Errorf("%d entries pinned; the table had 8 when this test was written — if one was added or removed, update this and docs/prerequisites.md", count)
	}
}

// pinTestEntry points one table entry at a local server serving body, with the digest of
// body pinned, and restores the entry afterwards. Tests in this package run sequentially,
// so mutating the shared table is safe here.
func pinTestEntry(t *testing.T, body []byte, serve func(w http.ResponseWriter, r *http.Request)) (entry PrerequisiteURL, hits *int32) {
	t.Helper()
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		serve(w, r)
	}))
	t.Cleanup(srv.Close)

	orig := DownloadURLs["vcredist"]["2022"]["x64"]
	t.Cleanup(func() { DownloadURLs["vcredist"]["2022"]["x64"] = orig })

	entry = orig
	entry.URL = srv.URL + "/vc_redist.x64.exe"
	sum := sha256.Sum256(body)
	entry.SHA256 = hex.EncodeToString(sum[:])
	DownloadURLs["vcredist"]["2022"]["x64"] = entry
	return entry, &n
}

func serveBytes(body []byte) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) { w.Write(body) }
}

func collect() (func(string), *[]string) {
	var msgs []string
	return func(m string) { msgs = append(msgs, m) }, &msgs
}

// A fresh download is verified before it exists under the cached name.
func TestDownloadIsVerifiedBeforeItIsCached(t *testing.T) {
	good := []byte("the genuine redistributable\n")
	entry, hits := pinTestEntry(t, good, serveBytes(good))
	c := &Cache{CacheDir: t.TempDir()}
	progress, msgs := collect()

	path, err := c.EnsurePrerequisite("vcredist", "2022", "x64", "", progress)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(c.CacheDir, "vcredist", "2022", entry.FileName)
	if path != want {
		t.Errorf("path = %s, want %s", path, want)
	}
	if data, _ := os.ReadFile(path); string(data) != string(good) {
		t.Errorf("cached bytes are not what the server sent")
	}
	if *hits != 1 {
		t.Errorf("server hit %d times, want 1", *hits)
	}
	if left := tempFiles(t, filepath.Dir(path)); len(left) != 0 {
		t.Errorf("temporary download files were left behind: %v", left)
	}
	if joined := strings.Join(*msgs, "\n"); !strings.Contains(joined, "verified") {
		t.Errorf("progress never said the file was verified: %q", joined)
	}
}

// A download whose bytes do not match the pin never lands in the cache — not under the cached
// name and not under the temporary one — and the error names both digests and the remedies.
func TestCorruptDownloadNeverLandsInTheCache(t *testing.T) {
	entry, _ := pinTestEntry(t, []byte("what msis pinned\n"), serveBytes([]byte("what the network delivered\n")))
	c := &Cache{CacheDir: t.TempDir()}

	_, err := c.EnsurePrerequisite("vcredist", "2022", "x64", "", nil)
	if err == nil {
		t.Fatal("a mismatching download was accepted")
	}
	for _, want := range []string{"expected " + entry.SHA256, "got ", entry.FileVersion, `source="..."`, "republished"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	dest := filepath.Join(c.CacheDir, "vcredist", "2022", entry.FileName)
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("%s exists after a failed verification", dest)
	}
	if left := tempFiles(t, filepath.Dir(dest)); len(left) != 0 {
		t.Errorf("temporary download files exist after a failed verification: %v", left)
	}
}

// A cached file that no longer matches is not reused: it is discarded and downloaded again,
// and the build gets the verified file. This is the cache half of #30 — the path that used to
// return before verification was ever reached.
func TestTamperedCacheEntryIsReplaced(t *testing.T) {
	good := []byte("the genuine redistributable\n")
	entry, hits := pinTestEntry(t, good, serveBytes(good))
	c := &Cache{CacheDir: t.TempDir()}
	dest := filepath.Join(c.CacheDir, "vcredist", "2022", entry.FileName)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("something else entirely\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	progress, msgs := collect()

	path, err := c.EnsurePrerequisite("vcredist", "2022", "x64", "", progress)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != string(good) {
		t.Errorf("the tampered file was returned instead of the verified download")
	}
	if *hits != 1 {
		t.Errorf("server hit %d times, want exactly one re-download", *hits)
	}
	if joined := strings.Join(*msgs, "\n"); !strings.Contains(joined, "does not match") {
		t.Errorf("progress never reported the mismatch: %q", joined)
	}
}

// When the cached file AND the fresh download both fail, the build is refused, and the
// tampered file is not left in place for the next build to find.
func TestTamperedCacheAndBadDownloadRefuseTheBuild(t *testing.T) {
	entry, hits := pinTestEntry(t, []byte("what msis pinned\n"), serveBytes([]byte("still not it\n")))
	c := &Cache{CacheDir: t.TempDir()}
	dest := filepath.Join(c.CacheDir, "vcredist", "2022", entry.FileName)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := c.EnsurePrerequisite("vcredist", "2022", "x64", "", nil)
	if err == nil {
		t.Fatal("accepted a file that matched neither on disk nor from the network")
	}
	if *hits != 1 {
		t.Errorf("server hit %d times, want one attempt and then refusal", *hits)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("the tampered cached file is still there after refusal")
	}
}

// A cached file that matches is reused without touching the network.
func TestVerifiedCacheEntryIsReusedOffline(t *testing.T) {
	good := []byte("the genuine redistributable\n")
	entry, hits := pinTestEntry(t, good, serveBytes(good))
	c := &Cache{CacheDir: t.TempDir()}
	dest := filepath.Join(c.CacheDir, "vcredist", "2022", entry.FileName)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, good, 0o644); err != nil {
		t.Fatal(err)
	}

	path, err := c.EnsurePrerequisite("vcredist", "2022", "x64", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if path != dest {
		t.Errorf("path = %s, want the cached %s", path, dest)
	}
	if *hits != 0 {
		t.Errorf("server hit %d times; a verified cache entry needs no network", *hits)
	}
}

// An entry without a digest is refused before any network access.
func TestUnpinnedEntryIsRefused(t *testing.T) {
	_, hits := pinTestEntry(t, []byte("x"), serveBytes([]byte("x")))
	e := DownloadURLs["vcredist"]["2022"]["x64"]
	e.SHA256 = ""
	DownloadURLs["vcredist"]["2022"]["x64"] = e
	c := &Cache{CacheDir: t.TempDir()}

	_, err := c.EnsurePrerequisite("vcredist", "2022", "x64", "", nil)
	if err == nil || !strings.Contains(err.Error(), "no digest is pinned") {
		t.Fatalf("want a refusal naming the missing pin, got %v", err)
	}
	if *hits != 0 {
		t.Errorf("server hit %d times; an unpinned entry must not be downloaded", *hits)
	}
}

// The custom-source path is unchanged: returned as-is, unverified, which the docs state.
func TestCustomSourceIsNotVerified(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "vc_redist.x64.exe")
	if err := os.WriteFile(custom, []byte("whatever the author supplied\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, hits := pinTestEntry(t, []byte("pinned\n"), serveBytes([]byte("pinned\n")))
	c := &Cache{CacheDir: dir}

	path, err := c.EnsurePrerequisite("vcredist", "2022", "x64", custom, nil)
	if err != nil || path != custom {
		t.Fatalf("path=%q err=%v, want the custom source returned as-is", path, err)
	}
	if *hits != 0 {
		t.Errorf("server hit %d times for a custom source", *hits)
	}
}

// VerifyCached reports what a build would find, entry by entry, without deleting anything.
func TestVerifyCachedReportsEachFile(t *testing.T) {
	good := []byte("the genuine redistributable\n")
	entry, _ := pinTestEntry(t, good, serveBytes(good))
	c := &Cache{CacheDir: t.TempDir()}
	sub := filepath.Join(c.CacheDir, "vcredist", "2022")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		entry.FileName:                good,                        // verifies
		"vc_redist.x86.exe":           []byte("not the x86 pin\n"), // real pin, wrong bytes
		"vc_redist.somethingelse.exe": []byte("stray\n"),           // no pin has this name
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(sub, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := c.VerifyCached()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]CachedEntry{}
	for _, e := range entries {
		got[filepath.Base(e.RelPath)] = e
	}
	if e := got[entry.FileName]; !e.Verified || e.Detail != "" {
		t.Errorf("verified file reported as %+v", e)
	}
	if e := got["vc_redist.x86.exe"]; e.Verified || !strings.Contains(e.Detail, "does not match") {
		t.Errorf("mismatching file reported as %+v", e)
	}
	if e := got["vc_redist.somethingelse.exe"]; e.Verified || !strings.Contains(e.Detail, "no pinned download") {
		t.Errorf("stray file reported as %+v", e)
	}
	for name := range files {
		if _, err := os.Stat(filepath.Join(sub, name)); err != nil {
			t.Errorf("%s was removed by a read-only check", name)
		}
	}
}

// tempFiles lists the in-progress download files in a cache subdirectory.
func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.download"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

// Two builds fetching the same prerequisite at once must not share a temporary file. Round-1
// review finding: with a fixed "<name>.download" path, a build that had verified its own bytes
// could rename the OTHER build's unchecked bytes into the cache, and the other build's later
// verification failure could not retract that. So each download gets an exclusively created
// temporary file, and each build publishes only what it verified itself.
//
// Coordinated, not timing-based: the server holds both requests at a barrier until both have
// arrived, so the cache directory can be inspected while both downloads are in flight. One
// request is then served the pinned bytes and the other something else.
func TestConcurrentDownloadsUseSeparateTemporaryFiles(t *testing.T) {
	good := []byte("the genuine redistributable\n")
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	var order int32
	entry, _ := pinTestEntry(t, good, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&order, 1)
		arrived <- struct{}{}
		<-release
		if n == 1 {
			w.Write(good)
		} else {
			w.Write([]byte("not what msis pinned\n"))
		}
	})
	c := &Cache{CacheDir: t.TempDir()}
	sub := filepath.Join(c.CacheDir, "vcredist", "2022")

	type result struct {
		path string
		err  error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			p, err := c.EnsurePrerequisite("vcredist", "2022", "x64", "", nil)
			results <- result{p, err}
		}()
	}

	// Both downloads are in flight: each must have a temporary file of its own.
	<-arrived
	<-arrived
	if inFlight := tempFiles(t, sub); len(inFlight) != 2 {
		t.Errorf("%d temporary file(s) while two downloads are in flight, want 2 (one per download): %v",
			len(inFlight), inFlight)
	}
	close(release)

	var ok, failed int
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err == nil {
			ok++
		} else {
			failed++
			if !strings.Contains(r.err.Error(), "does not match the digest") {
				t.Errorf("the mismatching download failed for another reason: %v", r.err)
			}
		}
	}
	if ok != 1 || failed != 1 {
		t.Errorf("%d succeeded and %d failed; want exactly the verified download to succeed", ok, failed)
	}
	dest := filepath.Join(sub, entry.FileName)
	if data, err := os.ReadFile(dest); err != nil || string(data) != string(good) {
		t.Errorf("the cached file is not the verified bytes (err=%v): %q", err, data)
	}
	if left := tempFiles(t, sub); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}
