package filekind

import (
	"archive/zip"
	"bytes"
	"debug/pe"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func want(t *testing.T, what string, got Kind, exe, archive, structured string) {
	t.Helper()
	if got.ExecutableValue() != exe || got.ArchiveValue() != archive || got.StructuredValue() != structured {
		t.Errorf("%s: %q/%q/%q, want %q/%q/%q", what,
			got.ExecutableValue(), got.ArchiveValue(), got.StructuredValue(), exe, archive, structured)
	}
}

// A real PE image - this test binary - carries nothing after its sections: executable, no
// archive, unstructured. The same bytes with data appended may be a self-extracting archive
// msis does not recognise, so archive and structured are left unknown rather than guessed.
func TestAPEIsAnArchiveOnlyWhenThatIsProvable(t *testing.T) {
	data := selfPE(t)
	want(t, "the test binary", Of("x.exe", data), "executable", "no archive", "unstructured")
	want(t, "the test binary with data appended", Of("x.exe", append(append([]byte{}, data...), make([]byte, 4096)...)),
		"executable", "", "")
}

// An Authenticode-signed PE ends with its certificate table, not with its last section; the
// signature must not be mistaken for an appended archive.
func TestASignedPEIsNotMistakenForAnArchive(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("looks for a signed binary in System32")
	}
	candidates, _ := filepath.Glob(filepath.Join(os.Getenv("SystemRoot"), "System32", "*.exe"))
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil || !embeddedSignature(data) {
			continue
		}
		want(t, filepath.Base(path), Of(filepath.Base(path), data), "executable", "no archive", "unstructured")
		return
	}
	t.Skip("no binary with an embedded Authenticode signature in System32")
}

// #63's review: a self-extracting executable laid out as sections | payload | signature ends
// with its signature, and the signature ending at the end of the file must not hide the payload
// before it. Both layouts are built from a real PE - 64-bit (this test binary) and 32-bit (a
// SysWOW64 executable) - by rewriting the security directory.
func TestAnOverlayBeforeTheSignatureIsNotHidden(t *testing.T) {
	for _, img := range peImages(t) {
		signed := withSignature(t, img.data, 0)
		want(t, img.name+" signed directly after its sections", Of("x.exe", signed), "executable", "no archive", "unstructured")
		overlay := withSignature(t, img.data, 4096)
		want(t, img.name+" with a payload before its signature", Of("x.exe", overlay), "executable", "", "")
	}
}

// #63's review: NumberOfRvaAndSizes comes from the file. Go's parser accepts more than the 16
// entries the header holds when the optional-header size matches, and copies only 16; the
// count must be bounded, not used to slice, or one crafted payload aborts the inspection.
func TestACraftedDirectoryCountDoesNotPanic(t *testing.T) {
	for _, img := range peImages(t) {
		crafted := withDirectoryCount(t, img.data, 17)
		if _, err := pe.NewFile(bytes.NewReader(crafted)); err != nil {
			t.Fatalf("%s: the crafted image does not parse, so it would not reach the bound: %v", img.name, err)
		}
		want(t, img.name+" declaring 17 data directories", Of("x.exe", crafted), "executable", "no archive", "unstructured")
	}
}

// A Burn bundle - here Microsoft's VC++ redistributable, where the prerequisite cache holds one -
// is an executable that is also a structured archive: its containers sit in a .wixburn section.
func TestABurnBundleIsAStructuredArchive(t *testing.T) {
	bundles, _ := filepath.Glob(filepath.Join(os.Getenv("LOCALAPPDATA"), "msis", "prerequisites", "vcredist", "*", "vc_redist.*.exe"))
	if len(bundles) == 0 {
		t.Skip("no cached VC++ redistributable to read; `msis /BUILD` with <requires type=\"vcredist\"> caches one")
	}
	data, err := os.ReadFile(bundles[0])
	if err != nil {
		t.Fatal(err)
	}
	want(t, filepath.Base(bundles[0]), Of(filepath.Base(bundles[0]), data), "executable", "archive", "structured")
}

func TestArchivesScriptsAndData(t *testing.T) {
	var zipped bytes.Buffer
	w := zip.NewWriter(&zipped)
	if _, err := w.Create("a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	ole := []byte("\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1rest-of-a-compound-file")

	want(t, "a zip", Of("a.zip", zipped.Bytes()), "non-executable", "archive", "structured")
	want(t, "a jar (a zip the JVM runs)", Of("a.jar", zipped.Bytes()), "executable", "archive", "structured")
	want(t, "a cabinet", Of("a.cab", []byte("MSCF\x00\x00\x00\x00")), "non-executable", "archive", "structured")
	want(t, "a tar", Of("a.tar", append(make([]byte, 257), []byte("ustar\x0000")...)), "non-executable", "archive", "structured")
	want(t, "an MSI", Of("app.msi", ole), "non-executable", "archive", "structured")
	want(t, "an OLE file that is not an installer", Of("old.doc", ole), "non-executable", "", "")
	want(t, "a PowerShell script", Of("setup.ps1", []byte("Write-Host hi")), "executable", "no archive", "unstructured")
	want(t, "an ES module", Of("app.mjs", []byte("export {}")), "executable", "no archive", "unstructured")
	want(t, "Java bytecode", Of("A.class", []byte("\xCA\xFE\xBA\xBEjunk")), "executable", "no archive", "unstructured")
	want(t, "a localisation file", Of("en-us.wxl", []byte("<WixLocalization/>")), "non-executable", "no archive", "unstructured")
	want(t, "a PNG, whatever it is called", Of("logo.dat", []byte("\x89PNG\r\n\x1a\nrest")), "non-executable", "no archive", "unstructured")
	want(t, "an empty file", Of(".gitkeep", nil), "non-executable", "no archive", "unstructured")
}

// #63's review: a format msis does not recognise gets NO properties. A guessed "non-executable"
// or "no archive" is a wrong fact about a file that may be neither.
func TestAnUnrecognisedFormatStaysUnknown(t *testing.T) {
	want(t, "MZ without a PE header", Of("x.bin", []byte("MZ-not-really")), "", "", "")
	want(t, "an unknown binary format", Of("blob.xyz", []byte("\x01\x02\x03\x04 unknown")), "", "", "")
	want(t, "an archive format msis does not know (lzip)", Of("a.lz", []byte("LZIP\x01rest")), "", "", "")
}

// --- real PE images, and byte-level rewrites of them ----------------------------------------

type peImage struct {
	name string
	data []byte
}

func selfPE(t *testing.T) []byte {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("the test binary is a PE image only on Windows")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// peImages are a 64-bit and, where the machine has one, a 32-bit real PE.
func peImages(t *testing.T) []peImage {
	t.Helper()
	images := []peImage{{"PE32+ (the test binary)", selfPE(t)}}
	wow, _ := filepath.Glob(filepath.Join(os.Getenv("SystemRoot"), "SysWOW64", "*.exe"))
	for _, path := range wow {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		f, err := pe.NewFile(bytes.NewReader(data))
		if err != nil {
			continue
		}
		_, is32 := f.OptionalHeader.(*pe.OptionalHeader32)
		f.Close()
		if is32 {
			images = append(images, peImage{"PE32 (" + filepath.Base(path) + ")", data})
			break
		}
	}
	if len(images) < 2 {
		t.Log("no 32-bit PE in SysWOW64; checking PE32+ only")
	}
	return images
}

// layout is where the COFF header and data directory 0 sit in a PE, read from the bytes.
type layout struct {
	coff, dirs int
}

func layoutOf(data []byte) layout {
	coff := int(binary.LittleEndian.Uint32(data[0x3C:])) + 4
	opt := coff + 20
	dirs := opt + 96 // PE32
	if binary.LittleEndian.Uint16(data[opt:]) == 0x20B {
		dirs = opt + 112 // PE32+
	}
	return layout{coff: coff, dirs: dirs}
}

// sectionsEnd is where the last section's raw data ends.
func sectionsEnd(t *testing.T, data []byte) int {
	t.Helper()
	f, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	end := 0
	for _, s := range f.Sections {
		if e := int(s.Offset + s.Size); e > end {
			end = e
		}
	}
	return end
}

// stripped is the image cut at the end of its sections, its COFF symbol table unlinked (so the
// cut cannot leave the parser reading symbols past the end) and its signature removed.
func stripped(t *testing.T, data []byte) ([]byte, layout) {
	t.Helper()
	l := layoutOf(data)
	out := append([]byte{}, data[:sectionsEnd(t, data)]...)
	binary.LittleEndian.PutUint32(out[l.coff+8:], 0)  // PointerToSymbolTable
	binary.LittleEndian.PutUint32(out[l.coff+12:], 0) // NumberOfSymbols
	binary.LittleEndian.PutUint64(out[l.dirs+4*8:], 0)
	return out, l
}

// withSignature appends a 1 KiB certificate table, gap bytes after the (8-aligned) sections.
func withSignature(t *testing.T, data []byte, gap int) []byte {
	t.Helper()
	out, l := stripped(t, data)
	for len(out)%8 != 0 {
		out = append(out, 0)
	}
	out = append(out, make([]byte, gap)...)
	start := len(out)
	out = append(out, make([]byte, 1024)...)
	binary.LittleEndian.PutUint32(out[l.dirs+4*8:], uint32(start))
	binary.LittleEndian.PutUint32(out[l.dirs+4*8+4:], 1024)
	return out
}

// withDirectoryCount declares n data directories, enlarging the optional header to match (so
// Go's parser accepts it) by inserting the extra entries before the section table, and moving
// the sections' raw-data pointers with the data they point at.
func withDirectoryCount(t *testing.T, data []byte, n int) []byte {
	t.Helper()
	img, l := stripped(t, data)
	nsec := int(binary.LittleEndian.Uint16(img[l.coff+2:]))
	extra := (n - 16) * 8
	insertAt := l.dirs + 16*8
	out := append(append(append([]byte{}, img[:insertAt]...), make([]byte, extra)...), img[insertAt:]...)
	binary.LittleEndian.PutUint32(out[l.dirs-4:], uint32(n)) // NumberOfRvaAndSizes
	size := binary.LittleEndian.Uint16(out[l.coff+16:])
	binary.LittleEndian.PutUint16(out[l.coff+16:], size+uint16(extra)) // SizeOfOptionalHeader
	secTable := insertAt + extra
	for i := 0; i < nsec; i++ {
		ptr := secTable + i*40 + 20 // PointerToRawData
		if v := binary.LittleEndian.Uint32(out[ptr:]); v != 0 {
			binary.LittleEndian.PutUint32(out[ptr:], v+uint32(extra))
		}
	}
	return out
}

func embeddedSignature(data []byte) bool {
	f, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return false
	}
	defer f.Close()
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		return oh.NumberOfRvaAndSizes > 4 && oh.DataDirectory[4].Size > 0
	case *pe.OptionalHeader32:
		return oh.NumberOfRvaAndSizes > 4 && oh.DataDirectory[4].Size > 0
	}
	return false
}
