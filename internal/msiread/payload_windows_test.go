//go:build windows

package msiread

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExtractedPayloadMatchesTheOriginalFiles is the check that decides whether cabinet
// extraction can be trusted at all.
//
// FDICopy returning success proves only that it did not give up. What matters for an SBOM is
// that the bytes are right, and the only way to know that is to compare them against files whose
// content is independently known. The release package was built from files that are still on
// disk, so every one of them is a reference.
//
// A decompressor that returned plausible-looking rubbish would pass every other test in this
// package and produce an SBOM full of confident, wrong hashes.
func TestExtractedPayloadMatchesTheOriginalFiles(t *testing.T) {
	msi := findX64Package(t)
	if msi == "" {
		t.Skip("no bootstrap/dist/*-x64.msi; run `just release-all` to produce one")
	}

	pkg, err := Read(msi)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	// Each payload file, by the name it installs under, paired with the file it was built
	// from. These are committed sources plus the binary the release recipe packaged.
	references := map[string]string{
		"msis.exe":     filepath.Join("..", "..", "bootstrap", "msis-x64.exe"),
		"template.wxs": filepath.Join("..", "..", "templates", "minimal", "template.wxs"),
		"bundle.wxs":   filepath.Join("..", "..", "templates", "bundle.wxs"),
		"en-us.wxl":    filepath.Join("..", "..", "templates", "wixlib", "en-us.wxl"),
	}

	checked := 0
	for _, f := range pkg.Files {
		ref, ok := references[f.Name]
		if !ok {
			continue
		}
		// template.wxs appears more than once (minimal, x64, x86); only compare the one
		// installed from the minimal template directory.
		if f.Name == "template.wxs" && !strings.Contains(f.Target, `minimal`) {
			continue
		}

		want, err := hashFile(ref)
		if err != nil {
			t.Logf("skipping %s: %v", f.Name, err)
			continue
		}
		if f.SHA256 == "" {
			t.Errorf("%s (%s) has no digest; payload extraction did not cover it", f.Name, f.ID)
			continue
		}
		if f.SHA256 != want {
			t.Errorf("%s (%s): extracted bytes do not match %s\n  extracted %s\n  original  %s",
				f.Name, f.ID, ref, f.SHA256, want)
			continue
		}
		checked++
		t.Logf("%-14s matches %s", f.Name, ref)
	}

	if checked == 0 {
		t.Fatal("no payload file could be compared against its original; the check proved nothing")
	}
	if checked < 3 {
		t.Errorf("only %d file(s) compared; want the binary and at least two sources", checked)
	}
}

// TestEveryPayloadFileIsHashed: a file listed in the inventory with no digest is the
// "looks complete, is not" output this package exists to avoid. The only acceptable reason is a
// cabinet that did not travel with the package, and that has to be recorded on the Media row.
func TestEveryPayloadFileIsHashed(t *testing.T) {
	msi := findReleasedPackage(t)
	if msi == "" {
		t.Skip("no released .msi in bootstrap/dist; run `just release-all` to produce one")
	}

	pkg, err := Read(msi)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	var unexcused []string
	for _, f := range pkg.Files {
		if f.SHA256 == "" {
			unexcused = append(unexcused, f.ID+" ("+f.Name+")")
			continue
		}
		if len(f.SHA256) != 64 {
			t.Errorf("%s: digest %q is not a SHA-256", f.ID, f.SHA256)
		}
	}
	if len(unexcused) > 0 && unavailableCabinets(pkg) == "" {
		t.Errorf("%d file(s) have no digest and no cabinet is recorded as unavailable: %s",
			len(unexcused), strings.Join(unexcused, ", "))
	}

	// Zero-length payload is legitimate (the package ships a .gitkeep) and must still be
	// hashed rather than skipped as "nothing to hash".
	empty := sha256.Sum256(nil)
	var sawEmpty bool
	for _, f := range pkg.Files {
		if f.Size == 0 {
			sawEmpty = true
			if f.SHA256 != hex.EncodeToString(empty[:]) {
				t.Errorf("%s is empty but its digest is %s, want the digest of no bytes",
					f.ID, f.SHA256)
			}
		}
	}
	if !sawEmpty {
		t.Log("no zero-length payload in this package; the empty-file case was not exercised")
	}
}

// TestExtractionIsDeterministic: the same cabinet must yield the same bytes every time, or the
// SBOM built on it is not diffable.
func TestExtractionIsDeterministic(t *testing.T) {
	msi := findReleasedPackage(t)
	if msi == "" {
		t.Skip("no released .msi in bootstrap/dist")
	}
	first, err := Read(msi)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	second, err := Read(msi)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for i := range first.Files {
		if first.Files[i].SHA256 != second.Files[i].SHA256 {
			t.Fatalf("%s hashed differently across reads: %s vs %s",
				first.Files[i].ID, first.Files[i].SHA256, second.Files[i].SHA256)
		}
	}
}

func findX64Package(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("..", "..", "bootstrap", "dist", "*-x64.msi"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
