package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/buildrecord"
	"github.com/gersonkurz/msis/internal/ir"
)

// sha256Hex lives here rather than beside the build fixtures because these tests compile on
// every platform: the fixtures are //go:build windows (they drive the real wix CLI), so a
// helper defined there leaves the platform-independent tests undefined everywhere else.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// A bundle caches EVERY architecture of the VC++ runtime it can carry, each its own file with
// its own digest. Recording one guessed architecture would miss the others and, on an x86 or
// arm64 build, either find nothing or publish one architecture's digest under another's name.
//
// The generator already keeps what it resolved, so this reads that map rather than
// reconstructing one - which is also how a custom source is told apart from a download: a
// script-supplied prerequisite has no cache entry at all.
func TestEveryCachedArchitectureIsRecorded(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "setup.msis")
	if err := os.WriteFile(script, []byte("<setup/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A stand-in cache, laid out the way the real one is.
	cached := map[string]string{}
	digests := map[string]string{}
	for _, arch := range []string{"x64", "x86", "arm64"} {
		p := filepath.Join(dir, "cache", "prerequisites", "vcredist", "2022",
			"vc_redist."+arch+".exe")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "the " + arch + " redistributable\n"
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		cached["vcredist/2022/"+arch] = p
		digests[arch] = sha256Hex([]byte(body))
	}

	rec := buildrecord.New(buildrecord.PathAutoBundle, script, nil)
	recordPrerequisites(rec, []ir.Prerequisite{{Type: "vcredist", Version: "2022"}}, cached)
	rec.Sort()

	if len(rec.Prereqs) != 3 {
		t.Fatalf("%d prerequisites recorded, want one per cached architecture: %+v",
			len(rec.Prereqs), rec.Prereqs)
	}
	seen := map[string]buildrecord.Prerequisite{}
	for _, p := range rec.Prereqs {
		seen[p.Arch] = p
	}
	for _, arch := range []string{"x64", "x86", "arm64"} {
		p, ok := seen[arch]
		if !ok {
			t.Errorf("%s was cached but not recorded", arch)
			continue
		}
		// Each carries ITS OWN digest - the whole point of not guessing one architecture.
		if p.SHA256 != digests[arch] {
			t.Errorf("%s: digest %s, want %s", arch, p.SHA256[:16], digests[arch][:16])
		}
		if !strings.HasSuffix(p.CachePath, "vc_redist."+arch+".exe") {
			t.Errorf("%s: cache entry = %q", arch, p.CachePath)
		}
		if !strings.HasPrefix(p.CachePath, "prerequisite-cache:") {
			t.Errorf("%s: cache path is not symbolic: %q", arch, p.CachePath)
		}
		if p.DownloadURL == "" {
			t.Errorf("%s: a cached prerequisite has a download URL; it was not recorded", arch)
		}
		if !strings.Contains(p.DownloadURL, arch) {
			t.Errorf("%s: download URL %q is another architecture's", arch, p.DownloadURL)
		}
	}
}

// A prerequisite the SCRIPT supplied was never downloaded. Claiming a Microsoft URL for bytes
// somebody authored locally is an invented provenance, and a URL looks more like evidence than
// most invented things do.
func TestAScriptSuppliedPrerequisiteClaimsNoDownload(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "setup.msis")
	if err := os.WriteFile(script, []byte("<setup/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "locally authored stand-in\n"
	if err := os.WriteFile(filepath.Join(dir, "stub.exe"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := buildrecord.New(buildrecord.PathBundle, script, nil)
	// A cache map that WOULD match if the code consulted it regardless of the source.
	cached := map[string]string{"vcredist/2022/x64": filepath.Join(dir, "stub.exe")}
	recordPrerequisites(rec, []ir.Prerequisite{
		{Type: "vcredist", Version: "2022", Source: "stub.exe"},
	}, cached)

	if len(rec.Prereqs) != 1 {
		t.Fatalf("%+v", rec.Prereqs)
	}
	p := rec.Prereqs[0]
	if p.DownloadURL != "" {
		t.Errorf("a script-supplied prerequisite claims it was downloaded from %q", p.DownloadURL)
	}
	if p.CachePath != "" {
		t.Errorf("a script-supplied prerequisite claims the cache entry %q", p.CachePath)
	}
	if p.Source != "stub.exe" {
		t.Errorf("source = %q, want stub.exe", p.Source)
	}
	if p.SHA256 != sha256Hex([]byte(body)) {
		t.Errorf("digest = %q, want the local file's", p.SHA256)
	}
}

// A prerequisite the build arranged but resolved nothing for is recorded as unaccounted, not
// dropped: the bundle chains it either way, and a reader has to know msis cannot say where its
// bytes came from.
func TestAnUnresolvedPrerequisiteIsRecorded(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "setup.msis")
	if err := os.WriteFile(script, []byte("<setup/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := buildrecord.New(buildrecord.PathBundle, script, nil)
	recordPrerequisites(rec, []ir.Prerequisite{{Type: "netfx", Version: "4.8"}}, nil)

	if len(rec.Prereqs) != 1 {
		t.Fatalf("a prerequisite with nothing resolved was dropped: %+v", rec.Prereqs)
	}
	if len(rec.Unresolved) != 1 || !strings.Contains(rec.Unresolved[0], "netfx") {
		t.Errorf("unresolved = %v, want it to name the prerequisite", rec.Unresolved)
	}
}

// An auto-bundle's prerequisites and its chained MSI are in the BUNDLE. The MSI it wraps
// carries neither, and a record handed to both documents made the MSI claim both.
func TestEachArtifactSeesOnlyItsOwnPart(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "setup.msis")
	if err := os.WriteFile(script, []byte("<setup/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := buildrecord.New(buildrecord.PathAutoBundle, script, nil)
	rec.AddFile("FILE_ID00000", "a.txt")
	rec.AddChained("MsiPackage", "a.txt")
	rec.AddPrerequisiteFromSource("vcredist", "2022", "a.txt")
	rec.AddRuntime("netfx", "4.8", "cond")
	rec.AddTool("msis", "test")

	msi := rec.For(buildrecord.ScopeArtifactMSI)
	if len(msi.Files) != 1 || len(msi.Runtimes) != 1 {
		t.Errorf("the MSI's part lost its own payload: %+v", msi)
	}
	if len(msi.Prereqs) != 0 || len(msi.Chained) != 0 {
		t.Errorf("the MSI's part carries the wrapper's contents: %d prereqs, %d chained",
			len(msi.Prereqs), len(msi.Chained))
	}

	bundle := rec.For(buildrecord.ScopeArtifactBundle)
	if len(bundle.Prereqs) != 1 || len(bundle.Chained) != 1 {
		t.Errorf("the bundle's part lost its own contents: %+v", bundle)
	}
	if len(bundle.Files) != 0 || len(bundle.Binaries) != 0 {
		t.Errorf("the bundle's part carries the MSI's payload: %d files", len(bundle.Files))
	}

	// What describes the BUILD rather than either output is shared.
	for _, part := range []*buildrecord.Record{msi, bundle} {
		if part.Path != buildrecord.PathAutoBundle || part.Script != "setup.msis" {
			t.Errorf("a scoped record lost which build produced it: %+v", part)
		}
		if len(part.Toolchain) != 1 {
			t.Errorf("a scoped record lost the toolchain")
		}
	}
}
