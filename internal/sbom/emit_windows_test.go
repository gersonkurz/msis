//go:build windows

package sbom

import (
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gersonkurz/msis/internal/msiread"
	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

// The fixture built for #31: a package carrying a merge module's contribution, a level-zero
// feature, a SourceDir subdirectory and a Binary stream. It is committed, so these run in a
// clean checkout with no WiX toolchain.
func fixtureMSI() string {
	return filepath.Join("..", "msiread", "testdata", "fixture.msi")
}

// TestRealPackageProducesAConformingDocument is the end-to-end check: a real artifact, read by
// the real reader, emitted and then put through the conformance package every later emitter
// will use. Expected is supplied independently, so the document is judged against what the
// artifact actually contains rather than against itself.
func TestRealPackageProducesAConformingDocument(t *testing.T) {
	pkg, err := msiread.Read(fixtureMSI())
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}

	doc, err := FromPackage(pkg, Options{MsisVersion: "test"})
	if err != nil {
		t.Fatalf("FromPackage: %v", err)
	}
	data, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	// What the artifact is known to hold, named here rather than read back out of the
	// document - the point of Expected is that it is an independent input.
	want := conformance.Expected{
		PayloadNames:         []string{"payload.txt", "nested.txt", "hidden.txt", "merged.txt"},
		IdentifiedComponents: []string{doc.Metadata.Component.BOMRef},
	}
	if problems := conformance.Check(data, want); len(problems) > 0 {
		for _, p := range problems {
			t.Errorf("conformance: %v", p)
		}
	}

	// The merge module's file is the one that proves reading the artifact beats reading the
	// script: nothing in a build-time view would have produced it.
	var merged bool
	for _, c := range doc.Components {
		if c.Name == "merged.txt" {
			merged = true
		}
	}
	if !merged {
		t.Error("the merge module's contribution is missing from the document")
	}
}

// #63: BSI TR-03183-2 v2.1.0 §5.2.2 on a real package. The subject - the MSI itself - carries its
// SHA-512 (computed here independently) and is a structured, non-executable archive; every
// payload file carries a SHA-512 beside its SHA-256, its filename, and what kind of file it is.
func TestARealPackageCarriesBSIsFileFacts(t *testing.T) {
	pkg, err := msiread.Read(fixtureMSI())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := FromPackage(pkg, Options{MsisVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fixtureMSI())
	if err != nil {
		t.Fatal(err)
	}
	sum := sha512.Sum512(data)
	root := doc.Metadata.Component
	var subject512 string
	for _, h := range root.Hashes {
		if h.Alg == "SHA-512" {
			subject512 = h.Content
		}
	}
	if subject512 != hex.EncodeToString(sum[:]) {
		t.Errorf("subject SHA-512 %q, want the fixture's %x", subject512, sum)
	}
	for name, want := range map[string]string{
		propBSIFilename: "fixture.msi", propBSIExecutable: "non-executable",
		propBSIArchive: "archive", propBSIStructured: "structured",
	} {
		if got := propertyValue(root.Properties, name); got != want {
			t.Errorf("subject %s = %q, want %q", name, got, want)
		}
	}

	payloads := 0
	for _, c := range doc.Components {
		if propertyValue(c.Properties, propRole) != rolePayload {
			continue
		}
		payloads++
		var n512 int
		for _, h := range c.Hashes {
			if h.Alg == "SHA-512" && len(h.Content) == 128 {
				n512++
			}
		}
		if n512 != 1 {
			t.Errorf("%s: %d SHA-512 digests, want 1", c.Name, n512)
		}
		for name, want := range map[string]string{
			propBSIFilename: c.Name, propBSIExecutable: "non-executable",
			propBSIArchive: "no archive", propBSIStructured: "unstructured",
		} {
			if got := propertyValue(c.Properties, name); got != want {
				t.Errorf("%s: %s = %q, want %q (it is a text file)", c.Name, name, got, want)
			}
		}
	}
	if payloads == 0 {
		t.Fatal("the fixture's payload files are missing")
	}
}

// The subject artifact's digest is what lets a BOM-Link be checked against the package it
// claims to describe, rather than trusting a matching name and version.
func TestDocumentCarriesTheSubjectDigest(t *testing.T) {
	pkg, err := msiread.Read(fixtureMSI())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := FromPackage(pkg, Options{MsisVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}

	onDisk, err := hashFile(fixtureMSI())
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, h := range doc.Metadata.Component.Hashes {
		if h.Alg == "SHA-256" {
			found = h.Content
		}
	}
	if found != onDisk {
		t.Errorf("subject digest = %s, want the artifact's own %s", found, onDisk)
	}
}

// Sidecars are named after the FULL artifact filename. Since #28 a BUILD_TARGET is a name
// pattern, so an auto-bundle's MSI and EXE deliberately share a stem - a stem-based sidecar
// would give both one path and the second would overwrite the first.
func TestSidecarNamingSurvivesASharedStem(t *testing.T) {
	msi := SidecarPath(`C:\dist\App-1.0.0.msi`)
	exe := SidecarPath(`C:\dist\App-1.0.0.exe`)
	if msi == exe {
		t.Fatalf("the MSI and the bundle would share one sidecar: %s", msi)
	}
	if !strings.HasSuffix(msi, `App-1.0.0.msi.cdx.json`) {
		t.Errorf("sidecar = %q, want it named after the whole artifact", msi)
	}
}

// Retention: a document a parent already references cannot simply be replaced. Reissuing the
// parent does not repair a parent a customer already holds, so the earlier document stays
// resolvable under its own serial.
func TestReissuePreservesTheEarlierDocument(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "App.msi")
	if err := os.WriteFile(artifact, []byte("pretend installer"), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg := syntheticPackage()
	pkg.Path = artifact

	first, err := FromPackage(pkg, Options{
		MsisVersion: "1.0",
		NewSerial:   func() (string, error) { return "urn:uuid:aaaaaaaa-1111-4111-8111-111111111111", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	path, preserved, err := Write(artifact, first)
	if err != nil {
		t.Fatal(err)
	}
	if preserved != "" {
		t.Errorf("the first write preserved %q; there was nothing to preserve", preserved)
	}

	second, err := FromPackage(pkg, Options{
		MsisVersion: "1.0",
		NewSerial:   func() (string, error) { return "urn:uuid:bbbbbbbb-2222-4222-8222-222222222222", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, preserved, err = Write(artifact, second)
	if err != nil {
		t.Fatal(err)
	}
	if preserved == "" {
		t.Fatal("the earlier document was replaced; a parent referencing its serial would dangle")
	}

	// Both must resolve, each to its own serial.
	if got := serialOf(t, path); got != second.SerialNumber {
		t.Errorf("the sidecar holds serial %s, want the new one", got)
	}
	if got := serialOf(t, preserved); got != first.SerialNumber {
		t.Errorf("the preserved copy holds serial %s, want the original %s", got, first.SerialNumber)
	}

	// Writing the same document again preserves nothing: only a change of serial matters.
	if _, again, err := Write(artifact, second); err != nil || again != "" {
		t.Errorf("rewriting an identical document preserved %q (err %v)", again, err)
	}
}

// A failed write must not leave a half-written sidecar looking current.
func TestWriteIsAtomic(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "App.msi")
	if err := os.WriteFile(artifact, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := syntheticPackage()
	pkg.Path = artifact
	doc, err := FromPackage(pkg, Options{MsisVersion: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Write(artifact, doc); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SidecarPath(artifact) + ".tmp"); err == nil {
		t.Error("the temporary file survived a successful write")
	}
}

func TestCanonicalForDiffRemovesExactlyTwoFields(t *testing.T) {
	pkg := syntheticPackage()
	doc, err := FromPackage(pkg, Options{
		MsisVersion: "1.0",
		Now:         func() time.Time { return time.Unix(1000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	canon, err := CanonicalForDiff(data)
	if err != nil {
		t.Fatal(err)
	}

	var before, after map[string]any
	json.Unmarshal(data, &before)
	json.Unmarshal(canon, &after)

	if _, ok := after["serialNumber"]; ok {
		t.Error("serialNumber survived the canonical form")
	}
	if md, ok := after["metadata"].(map[string]any); ok {
		if _, ok := md["timestamp"]; ok {
			t.Error("metadata.timestamp survived the canonical form")
		}
	}
	// Everything else must still be there - the canonical form is for diffing, not redaction.
	for key := range before {
		if key == "serialNumber" {
			continue
		}
		if _, ok := after[key]; !ok {
			t.Errorf("the canonical form dropped %q, which is not one of the two varying fields", key)
		}
	}
}

// Against a released package too, not only the fixture: it is an order of magnitude larger, has
// a real product identity, and carries WiX's own injected binaries.
func TestReleasedPackageProducesAConformingDocument(t *testing.T) {
	matches, _ := filepath.Glob(filepath.Join("..", "..", "bootstrap", "dist", "*-x64.msi"))
	if len(matches) == 0 {
		t.Skip("no released .msi in bootstrap/dist; run `just release-all` to produce one")
	}

	pkg, err := msiread.Read(matches[0])
	if err != nil {
		t.Fatalf("reading %s: %v", matches[0], err)
	}
	doc, err := FromPackage(pkg, Options{MsisVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	want := conformance.Expected{
		PayloadNames:         []string{"msis.exe", "msi-simplica.dll"},
		IdentifiedComponents: []string{doc.Metadata.Component.BOMRef},
	}
	for _, p := range conformance.Check(data, want) {
		t.Errorf("conformance: %v", p)
	}
	t.Logf("%s: %d components", filepath.Base(matches[0]), len(doc.Components))
}

func serialOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var d struct {
		SerialNumber string `json:"serialNumber"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return d.SerialNumber
}
