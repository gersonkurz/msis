//go:build windows

package cabinet

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

// testdata/fixture.cab is the cabinet lifted out of the MSI fixture in internal/msiread: 277
// bytes, LZX-compressed, four small files. Committed so this package tests on its own rather
// than importing msiread, which imports this one.
func fixtureCab(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "fixture.cab"))
	if err != nil {
		t.Fatalf("reading the cabinet fixture: %v", err)
	}
	return data
}

func TestExtractReadsEveryEntry(t *testing.T) {
	files, err := Extract(fixtureCab(t))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no entries extracted")
	}

	// The cabinet carries the fixture's payload under the File table's keys, and the bytes
	// must match the sources it was built from - a decompressor returning plausible rubbish
	// would pass every other check here.
	sources := map[string]string{
		"F_Main":   filepath.Join("..", "msiread", "testdata", "payload.txt"),
		"F_Nested": filepath.Join("..", "msiread", "testdata", "nested.txt"),
		"F_Hidden": filepath.Join("..", "msiread", "testdata", "hidden.txt"),
	}
	checked := 0
	for key, src := range sources {
		got, ok := files[key]
		if !ok {
			t.Errorf("%s is missing from the cabinet", key)
			continue
		}
		want, err := os.ReadFile(src)
		if err != nil {
			t.Logf("skipping %s: %v", key, err)
			continue
		}
		if sha256.Sum256(got) != sha256.Sum256(want) {
			t.Errorf("%s: extracted bytes do not match %s\n  got  %s\n  want %s",
				key, src, digest(got), digest(want))
			continue
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("nothing could be compared against its source; the check proved nothing")
	}
}

func TestExtractRejectsWhatIsNotACabinet(t *testing.T) {
	if _, err := Extract([]byte("not a cabinet")); err == nil {
		t.Error("arbitrary bytes must not extract")
	}
	if _, err := Extract([]byte("MSCFbut only the signature")); err == nil {
		t.Error("a truncated cabinet must not extract")
	}
}

// FDINOTIFICATION's layout. Hand-written padding hard-codes the amd64 answer; on 386 a pointer
// aligns to 4, so psz1 belongs at offset 4 and an explicit pad displaces every field after it -
// including hf, which decides which extracted file the bytes belong to.
//
// Checked by arithmetic, so it holds on whichever architecture runs it.
func TestNotificationLayoutMatchesThePlatform(t *testing.T) {
	var n fdiNotification
	ptr := unsafe.Sizeof(uintptr(0))

	if got, want := unsafe.Offsetof(n.psz1), ptr; got != want {
		t.Errorf("psz1 at offset %d, want %d (one pointer in, after the 4-byte cb)", got, want)
	}
	for i, f := range []struct {
		name string
		off  uintptr
	}{
		{"psz2", unsafe.Offsetof(n.psz2)},
		{"psz3", unsafe.Offsetof(n.psz3)},
		{"pv", unsafe.Offsetof(n.pv)},
		{"hf", unsafe.Offsetof(n.hf)},
	} {
		if want := ptr * uintptr(i+2); f.off != want {
			t.Errorf("%s at offset %d, want %d", f.name, f.off, want)
		}
	}
}

// A spanned cabinet must abort, not spin. Returning 0 from fdintNEXT_CABINET tells FDI to retry,
// and the open callback would hand back the same bytes for ever - holding the extraction lock
// with it.
func TestSpannedCabinetAborts(t *testing.T) {
	extractMu.Lock()
	defer extractMu.Unlock()
	spanned = false
	defer func() { spanned = false }()

	if got := handleNotify(fdintNEXT_CABINET, &fdiNotification{}); got != ^uintptr(0) {
		t.Errorf("fdintNEXT_CABINET returned %d, want -1 so FDI stops retrying", int(got))
	}
	if !spanned {
		t.Error("the spanned case was not recorded, so the error would not say why")
	}
	if got := handleNotify(fdintCABINET_INFO, &fdiNotification{}); got != 0 {
		t.Errorf("fdintCABINET_INFO returned %d, want 0", int(got))
	}
}

// Extraction must leave the handle registry as it found it. Cabinet handles carry the whole
// compressed buffer, so retaining them grows memory with every package inspected.
func TestExtractionLeavesNoHandlesBehind(t *testing.T) {
	before := registrySize()

	if _, err := Extract(fixtureCab(t)); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if after := registrySize(); after != before {
		t.Errorf("after a successful extraction the registry holds %d handles, was %d", after, before)
	}

	if _, err := Extract([]byte("MSCFnot really a cabinet at all")); err == nil {
		t.Fatal("garbage must not extract successfully")
	}
	if after := registrySize(); after != before {
		t.Errorf("after a failed extraction the registry holds %d handles, was %d", after, before)
	}
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}
