//go:build windows

package burnread

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"os"
	"reflect"
	"strings"
	"testing"
)

// reattachedFixture rebuilds the fixture in the layout a SIGNED bundle has.
//
// Signing a bundle means detaching the engine, signing it, reattaching it and then signing the
// whole file. After the reattach the engine's own signature sits between the bootstrapper's
// container and the attached containers, and the header records where it was - which is the
// only thing that says the attached containers have moved.
//
// This is synthesised rather than produced by signtool because signing needs a certificate,
// but nothing about it is approximated: the bytes really are inserted, the header really does
// describe the new layout, and the reader has to find the containers where the header says.
func reattachedFixture(t *testing.T, raw []byte, pad int) []byte {
	t.Helper()

	pf, err := pe.NewFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer pf.Close()
	sec := pf.Section(".wixburn")
	if sec == nil {
		t.Fatal("the fixture has no .wixburn section")
	}
	h, err := parseSection(raw[sec.Offset:sec.Offset+sec.Size], len(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(h.ContainerSizes) < 2 {
		t.Skip("the fixture has no attached container to move")
	}
	uxEnd := int(h.StubSize) + int(h.ContainerSizes[0])
	if int(sec.Offset)+offContainerSizes > uxEnd {
		t.Fatalf("the .wixburn section at %d is not inside the stub, so patching it would "+
			"move the containers too", sec.Offset)
	}

	out := make([]byte, 0, len(raw)+pad)
	out = append(out, raw[:uxEnd]...)
	// A stand-in signature blob. Deliberately not zeros: if the reader looks in the old
	// place it gets this, and a cabinet extractor fed 0xAB fails loudly rather than quietly.
	out = append(out, bytes.Repeat([]byte{0xAB}, pad)...)
	out = append(out, raw[uxEnd:]...)

	binary.LittleEndian.PutUint32(out[int(sec.Offset)+offOriginalSigOffset:], uint32(uxEnd))
	binary.LittleEndian.PutUint32(out[int(sec.Offset)+offOriginalSigSize:], uint32(pad))
	return out
}

// The executed regression for the signed layout: a real bundle, moved into the place a signed
// one occupies, must read to exactly the same inventory - same payloads, same digests. Before
// the fix the reader looked at stub+container[0] and found the signature blob there, which the
// container digest check would have reported as a corrupt file.
func TestASignedBundleReadsToTheSameInventory(t *testing.T) {
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}

	plain, err := read(fixture, raw)
	if err != nil {
		t.Fatalf("the unsigned fixture must read: %v", err)
	}

	for _, pad := range []int{16, 512, 4096} {
		moved := reattachedFixture(t, raw, pad)
		signed, err := read(fixture, moved)
		if err != nil {
			t.Fatalf("pad %d: a bundle whose engine carried a signature must read: %v", pad, err)
		}
		if !reflect.DeepEqual(plain, signed) {
			t.Errorf("pad %d: the signed layout produced a different inventory", pad)
		}
		// Stated positively, so the test cannot pass on two empty results.
		inst := signed.Packages[0].Installer()
		if inst == nil || inst.SHA256 == "" || inst.SHA256 != plain.Packages[0].Installer().SHA256 {
			t.Errorf("pad %d: the chained installer's digest did not survive the move", pad)
		}
	}
}

// The fix must not be achieved by ignoring the digests: a signed-layout bundle whose header
// points at the WRONG place still has to be refused. Here the recorded signature size is a
// byte short, so the attached container is read one byte off.
func TestASignedBundleWithAWrongOffsetIsStillRefused(t *testing.T) {
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	moved := reattachedFixture(t, raw, 512)

	pf, err := pe.NewFile(bytes.NewReader(moved))
	if err != nil {
		t.Fatal(err)
	}
	defer pf.Close()
	sec := pf.Section(".wixburn")
	at := int(sec.Offset) + offOriginalSigSize
	binary.LittleEndian.PutUint32(moved[at:], binary.LittleEndian.Uint32(moved[at:])-1)

	if _, err := read(fixture, moved); err == nil {
		t.Fatal("a header pointing one byte short of the container was accepted")
	}
}

// A bundle carrying an Authenticode signature but no record of the engine's own is a state
// neither WiX produces nor Burn reads. Guessing an offset for it would surface later as "this
// file is corrupt" - a true symptom and a false diagnosis - so it is named for what it is.
func TestASignatureWithNoReattachRecordIsNamed(t *testing.T) {
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}

	dd := certificateDirectory(t, raw)

	// Give the PE a non-empty certificate table without touching anything else. The size is
	// all the reader looks at.
	signed := make([]byte, len(raw))
	copy(signed, raw)
	binary.LittleEndian.PutUint32(signed[dd+4:], 8) // Size

	_, err = read(fixture, signed)
	if err == nil {
		t.Fatal("a signed bundle with no original-signature record must not be guessed at")
	}
	for _, want := range []string{"Authenticode signature", "original engine signature"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}

	// The same file WITH a reattach record reads, so the refusal is about the missing
	// record and not about the signature.
	both := reattachedFixture(t, raw, 512)
	binary.LittleEndian.PutUint32(both[dd+4:], 8)
	if _, err := read(fixture, both); err != nil {
		t.Errorf("a properly reattached signed bundle must read: %v", err)
	}
}

// certificateDirectory returns the file offset of the certificate table's entry in the PE data
// directory, so a test can give a file a non-empty one without building a real signature.
func certificateDirectory(t *testing.T, raw []byte) int {
	t.Helper()
	peOff := int(binary.LittleEndian.Uint32(raw[0x3c:]))
	optSize := int(binary.LittleEndian.Uint16(raw[peOff+20:]))
	optStart := peOff + 24

	// PE32+ puts NumberOfRvaAndSizes at 108 and the directories at 112; PE32 at 92 and 96.
	dirStart := optStart + 112
	if binary.LittleEndian.Uint16(raw[optStart:]) == 0x10b { // IMAGE_NT_OPTIONAL_HDR32_MAGIC
		dirStart = optStart + 96
	}
	if dirStart+certificateTable*8+8 > optStart+optSize {
		t.Fatal("the data directory does not fit the optional header")
	}
	return dirStart + certificateTable*8
}
