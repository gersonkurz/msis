//go:build windows

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/prereqcache"
)

// The production signature reader is the trust evidence behind every future pin, so it has to
// run for real at least once per test run rather than only be replaced by a stub (round-1
// review). The subject is a currently pinned Microsoft installer as it sits in this machine's
// prerequisite cache - the same bytes msis chains - fed through verify with the downloader
// replaced by a copy from the cache. Everything else is production code: the PowerShell
// invocation, its output parsing, the digest, the CDN hash-segment check.
func TestVerifyReadsARealMicrosoftSignature(t *testing.T) {
	pin := prereqcache.DownloadURLs["vcredist"]["2022"]["x64"]
	cached := filepath.Join(prereqcache.GetDefaultCacheDir(), pin.Type, pin.Version, pin.FileName)
	if _, err := os.Stat(cached); err != nil {
		t.Skipf("%s is not in the prerequisite cache; build any package with <requires type=\"vcredist\" version=\"2022\"/> once to populate it", cached)
	}

	orig := download
	download = func(url, dest string) (int64, error) {
		if url != pin.URL {
			t.Fatalf("download asked for %s, want the pinned URL", url)
		}
		src, err := os.Open(cached)
		if err != nil {
			return 0, err
		}
		defer src.Close()
		dst, err := os.Create(dest)
		if err != nil {
			return 0, err
		}
		defer dst.Close()
		return io.Copy(dst, src)
	}
	t.Cleanup(func() { download = orig })

	// Pretend the alias moved to the very URL that is pinned: the bytes then have to come back
	// with the pinned digest and version, which is the strongest check available offline.
	f := finding{Pin: pin, Status: statusMoved, AliasTarget: pin.URL}
	verify(&f)

	if f.VerifyErr != nil || !f.Verified {
		t.Fatalf("verify failed on a genuine, currently pinned Microsoft installer: %v (status %q, signer %q, version %q)",
			f.VerifyErr, f.SigStatus, f.Signer, f.NewVersion)
	}
	if f.NewSHA256 != pin.SHA256 {
		t.Errorf("digest %s, want the pinned %s (the cache holds a different file?)", f.NewSHA256, pin.SHA256)
	}
	if f.SigStatus != "Valid" {
		t.Errorf("signature status %q, want Valid", f.SigStatus)
	}
	if !strings.Contains(f.Signer, "CN=Microsoft Corporation") {
		t.Errorf("signer %q, want Microsoft Corporation", f.Signer)
	}
	if f.NewVersion != pin.FileVersion {
		t.Errorf("file version %q, want the pinned %q", f.NewVersion, pin.FileVersion)
	}
	if f.Size == 0 {
		t.Error("no bytes were downloaded")
	}
}

// An unsigned file is refused by the production reader, with the status named.
func TestVerifyRejectsAnUnsignedFile(t *testing.T) {
	dir := t.TempDir()
	unsigned := filepath.Join(dir, "not-signed.exe")
	if err := os.WriteFile(unsigned, []byte("this is not a signed executable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := download
	download = func(url, dest string) (int64, error) {
		data, err := os.ReadFile(unsigned)
		if err != nil {
			return 0, err
		}
		return int64(len(data)), os.WriteFile(dest, data, 0o644)
	}
	t.Cleanup(func() { download = orig })

	pin := prereqcache.DownloadURLs["vcredist"]["2022"]["x64"]
	f := finding{Pin: pin, Status: statusMoved, AliasTarget: "https://example.invalid/not-signed.exe"}
	verify(&f)

	if f.Verified || f.VerifyErr == nil {
		t.Fatalf("an unsigned file was accepted: %+v", f)
	}
	if !strings.Contains(f.VerifyErr.Error(), "Authenticode status") {
		t.Errorf("error = %v, want the signature status named", f.VerifyErr)
	}
	if f.SigStatus == "Valid" {
		t.Errorf("PowerShell reported %q for an unsigned file", f.SigStatus)
	}
}
