package burnread

import (
	"encoding/binary"
	"encoding/xml"
	"strings"
	"testing"
)

// section builds a .wixburn section body from a container list, so a test can state the layout
// it means rather than a wall of bytes.
func section(stub uint32, sizes ...uint32) []byte {
	b := make([]byte, offContainerSizes+len(sizes)*4)
	binary.LittleEndian.PutUint32(b[0:], burnMagic)
	binary.LittleEndian.PutUint32(b[offVersion:], burnVersion)
	binary.LittleEndian.PutUint32(b[offStubSize:], stub)
	binary.LittleEndian.PutUint32(b[offFormat:], 1)
	binary.LittleEndian.PutUint32(b[offContainerCount:], uint32(len(sizes)))
	for i, s := range sizes {
		binary.LittleEndian.PutUint32(b[offContainerSizes+i*4:], s)
	}
	// A recognisable GUID: {04030201-0605-0807-090A-0B0C0D0E0F10}
	for i := 0; i < 16; i++ {
		b[offBundleCode+i] = byte(i + 1)
	}
	return b
}

// Every offset in the header is load-bearing: they are the only description of where the
// containers are, and reading one wrong means inventorying arbitrary bytes as if they were the
// payload. The layout is pinned against a hand-built record so a field cannot shift silently.
func TestSectionHeaderLayout(t *testing.T) {
	h, err := parseSection(section(1000, 200, 300), 1500)
	if err != nil {
		t.Fatal(err)
	}
	if h.StubSize != 1000 {
		t.Errorf("StubSize = %d, want 1000", h.StubSize)
	}
	if h.Format != 1 {
		t.Errorf("Format = %d, want 1", h.Format)
	}
	if len(h.ContainerSizes) != 2 || h.ContainerSizes[0] != 200 || h.ContainerSizes[1] != 300 {
		t.Errorf("ContainerSizes = %v, want [200 300]", h.ContainerSizes)
	}
	// Windows writes a GUID mixed-endian on disk: the first three fields little-endian, the
	// last two as they are. Formatting all five the same way produces a GUID that looks
	// plausible and is wrong, which is the worst kind of identity bug.
	const want = "{04030201-0605-0807-090A-0B0C0D0E0F10}"
	if h.BundleCode != want {
		t.Errorf("BundleCode = %s, want %s", h.BundleCode, want)
	}
}

// Containers sit end to end after the stub, and the index is what says which is which. Getting
// the arithmetic wrong for anything past the first container would hand the cabinet extractor a
// window straddling two of them.
func TestContainersAreLaidOutConsecutivelyAfterTheStub(t *testing.T) {
	raw := make([]byte, 60)
	for i := range raw {
		raw[i] = byte(i)
	}
	h, err := parseSection(section(10, 20, 30), len(raw))
	if err != nil {
		t.Fatal(err)
	}

	first, err := h.container(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 20 || first[0] != 10 {
		t.Errorf("container 0 = %d bytes starting %d, want 20 starting 10", len(first), first[0])
	}

	second, err := h.container(raw, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 30 || second[0] != 30 {
		t.Errorf("container 1 = %d bytes starting %d, want 30 starting 30", len(second), second[0])
	}

	if _, err := h.container(raw, 2); err == nil {
		t.Error("an index past the declared containers must fail")
	}
}

// Everything in this header comes out of a file msis was merely pointed at. Each rejection is
// the difference between a clear error and a wild read, so each is exercised.
func TestTheHeaderIsNotTrusted(t *testing.T) {
	bad := func(fn func([]byte)) []byte {
		b := section(1000, 200)
		fn(b)
		return b
	}

	cases := []struct {
		name    string
		data    []byte
		total   int
		mustSay string
	}{
		{
			name:    "too short to hold a header at all",
			data:    make([]byte, 20),
			total:   1000,
			mustSay: "too short",
		},
		{
			name:    "not a Burn section",
			data:    bad(func(b []byte) { binary.LittleEndian.PutUint32(b[0:], 0xdeadbeef) }),
			total:   2000,
			mustSay: "not the Burn magic",
		},
		{
			// A future format would put different things at these offsets. Reading it
			// anyway produces an inventory of the wrong bytes, which is worse than a
			// refusal because it looks like an answer.
			name:    "a format version msis does not know",
			data:    bad(func(b []byte) { binary.LittleEndian.PutUint32(b[offVersion:], 99) }),
			total:   2000,
			mustSay: "format version 99",
		},
		{
			name:    "no containers, so no manifest",
			data:    bad(func(b []byte) { binary.LittleEndian.PutUint32(b[offContainerCount:], 0) }),
			total:   2000,
			mustSay: "no containers",
		},
		{
			// The count sizes a loop, so it is bounded before it is used.
			name:    "an absurd container count",
			data:    bad(func(b []byte) { binary.LittleEndian.PutUint32(b[offContainerCount:], 1<<30) }),
			total:   2000,
			mustSay: "not credible",
		},
		{
			name:    "more containers than the section has sizes for",
			data:    bad(func(b []byte) { binary.LittleEndian.PutUint32(b[offContainerCount:], 8) }),
			total:   2000,
			mustSay: "bytes of header",
		},
		{
			name:    "containers running past the end of the file",
			data:    section(1000, 200),
			total:   1100,
			mustSay: "truncated",
		},
		{
			// A single container larger than the file.
			name:    "one container larger than the whole file",
			data:    section(0, 0xFFFFFFFF),
			total:   4096,
			mustSay: "truncated",
		},
		{
			// The offset arithmetic must not wrap. Here the first container is tiny, so the
			// attached one is checked at offset 2 with size 4 GB - 1: in int64 that ends
			// past the file and is refused, but computed in uint32 it wraps to 1, lands
			// comfortably "inside" a 4 kB file, and is then sliced out of it.
			name:    "an attached container whose end overflows a 32-bit offset",
			data:    section(0, 2, 0xFFFFFFFF),
			total:   4096,
			mustSay: "truncated",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseSection(tc.data, tc.total)
			if err == nil {
				t.Fatal("this header must be rejected")
			}
			if !strings.Contains(err.Error(), tc.mustSay) {
				t.Errorf("error = %v, want it to mention %q", err, tc.mustSay)
			}
		})
	}
}

// A signed bundle carries its Authenticode signature after the containers, so the containers
// legitimately end before the file does. Requiring an exact match would reject every signed
// bundle - which is every bundle that ships.
func TestASignedBundleHasBytesAfterItsContainers(t *testing.T) {
	if _, err := parseSection(section(1000, 200), 1500); err != nil {
		t.Errorf("a file larger than stub+containers must be accepted: %v", err)
	}
}

const uxManifest = `<?xml version="1.0" encoding="utf-8"?>
<BurnManifest EngineVersion="7.0.0.0" xmlns="http://wixtoolset.org/schemas/v4/2008/Burn">
  <UX PrimaryPayloadId="BA">
    <Payload Id="BA" FilePath="ba.exe" SourcePath="u0" />
    <Payload Id="Theme" FilePath="thm.xml" SourcePath="u1" />
  </UX>
  <Registration Code="{11111111-1111-4111-8111-111111111111}" Version="2.0.0"
                Scope="perMachine" PrimaryUpgradeCode="{22222222-2222-4222-8222-222222222222}">
    <Arp DisplayName="Example" DisplayVersion="2.0.0" Publisher="Someone" />
  </Registration>
</BurnManifest>`

// The manifest is matched on local names so that a Burn schema revision changing only the
// namespace URI still reads. This pins that, and the identity fields with it - they go straight
// into the document's metadata, so reading one from the wrong attribute mislabels the artifact.
func TestManifestIdentityIsReadWhateverTheNamespace(t *testing.T) {
	for _, ns := range []string{
		"http://wixtoolset.org/schemas/v4/2008/Burn",
		"http://wixtoolset.org/schemas/v9/9999/Burn",
	} {
		var m manifest
		doc := strings.Replace(uxManifest, "http://wixtoolset.org/schemas/v4/2008/Burn", ns, 1)
		if err := xml.Unmarshal([]byte(doc), &m); err != nil {
			t.Fatalf("%s: %v", ns, err)
		}
		if m.EngineVersion != "7.0.0.0" {
			t.Errorf("%s: EngineVersion = %q", ns, m.EngineVersion)
		}
		if m.Registration.Arp.DisplayName != "Example" {
			t.Errorf("%s: DisplayName = %q", ns, m.Registration.Arp.DisplayName)
		}
		if m.Registration.Version != "2.0.0" {
			t.Errorf("%s: Version = %q", ns, m.Registration.Version)
		}
		if m.Registration.PrimaryUpgradeCode != "{22222222-2222-4222-8222-222222222222}" {
			t.Errorf("%s: PrimaryUpgradeCode = %q", ns, m.Registration.PrimaryUpgradeCode)
		}
		if len(m.UX.Payloads) != 2 {
			t.Errorf("%s: %d UX payloads, want 2", ns, len(m.UX.Payloads))
		}
	}
}

// A UX payload the manifest lists but the container does not hold is a hole in the inventory.
// Skipping it would produce a document that looks complete and is not, which is the failure
// this whole package is written against.
func TestAMissingBootstrapperPayloadIsRefused(t *testing.T) {
	var m manifest
	if err := xml.Unmarshal([]byte(uxManifest), &m); err != nil {
		t.Fatal(err)
	}

	b := &Bundle{}
	err := b.readUX(&m, map[string][]byte{"u0": []byte("ba")})
	if err == nil {
		t.Fatal("a payload the container does not hold must be refused, not skipped")
	}
	if !strings.Contains(err.Error(), "thm.xml") {
		t.Errorf("error = %v, want it to name the missing payload", err)
	}
}

// The digest the bundle records is what the engine enforces; here it is an independent check
// that the extracted bytes are the ones the bundle was built from. Accepting a mismatch would
// put a confidently wrong digest in a document whose purpose is verification.
func TestExtractedBytesAreCheckedAgainstTheRecordedDigest(t *testing.T) {
	// A well-formed SHA-512 that is deliberately NOT the digest of the bytes below. Any
	// wrong digest exercises the check; a pinned real one would only test that two calls to
	// crypto/sha512 agree.
	const wrongSHA512 = "5b3e54b7c1c04b5b0b51d70fb4c3f5b0b30bcd0f7c1c4b90d6e5c1fa7b36e1d0" +
		"24c3f1c0f8bd6f9e6d4a8b2f0e7c1a3d5b9f2e8c4a6d0b3f7e1c5a9d2b6f0e4c8"

	var m manifest
	doc := strings.Replace(uxManifest,
		`<Payload Id="BA" FilePath="ba.exe" SourcePath="u0" />`,
		`<Payload Id="BA" FilePath="ba.exe" SourcePath="u0" Hash="`+wrongSHA512+`" />`, 1)
	doc = strings.Replace(doc, `<Payload Id="Theme" FilePath="thm.xml" SourcePath="u1" />`, "", 1)
	if err := xml.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatal(err)
	}

	b := &Bundle{}
	err := b.readUX(&m, map[string][]byte{"u0": []byte("not the bytes that were hashed")})
	if err == nil {
		t.Fatal("bytes that do not match the recorded digest must be refused")
	}
	for _, want := range []string{"ba.exe", "do not match", "corrupt or has been modified"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}

	// ...and the matching case must still pass, or the check is just a refusal.
	b2 := &Bundle{}
	var m2 manifest
	plain := strings.Replace(uxManifest,
		`<Payload Id="Theme" FilePath="thm.xml" SourcePath="u1" />`, "", 1)
	if err := xml.Unmarshal([]byte(plain), &m2); err != nil {
		t.Fatal(err)
	}
	if err := b2.readUX(&m2, map[string][]byte{"u0": []byte("ba")}); err != nil {
		t.Fatalf("a payload with no recorded digest must still be read: %v", err)
	}
	if len(b2.UX) != 1 || b2.UX[0].SHA256 == "" || !b2.UX[0].Carried {
		t.Errorf("UX = %+v, want one carried payload with a digest", b2.UX)
	}
}

// A payload the bundle does not carry has to say which of the two reasons applies: the engine
// downloads it, or it is expected beside the installer. They lead to different actions, so
// "not carried" on its own is not enough.
func TestNotCarriedSaysWhichKindOfAbsence(t *testing.T) {
	remote := notCarried(&manifestPayload{
		FilePath: "vcredist.exe", DownloadURL: "https://example.invalid/vc.exe",
	}, nil)
	if !strings.Contains(remote, "downloads it from https://example.invalid/vc.exe") {
		t.Errorf("a remote payload must name its URL; got %q", remote)
	}

	beside := notCarried(&manifestPayload{FilePath: "extra.msi", Packaging: "external"}, nil)
	switch {
	case !strings.Contains(beside, "beside the installer"):
		t.Errorf("an external payload must say where the engine looks; got %q", beside)
	case strings.Contains(beside, "downloads"):
		t.Errorf("an external payload has no download; got %q", beside)
	}
}

// GUIDs are compared, never string-compared: the section writes braces and upper case, and a
// manifest need not. A spelling difference is not an identity difference, and treating it as
// one would reject every bundle.
func TestGUIDComparisonIgnoresSpelling(t *testing.T) {
	const a = "{11111111-1111-4111-8111-111111111111}"
	if !sameGUID(a, strings.ToLower(strings.Trim(a, "{}"))) {
		t.Error("braces and case must not make two spellings of one GUID differ")
	}
	if sameGUID(a, "{11111111-1111-4111-8111-111111111112}") {
		t.Error("genuinely different GUIDs must not compare equal")
	}
}
