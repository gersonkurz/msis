package wix

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(content))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// #67: an extension DLL embeds one .wixlib (a zip) per culture or platform among other bytes;
// every one is read, each located from its end-of-central-directory record, and a stray
// "PK\x05\x06" that is not a real record is skipped.
func TestExtensionPayloadsReadsEveryEmbeddedWixlib(t *testing.T) {
	dll := bytes.Join([][]byte{
		[]byte("MZ...managed assembly..."),
		zipOf(t, map[string]string{"wix-ir/bannrbmp.bmp": "banner", "wix-ir.json": "{}"}),
		[]byte("PK\x05\x06 not a record"),
		zipOf(t, map[string]string{"wix-ir/utilca.dll-1": "custom action"}),
		[]byte("...trailer"),
	}, nil)
	path := filepath.Join(t.TempDir(), "x.wixext.dll")
	if err := os.WriteFile(path, dll, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ExtensionPayloads(path)
	if err != nil {
		t.Fatal(err)
	}
	for content, entry := range map[string]string{"banner": "wix-ir/bannrbmp.bmp", "custom action": "wix-ir/utilca.dll-1"} {
		if got[sum(content)] != entry {
			t.Errorf("%s: got %q, want %q (all: %v)", entry, got[sum(content)], entry, got)
		}
	}
}

// placeExtension puts an extension DLL into a cache the way `wix extension add` lays it out.
func placeExtension(t *testing.T, cache, id, version, pkgFolder string) string {
	t.Helper()
	p := filepath.Join(cache, id, version, pkgFolder, id+".dll")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(version), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// #67's review: msis resolves an extension exactly as WiX's ExtensionManager.Load does, because
// the build is then handed that DLL - so what the SBOM attributes is what the build loaded.
func TestResolveExtensionsIsWixsLoader(t *testing.T) {
	home, work, redirected := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("WIX_EXTENSIONS", "")
	t.Setenv("CommonProgramW6432", t.TempDir())
	t.Setenv("CommonProgramFiles(x86)", t.TempDir())
	t.Setenv("CommonProgramFiles", t.TempDir())
	userCache := filepath.Join(home, ".wix", "extensions")
	resolve := func() ResolvedExtension {
		t.Helper()
		return ResolveExtensions(work, []string{extUI}, 6)[0]
	}

	// Unversioned, WiX takes the LATEST cached version, not the tool's: 6.0.2 over 6.0.0.
	placeExtension(t, userCache, extUI, "6.0.0", "wixext6")
	latest := placeExtension(t, userCache, extUI, "6.0.2", "wixext6")
	if got := resolve(); got.Path != latest || got.Version != "6.0.2" {
		t.Errorf("competing versions: got %+v, want 6.0.2 at %s", got, latest)
	}

	// The latest version holds no DLL for this WiX major: WiX moves to the NEXT LOCATION, not to
	// an older version in the same one - so nothing resolves here, and the bare id is handed on.
	placeExtension(t, userCache, extUI, "7.0.0", "wixext7")
	if got := resolve(); got.Path != "" || got.Version != "" {
		t.Errorf("the latest lacks wixext6: got %+v, want unresolved", got)
	}
	if args := extensionArgs([]ResolvedExtension{resolve()}, nil); len(args) != 2 || args[1] != extUI {
		t.Errorf("an unresolved extension is handed to wix as %v, want the bare id", args)
	}

	// WIX_EXTENSIONS redirects the user cache.
	t.Setenv("WIX_EXTENSIONS", redirected)
	moved := placeExtension(t, filepath.Join(redirected, ".wix", "extensions"), extUI, "6.0.1", "wixext6")
	if got := resolve(); got.Path != moved {
		t.Errorf("WIX_EXTENSIONS: got %+v, want %s", got, moved)
	}

	// The project cache in the build directory comes first of all.
	project := placeExtension(t, filepath.Join(work, ".wix", "extensions"), extUI, "6.0.0", "wixext6")
	if got := resolve(); got.Path != project {
		t.Errorf("project cache: got %+v, want %s", got, project)
	}

	// And what is resolved is what wix is told to load.
	if args := extensionArgs([]ResolvedExtension{resolve()}, nil); len(args) != 2 || args[1] != project {
		t.Errorf("wix is handed %v, want the resolved DLL", args)
	}
}

// #67's second review: WiX tries the reference as a FILE in its working directory before any
// cache, and resolves a relative cache root against that directory, not msis's. Either way the
// file msis hands WiX must be the one WiX would have loaded, or nothing is resolved.
func TestResolveExtensionsKeepsWixsPrecedence(t *testing.T) {
	home, work, caller := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("CommonProgramW6432", t.TempDir())
	t.Setenv("CommonProgramFiles(x86)", t.TempDir())
	t.Setenv("CommonProgramFiles", t.TempDir())
	t.Chdir(caller) // msis's own directory differs from WiX's working directory

	// A relative WIX_EXTENSIONS is relative to WiX's working directory. The same relative path
	// under msis's directory holds a competing, newer version, which must not be taken.
	t.Setenv("WIX_EXTENSIONS", "cache")
	want := placeExtension(t, filepath.Join(work, "cache", ".wix", "extensions"), extUI, "7.0.0", "wixext7")
	placeExtension(t, filepath.Join(caller, "cache", ".wix", "extensions"), extUI, "7.0.1", "wixext7")
	got := ResolveExtensions(work, []string{extUI}, 7)[0]
	if got.Path != want || !filepath.IsAbs(got.Path) {
		t.Errorf("relative WIX_EXTENSIONS: got %+v, want %s", got, want)
	}

	// Without WiX's working directory, neither rule can be applied: nothing is resolved.
	if got := ResolveExtensions("", []string{extUI}, 7)[0]; got.Path != "" {
		t.Errorf("no working directory: got %+v, want unresolved", got)
	}

	// A file named like the id beside the .wxs wins over every cache in WiX: left to WiX.
	if err := os.WriteFile(filepath.Join(work, extUI), []byte("an assembly"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveExtensions(work, []string{extUI}, 7)[0]; got.Path != "" {
		t.Errorf("a direct file and a cached extension: got %+v, want unresolved (WiX loads the file)", got)
	}
}

// WixVersion's grammar and order, as TryFindLatestVersionInFolder uses them.
func TestWixVersionOrder(t *testing.T) {
	for _, bad := range []string{"", "latest", "1.2.3.4.5", "1.2.", "1.2-", "1.2-rc.", "99999999999.0", "1..2"} {
		if _, ok := parseWixVersion(bad); ok {
			t.Errorf("%q parsed as a valid version", bad)
		}
	}
	ascending := []string{"6.0.0", "6.0.2", "6.0.10", "7.0.0-rc.1", "7.0.0-rc.2", "7.0.0-rc.10", "7.0.0", "v7.0.1"}
	for i := 1; i < len(ascending); i++ {
		a, _ := parseWixVersion(ascending[i-1])
		b, ok := parseWixVersion(ascending[i])
		if !ok || compareWixVersion(a, b) >= 0 {
			t.Errorf("%s is not below %s", ascending[i-1], ascending[i])
		}
	}
	a, _ := parseWixVersion("7.0.0+b8977d6")
	b, _ := parseWixVersion("7.0")
	if compareWixVersion(a, b) != 0 {
		t.Error("metadata, or a missing part, changed the order")
	}
}

// #67's review: an apparent end-of-central-directory record whose unsigned fields would wrap a
// 32-bit int is skipped, not sliced - the shipped x86 build panicked on it. Run under
// GOARCH=386 as well as natively.
func TestABogusZipRecordIsSkipped(t *testing.T) {
	record := make([]byte, 22)
	copy(record, "PK")
	record[2], record[3] = 5, 6                            // the end-of-central-directory signature
	binary.LittleEndian.PutUint32(record[12:], 0xffffff00) // central directory size
	binary.LittleEndian.PutUint32(record[16:], 0)          // central directory offset
	data := append(record, zipOf(t, map[string]string{"wix-ir/new.ico": "icon"})...)
	path := filepath.Join(t.TempDir(), "x.dll")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ExtensionPayloads(path)
	if err != nil {
		t.Fatal(err)
	}
	if got[sum("icon")] != "wix-ir/new.ico" || len(got) != 1 {
		t.Errorf("payloads %v, want the real zip's one entry", got)
	}
}

// The WiX version msis provisions must be pinned, or msis's own installers - and every one
// built after /SETUP-WIX - would lose the attribution silently.
func TestTheDefaultWixVersionIsPinned(t *testing.T) {
	for _, id := range MSIExtensions() {
		if _, ok := ExtensionPackageFacts(id, DefaultVersion); !ok {
			t.Errorf("%s %s is not pinned in extensionPackages", id, DefaultVersion)
		}
	}
}
