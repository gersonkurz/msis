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

// These run against testdata/fixture.msi, a committed 32 kB package built to carry the shapes no
// released msis package contains. Because it is committed, they run in a clean checkout with no
// WiX toolchain — which is what the release-package tests in this directory cannot do.
//
// See testdata/README.md for what is in it and how to rebuild it.

func fixturePath() string { return filepath.Join("testdata", "fixture.msi") }

func readFixture(t *testing.T) *Package {
	t.Helper()
	pkg, err := Read(fixturePath())
	if err != nil {
		t.Fatalf("Read(%s): %v", fixturePath(), err)
	}
	return pkg
}

func fileNamed(p *Package, name string) *File {
	for i := range p.Files {
		if p.Files[i].Name == name {
			return &p.Files[i]
		}
	}
	return nil
}

// A merge module's files are merged into the package's own File table at build time, so they are
// in the artifact and absent from any build-time view of the payload. This is the case that
// carries the "reading the artifact is more accurate" claim in #29, so it is worth proving
// against a package that actually has one.
func TestMergeModuleContributionIsInventoried(t *testing.T) {
	pkg := readFixture(t)

	f := fileNamed(pkg, "merged.txt")
	if f == nil {
		t.Fatalf("merged.txt is missing; the merge module's contribution was not read")
	}
	if !strings.Contains(f.Target, `merged\`) {
		t.Errorf("merged.txt target = %q, want it under the merge directory", f.Target)
	}
	// The File key a merge module produces carries the module GUID, which is exactly the sort
	// of identifier that must survive rather than be normalised away.
	if !strings.HasPrefix(f.ID, "F_Merged.") {
		t.Errorf("merged file ID = %q, want the module-qualified key", f.ID)
	}
	if want := hashOf(t, filepath.Join("testdata", "merged.txt")); f.SHA256 != want {
		t.Errorf("merged.txt digest = %s, want %s (the source it was built from)", f.SHA256, want)
	}
}

// A level-zero feature is not installed by default, but its payload is in the package. An
// inventory that omitted it would under-report what shipped — and an administrative install,
// the extraction route rejected in #29, omits exactly this.
func TestLevelZeroFeaturePayloadIsInventoried(t *testing.T) {
	pkg := readFixture(t)

	f := fileNamed(pkg, "hidden.txt")
	if f == nil {
		t.Fatal("hidden.txt is missing; a level-zero feature's payload was dropped")
	}
	if f.SHA256 == "" {
		t.Error("hidden.txt has no digest")
	}
	if want := hashOf(t, filepath.Join("testdata", "hidden.txt")); f.SHA256 != want {
		t.Errorf("hidden.txt digest = %s, want %s", f.SHA256, want)
	}
}

// Against a real package this time, not a hand-built Directory map: SourceDir is special only
// as the root's DefaultDir.
func TestSourceDirSubdirectorySurvivesInARealPackage(t *testing.T) {
	pkg := readFixture(t)

	f := fileNamed(pkg, "nested.txt")
	if f == nil {
		t.Fatal("nested.txt is missing")
	}
	if !strings.Contains(f.Target, `\SourceDir\nested.txt`) {
		t.Errorf("target = %q, want the SourceDir directory to appear in the path", f.Target)
	}
}

// The fixture's Binary stream and its cabinet both have to read, since those are the two
// distinct payload paths.
func TestFixtureCoversBothPayloadPaths(t *testing.T) {
	pkg := readFixture(t)

	if len(pkg.Binaries) != 1 || pkg.Binaries[0].Name != "FixtureBinary" {
		t.Errorf("Binary streams = %+v, want exactly FixtureBinary", pkg.Binaries)
	} else if want := hashOf(t, filepath.Join("testdata", "payload.txt")); pkg.Binaries[0].SHA256 != want {
		t.Errorf("FixtureBinary digest = %s, want %s", pkg.Binaries[0].SHA256, want)
	}

	if len(pkg.Media) != 1 || !pkg.Media[0].Embedded() {
		t.Fatalf("Media = %+v, want one embedded cabinet", pkg.Media)
	}
	if pkg.Media[0].Unavailable != "" {
		t.Errorf("the embedded cabinet was reported unavailable: %s", pkg.Media[0].Unavailable)
	}

	// Four payload files, every one hashed from the cabinet.
	if len(pkg.Files) != 4 {
		t.Errorf("payload files = %d, want 4 (main, nested, hidden, merged)", len(pkg.Files))
	}
	for _, f := range pkg.Files {
		if len(f.SHA256) != 64 {
			t.Errorf("%s (%s) has no digest", f.ID, f.Name)
		}
	}
}

func hashOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
