//go:build windows

package msiread

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gersonkurz/msis/internal/wix"
)

// TestExtractedPayloadMatchesTheOriginalFiles is the check that decides whether cabinet
// extraction can be trusted at all.
//
// FDICopy returning success proves only that it did not give up. What matters for an SBOM is
// that the bytes are right, and the only way to know that is to compare them against files whose
// content is independently known. So the package is BUILT HERE, from the working tree, by the
// msis this tree compiles (see builtPackage): every payload in it has a source file the test
// can hash, and the two are never out of step.
//
// It used to read whatever bootstrap/dist/*-x64.msi happened to be on the machine and compare
// that against the current templates - a release artifact, gitignored, built from whatever the
// tree was on the day. After pulling template changes it failed on a clean HEAD, blocking the
// Verify gate for every task until someone rebuilt the release by hand (#47). The self-built
// package has the same shape - a 16 MB executable and a template tree in an LZX cabinet -
// without the dependence on machine state.
//
// A decompressor that returned plausible-looking rubbish would pass every other test in this
// package and produce an SBOM full of confident, wrong hashes. So the comparison is complete in
// both directions: every file the package carries must match a source, and every source the
// script packaged must appear in the package.
func TestExtractedPayloadMatchesTheOriginalFiles(t *testing.T) {
	b := builtPackage(t)

	pkg, err := Read(b.msi)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	seen := map[string]bool{}
	for _, f := range pkg.Files {
		src, ok := b.sourceOf(f)
		if !ok {
			t.Errorf("%s (%s) at %s: the package carries a file the script did not package", f.Name, f.ID, f.Target)
			continue
		}
		seen[src] = true
		want, err := hashFile(src)
		if err != nil {
			t.Errorf("%s: %v", f.Name, err)
			continue
		}
		if f.SHA256 == "" {
			t.Errorf("%s (%s) has no digest; payload extraction did not cover it", f.Name, f.ID)
			continue
		}
		if f.SHA256 != want {
			t.Errorf("%s (%s): extracted bytes do not match %s\n  extracted %s\n  original  %s",
				f.Name, f.ID, src, f.SHA256, want)
		}
	}
	for _, src := range b.sources {
		if !seen[src] {
			t.Errorf("%s was packaged but is not in the inventory", src)
		}
	}
	if len(seen) < 3 {
		t.Fatalf("only %d file(s) compared; want the binary and the template tree", len(seen))
	}
	t.Logf("%d payload files matched their sources", len(seen))
}

// TestEveryPayloadFileIsHashed: a file listed in the inventory with no digest is the
// "looks complete, is not" output this package exists to avoid. The only acceptable reason is a
// cabinet that did not travel with the package, and that has to be recorded on the Media row.
func TestEveryPayloadFileIsHashed(t *testing.T) {
	msi := builtPackage(t).msi

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
	msi := builtPackage(t).msi
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

// built is the package the payload tests read: an MSI this test run assembled from the working
// tree, and the list of files that went into it.
type built struct {
	msi       string
	exe       string   // the msis binary compiled for the package, also its first payload
	templates string   // the repo's templates directory, packaged as the release does
	sources   []string // every file the script packaged, absolute
}

var (
	buildOnce sync.Once
	buildErr  error
	buildOut  *built
	buildDir  string // set before anything is written into it, so a failed assembly is cleaned too
)

// TestMain gives the shared package a lifetime: it is built by the first test that needs it,
// used by the others, and removed here after the last one. os.MkdirTemp cleans up nothing by
// itself - round-1 review found three leaked 25 MB copies from earlier runs.
func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

// attr escapes a value for an XML attribute. Paths go into the generated .msis, and a checkout
// or temp directory may contain '&' (C:\R&D\...) - the build directory here deliberately does.
func attr(s string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return s
	}
	return b.String()
}

// builtPackage assembles the package once per test binary and hands the same one to every
// test that asks. It mirrors bootstrap/setup.msis - the self-packaging release - with the
// msis binary compiled from THIS tree and the templates read from THIS tree, so the package's
// contents are known to the byte and cannot go stale against the checkout (#47). Skips, as the
// cmd/msis build tests do, when the Go toolchain or wix is not available.
//
// The build goes through the real CLI rather than the generator package: this is the package
// the release recipe produces, made the way the recipe makes it, minus the release-only steps
// (hook DLL build, SBOM capture). About ten seconds, once.
func builtPackage(t *testing.T) *built {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; this test compiles msis to package it")
	}
	if !wix.IsWixAvailable() {
		t.Skip("wix is not available; this test builds a real package")
	}
	buildOnce.Do(func() { buildOut, buildErr = assemblePackage() })
	if buildErr != nil {
		t.Fatalf("assembling the package under test: %v", buildErr)
	}
	return buildOut
}

func assemblePackage() (*built, error) {
	// Not t.TempDir(): the package outlives the first test that built it and is shared by
	// the others in this binary; TestMain removes the directory after the last test. The name
	// carries an ampersand on purpose, so every run proves the paths survive XML and WiX.
	dir, err := os.MkdirTemp("", "msiread-payload-R&D-*")
	if err != nil {
		return nil, err
	}
	buildDir = dir
	templates, err := filepath.Abs(filepath.Join("..", "..", "templates"))
	if err != nil {
		return nil, err
	}
	b := &built{
		msi:       filepath.Join(dir, "pkg.msi"),
		exe:       filepath.Join(dir, "msis.exe"),
		templates: templates,
	}

	// 1. The binary - a real msis, so the cabinet carries a real 16 MB executable.
	build := exec.Command("go", "build", "-o", b.exe, "github.com/gersonkurz/msis/cmd/msis")
	if out, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("go build: %v\n%s", err, out)
	}

	// 2. The script: bootstrap/setup.msis with absolute sources, so it can live in the temp
	// directory. BUILD_TARGET is relative and msis runs with the temp directory as its
	// working directory (D6), so every artifact lands there.
	//
	// TRACKED sources only. The release packages templates/x64, x86 and arm64 as whole
	// directories, which is where `just build-hooks` stages msi-simplica.dll - a gitignored
	// build artifact. A fresh checkout has no arm64 directory at all (nothing tracked lives
	// there) and no DLLs, so packaging those directories made the test depend on a release
	// step having run on the machine - the dependence #47 exists to remove. The tracked
	// template files are named individually where their directory also holds staged DLLs.
	packaged := []struct{ source, target string }{
		{b.exe, `[INSTALLDIR]`},
		{filepath.Join(templates, "x64", "template.wxs"), `[LOCALAPPDATADIR]templates\x64`},
		{filepath.Join(templates, "x86", "template.wxs"), `[LOCALAPPDATADIR]templates\x86`},
		{filepath.Join(templates, "x86", "template-silent.wxs"), `[LOCALAPPDATADIR]templates\x86`},
		{filepath.Join(templates, "minimal"), `[LOCALAPPDATADIR]templates\minimal`},
		{filepath.Join(templates, "minimal-x86"), `[LOCALAPPDATADIR]templates\minimal-x86`},
		{filepath.Join(templates, "bundle.wxs"), `[LOCALAPPDATADIR]templates`},
		{filepath.Join(templates, "bundle-silent.wxs"), `[LOCALAPPDATADIR]templates`},
		{filepath.Join(templates, "wixlib"), `[LOCALAPPDATADIR]templates\wixlib`},
		{filepath.Join(templates, "custom"), `[LOCALAPPDATADIR]custom`},
	}
	var sb strings.Builder
	sb.WriteString(`<setup>
  <set name="PRODUCT_NAME" value="MSIS"/>
  <set name="PRODUCT_VERSION" value="9.9.9"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{E7A3B8C1-5D2F-4A9E-B6C4-8F1D3E2A7B5C}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="pkg.msi"/>
  <set name="INSTALLDIR" value="MSIS"/>
  <feature name="MSIS">
`)
	for _, p := range packaged {
		fmt.Fprintf(&sb, "    <files source=\"%s\" target=\"%s\"/>\n", attr(p.source), attr(p.target))
		// What the script packages is what the test expects to find: a directory
		// contributes every regular file beneath it, a file contributes itself.
		if err := filepath.WalkDir(p.source, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				b.sources = append(b.sources, path)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	sb.WriteString("  </feature>\n</setup>\n")
	script := filepath.Join(dir, "setup.msis")
	if err := os.WriteFile(script, []byte(sb.String()), 0o644); err != nil {
		return nil, err
	}

	// 3. Build it with the compiled msis, as `just release` does.
	msis := exec.Command(b.exe, "--build",
		"--templatefolder="+templates,
		"--template="+filepath.Join(templates, "minimal", "template.wxs"),
		script)
	msis.Dir = dir
	if out, err := msis.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("msis --build: %v\n%s", err, out)
	}
	if _, err := os.Stat(b.msi); err != nil {
		return nil, fmt.Errorf("msis reported success but %s is not there: %v", b.msi, err)
	}
	return b, nil
}

// sourceOf maps a file in the package back to the file it was built from, by the target path
// the package records for it. The script packages the binary under INSTALLDIR and the template
// tree under LOCALAPPDATADIR, so the segment after "templates\" (or "custom\") is the path
// under the repo's templates directory.
func (b *built) sourceOf(f File) (string, bool) {
	target := filepath.ToSlash(f.Target)
	switch {
	case strings.HasSuffix(target, "/"+filepath.Base(b.exe)) && !strings.Contains(target, "/templates/"):
		return b.exe, true
	case strings.Contains(target, "/templates/"):
		rel := target[strings.LastIndex(target, "/templates/")+len("/templates/"):]
		return filepath.Join(b.templates, filepath.FromSlash(rel)), true
	case strings.Contains(target, "/custom/"):
		rel := target[strings.LastIndex(target, "/custom/")+len("/custom/"):]
		return filepath.Join(b.templates, "custom", filepath.FromSlash(rel)), true
	}
	return "", false
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
