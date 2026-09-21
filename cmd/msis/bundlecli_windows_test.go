//go:build windows

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The reader and the emitter can both be complete while the terminal is not, and the terminal
// is where a human actually looks. These drive the real CLI entry points against the real
// fixture bundle and read what they printed: a payload the reader found and the listing omits
// is a hole no reader test can see.

func bundleFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "internal", "burnread", "testdata", "fixture.exe")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("the bundle fixture is not present: %v", err)
	}
	return path
}

// capture runs fn with stdout redirected and returns what it printed.
func capture(t *testing.T, fn func() error) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(r)
		done <- string(out)
	}()

	runErr := fn()
	os.Stdout = saved
	w.Close()
	out := <-done
	r.Close()

	if runErr != nil {
		t.Fatalf("the command failed: %v\noutput so far:\n%s", runErr, out)
	}
	return out
}

// /INSPECT has to list every payload the bundle carries. The fixture has two that belong to no
// chain package - one carried inside the bundle, one the engine expects beside it - and both
// were missing from the listing while the reader had them all along.
func TestInspectBundleListsEveryPayload(t *testing.T) {
	out := capture(t, func() error { return inspectBundle(bundleFixture(t)) })

	// One line per thing the bundle references, whatever section it belongs to.
	for _, name := range []string{
		"fakeba.exe",                      // bootstrapper
		"BootstrapperApplicationData.xml", // bootstrapper, contributed by WiX
		"fixture.msi",                     // chained installer
		"sidecar.txt",                     // supplementary
		"external.exe",                    // chained installer, not carried
		"layout.txt",                      // bundle-level, carried
		"beside.txt",                      // bundle-level, NOT carried
	} {
		if !strings.Contains(out, name) {
			t.Errorf("/INSPECT does not mention %q\n%s", name, out)
		}
	}

	// The section that was missing entirely, and what makes it comprehensible.
	if !strings.Contains(out, "Bundle payloads") {
		t.Error("/INSPECT has no section for payloads outside the chain")
	}
	if !strings.Contains(out, "layout only") {
		t.Error("/INSPECT does not say a layout-only payload is never installed")
	}

	// A payload with no digest must say so rather than show a blank column.
	if !strings.Contains(out, "beside.txt is not carried in the bundle") {
		t.Errorf("/INSPECT does not warn about the payload it could not hash\n%s", out)
	}
}

// /SBOM warns in the terminal about every payload it could not hash. Warning only for chain
// packages left a bundle-level payload with no digest and no mention anywhere a human reads.
func TestBundleSBOMWarnsAboutEveryUnhashablePayload(t *testing.T) {
	// Work on a copy: /SBOM writes a document beside the artifact.
	dir := t.TempDir()
	src, err := os.ReadFile(bundleFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "fixture.exe")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}

	out := capture(t, func() error { return runBundleSBOM(path) })

	for _, want := range []string{
		"beside.txt is not carried in the bundle",   // bundle-level
		"external.exe is not carried in the bundle", // chain package
	} {
		if !strings.Contains(out, want) {
			t.Errorf("/SBOM does not warn: %q\n%s", want, out)
		}
	}

	// It must not warn about payloads it DID hash, or the warnings stop being read.
	for _, quiet := range []string{"layout.txt is not carried", "fixture.msi is not carried"} {
		if strings.Contains(out, quiet) {
			t.Errorf("/SBOM warned about a payload it hashed: %q", quiet)
		}
	}

	// And the document really was written, so this is the whole path and not just printing.
	if _, err := os.Stat(path + ".cdx.json"); err != nil {
		t.Errorf("no document beside the bundle: %v", err)
	}
}

// With no child documents present there is no link, and the terminal says which package and
// why - a link that was NOT made is the thing a reader has to act on.
func TestBundleSBOMReportsWhyALinkWasNotMade(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile(bundleFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "fixture.exe")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}

	out := capture(t, func() error { return runBundleSBOM(path) })
	if !strings.Contains(out, "No link:") {
		t.Errorf("/SBOM does not say a link was not made\n%s", out)
	}
	if !strings.Contains(out, "no document at fixture.msi.cdx.json") {
		t.Errorf("/SBOM does not name the document it looked for\n%s", out)
	}
}
