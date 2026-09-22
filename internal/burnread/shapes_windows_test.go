//go:build windows

package burnread

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The two fixtures this file reads. Both are real bundles WiX 6 built, shrunk like fixture.exe
// (see testdata/README.md); neither is ever run.
const (
	unsignedFixture = "testdata/unsigned.exe" // fixture.wxs built by WiX 6, as it came out of wix build
	signedFixture   = "testdata/signed.exe"   // that same build after detach / signtool / reattach / signtool
	shapesFixture   = "testdata/shapes.exe"   // shapes.wxs: four containers, one detached, a remote payload
)

func sha256Of(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sectionOf(t *testing.T, path string) (*sectionHeader, *pe.File, []byte) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pf, err := pe.NewFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	sec := pf.Section(".wixburn")
	if sec == nil {
		t.Fatalf("%s has no .wixburn section", path)
	}
	h, err := parseSection(raw[sec.Offset:sec.Offset+sec.Size], len(raw))
	if err != nil {
		t.Fatal(err)
	}
	return h, pf, raw
}

// --- 1. a genuinely signed bundle ----------------------------------------------------------

// signed.exe is fixture.wxs built again and then signed the supported way: `wix burn detach`,
// signtool on the engine, `wix burn reattach`, signtool on the whole file - with a self-signed
// certificate made for the purpose and discarded afterwards. The synthetic test in
// signed_windows_test.go inserts a stand-in blob where the engine's signature goes; this is the
// real thing, and the reader has to find the containers where WiX's reattach step said they
// moved to.
func TestAGenuinelySignedBundleIsSigned(t *testing.T) {
	h, pf, raw := sectionOf(t, signedFixture)
	defer pf.Close()

	if !hasCertificateTable(pf) {
		t.Fatal("signed.exe carries no Authenticode certificate table; it was not signed")
	}
	// The reattach step recorded where the engine's signature sits, and that is what moves
	// the attached containers - the whole point of the layout.
	if h.OriginalSignatureOffset == 0 || h.OriginalSignatureSize == 0 {
		t.Fatalf("no original-signature record: offset %d size %d", h.OriginalSignatureOffset, h.OriginalSignatureSize)
	}
	// Both signatures name the throwaway certificate; the subject is in each PKCS#7 blob as a
	// DER string, so the plain bytes are there to find. Counted INSIDE each signature region
	// - the engine's, where the reattach record says, and the bundle's, where the PE
	// certificate table says - because one signature carries the name several times (review
	// found three), so a whole-file count of two would not show that both exist.
	engineSig := raw[h.OriginalSignatureOffset : h.OriginalSignatureOffset+h.OriginalSignatureSize]
	dd := certificateDirectory(t, raw)
	certOff := binary.LittleEndian.Uint32(raw[dd:])
	certSize := binary.LittleEndian.Uint32(raw[dd+4:])
	bundleSig := raw[certOff : certOff+certSize]
	if int64(certOff) < h.EngineSize {
		t.Errorf("the bundle's certificate table at %d overlaps the engine region ending at %d", certOff, h.EngineSize)
	}
	name := []byte("msis burnread fixture")
	if !bytes.Contains(engineSig, name) {
		t.Errorf("the engine's signature region (%d bytes at %d) does not name the signer", len(engineSig), h.OriginalSignatureOffset)
	}
	if !bytes.Contains(bundleSig, name) {
		t.Errorf("the bundle's signature region (%d bytes at %d) does not name the signer", len(bundleSig), certOff)
	}
	naive := int64(h.StubSize) + int64(h.ContainerSizes[0])
	if h.EngineSize <= naive {
		t.Errorf("EngineSize %d is not past stub+container[0] = %d; the signature moved nothing?", h.EngineSize, naive)
	}
}

// Signing changes nothing the reader reports. unsigned.exe is the very build that was then
// detached, signed, reattached and signed again into signed.exe, so the two must read to the
// same inventory down to the bundle code; only the path differs. (fixture.exe is a different
// build - WiX 7, its own bundle code, its own generated BootstrapperApplicationData.xml - so it
// is not the twin; its chain and loose payloads, which come from the same committed sources,
// are compared below.)
func TestAGenuinelySignedBundleReadsToTheSameInventory(t *testing.T) {
	unsigned, err := Read(unsignedFixture)
	if err != nil {
		t.Fatalf("reading the unsigned twin: %v", err)
	}
	signed, err := Read(signedFixture)
	if err != nil {
		t.Fatalf("reading the genuinely signed bundle: %v", err)
	}
	if unsigned.Code != signed.Code || unsigned.Code == "" {
		t.Fatalf("unsigned and signed carry different bundle codes (%q, %q); they are not the same build",
			unsigned.Code, signed.Code)
	}
	unsigned.Path, signed.Path = "", ""
	if !reflect.DeepEqual(unsigned, signed) {
		t.Errorf("signing changed what the reader reports\nunsigned: %+v\nsigned:   %+v", unsigned, signed)
	}

	// Across builds and WiX versions, the payloads that come from committed sources agree.
	plain := readFixture(t)
	plain.Path, plain.Code, plain.EngineVersion = "", "", ""
	if !reflect.DeepEqual(plain.Packages, signed.Packages) || !reflect.DeepEqual(plain.Loose, signed.Loose) {
		t.Errorf("the chain or loose payloads differ between fixture.exe and signed.exe\nfixture: %+v / %+v\nsigned:  %+v / %+v",
			plain.Packages, plain.Loose, signed.Packages, signed.Loose)
	}
	inst := signed.Packages[0].Installer()
	if inst == nil || !inst.Carried || inst.SHA256 != sha256Of(t, filepath.Join("..", "msiread", "testdata", "fixture.msi")) {
		t.Errorf("the chained installer was not read out of the signed bundle intact: %+v", inst)
	}
}

// --- 2. more than two containers ----------------------------------------------------------

// shapes.exe declares two explicit attached containers on top of WiX's default one, so the
// header lists four and the offset arithmetic for index 2 and 3 runs against a real bundle. The
// synthetic TestContainersAreLaidOutConsecutivelyAfterTheStub remains; this is the executed
// counterpart. Each carried payload's bytes must come back matching its committed source, which
// is only possible if every container was found where the header says.
func TestFourContainersAreReadAtTheirRecordedOffsets(t *testing.T) {
	h, pf, _ := sectionOf(t, shapesFixture)
	pf.Close()
	if len(h.ContainerSizes) != 4 {
		t.Fatalf("%d containers declared, want 4 (UX, default attached, Second, Third): %v",
			len(h.ContainerSizes), h.ContainerSizes)
	}
	// Distinct, consecutive, inside the file: each begins where the previous ended.
	for i := 2; i < len(h.ContainerSizes); i++ {
		if got, want := h.offset(i), h.offset(i-1)+int64(h.ContainerSizes[i-1]); got != want {
			t.Errorf("container %d begins at %d, want %d (end of container %d)", i, got, want, i-1)
		}
	}

	b, err := Read(shapesFixture)
	if err != nil {
		t.Fatalf("reading shapes.exe: %v", err)
	}
	want := map[string]struct{ source, container string }{
		"Embedded":  {filepath.Join("..", "msiread", "testdata", "fixture.msi"), ""},
		"SecondExe": {filepath.Join("testdata", "second.exe"), "Second"},
		"ThirdExe":  {filepath.Join("testdata", "third.exe"), "Third"},
	}
	containers := map[string]bool{}
	for _, p := range b.Packages {
		w, ok := want[p.ID]
		if !ok {
			continue
		}
		inst := p.Installer()
		if inst == nil {
			t.Errorf("%s: no installer payload", p.ID)
			continue
		}
		if !inst.Carried || inst.SHA256 != sha256Of(t, w.source) {
			t.Errorf("%s: carried=%v sha256=%s, want the digest of %s", p.ID, inst.Carried, inst.SHA256, w.source)
		}
		if w.container != "" && inst.Container != w.container {
			t.Errorf("%s: read from container %q, want %q", p.ID, inst.Container, w.container)
		}
		containers[inst.Container] = true
	}
	if len(containers) != 3 {
		t.Errorf("the three carried installers came from %d container(s), want 3 distinct: %v", len(containers), containers)
	}
	// A supplementary payload in the third container, beside its installer.
	for _, p := range b.Packages {
		if p.ID != "ThirdExe" {
			continue
		}
		var third *Payload
		for i := range p.Payloads {
			if p.Payloads[i].Name == "third.txt" {
				third = &p.Payloads[i]
			}
		}
		if third == nil || third.Role != RoleSupplementary || !third.Carried ||
			third.SHA256 != sha256Of(t, filepath.Join("testdata", "third.txt")) {
			t.Errorf("third.txt was not read as a carried supplementary payload of ThirdExe: %+v", third)
		}
	}
}

// --- 3. a detached container, and a remote payload -----------------------------------------

// FarExe lives in far.cab, a detached container WiX wrote beside the bundle at build time and
// that is deliberately not committed. The reader must say so - naming the container and the
// URL the engine would fetch it from - and must not fail for want of the file. The synthetic
// TestADetachedContainerIsNotReadAsAttached drives readChain with a stand-in opener; this is
// the same claim against a real bundle read from disk.
func TestADetachedContainerPayloadIsReportedNotCarried(t *testing.T) {
	if _, err := os.Stat(filepath.Join("testdata", "far.cab")); err == nil {
		t.Fatal("far.cab is beside the fixture; the point is that the reader does not need it")
	}
	b, err := Read(shapesFixture)
	if err != nil {
		t.Fatalf("a bundle with a detached container must read without it: %v", err)
	}
	var far *Package
	for i := range b.Packages {
		if b.Packages[i].ID == "FarExe" {
			far = &b.Packages[i]
		}
	}
	if far == nil {
		t.Fatal("FarExe is not in the chain")
	}
	inst := far.Installer()
	switch {
	case inst == nil:
		t.Fatal("FarExe has no installer payload")
	case inst.Carried:
		t.Error("a payload in a detached container was reported as carried")
	case inst.SHA256 != "":
		t.Errorf("a digest was reported for bytes not in the file: %s", inst.SHA256)
	case inst.RecordedSHA512 == "":
		t.Error("the digest the engine will enforce on the download was not recorded")
	}
	for _, want := range []string{"detached container far.cab", "https://example.invalid/far.cab"} {
		if !strings.Contains(inst.Unavailable, want) {
			t.Errorf("Unavailable = %q, want it to mention %q", inst.Unavailable, want)
		}
	}
	if inst.Container != "Far" {
		t.Errorf("Container = %q, want Far", inst.Container)
	}
}

// Remote has no SourceFile at all: WiX was given a DownloadUrl, a size and a SHA-512 (generated
// with `wix burn remotepayload` from testdata/remote.exe). The reader reports it not carried,
// with the URL and the recorded digest - which a consumer can check the eventual download
// against - and with no SHA-256 of its own, because there are no bytes here to hash.
func TestARemotePayloadIsReportedWithItsURLAndRecordedDigest(t *testing.T) {
	b, err := Read(shapesFixture)
	if err != nil {
		t.Fatal(err)
	}
	var remote *Payload
	for i := range b.Packages {
		if b.Packages[i].ID == "Remote" {
			remote = b.Packages[i].Installer()
		}
	}
	if remote == nil {
		t.Fatal("Remote is not in the chain, or has no installer payload")
	}
	if remote.Carried || remote.SHA256 != "" {
		t.Errorf("a remote payload was reported as carried (%v) or hashed (%q)", remote.Carried, remote.SHA256)
	}
	if remote.DownloadURL != "https://example.invalid/remote.exe" {
		t.Errorf("DownloadURL = %q", remote.DownloadURL)
	}
	src, err := os.ReadFile(filepath.Join("testdata", "remote.exe"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha512.Sum512(src)
	if !strings.EqualFold(remote.RecordedSHA512, hex.EncodeToString(sum[:])) {
		t.Errorf("RecordedSHA512 = %s, want the SHA-512 of testdata/remote.exe", remote.RecordedSHA512)
	}
	if remote.Size != len(src) {
		t.Errorf("Size = %d, want %d", remote.Size, len(src))
	}
	if !strings.Contains(remote.Unavailable, "https://example.invalid/remote.exe") {
		t.Errorf("Unavailable = %q, want the download URL", remote.Unavailable)
	}
}

// Reading a bundle with these shapes is deterministic like reading the plain fixture.
func TestShapesReadTwiceGivesTheSameResult(t *testing.T) {
	a, err := Read(shapesFixture)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Read(shapesFixture)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Error("two reads of shapes.exe differ")
	}
}
