// Package filekind states what BSI TR-03183-2 v2.1.0 §5.2.2 asks of every component - whether it
// is executable, an archive, and structured - from the bytes msis already reads (#63).
//
// Every answer is a fact about the bytes or it is not given. A property is left unset rather
// than guessed: §3.2.1 lets information that is not available be omitted, and a wrong "no
// archive" would hide a self-extracting installer from anyone deciding what to dissect.
package filekind

import (
	"bytes"
	"debug/pe"
	"path/filepath"
	"strings"
)

// Tri is a property that may be unknown.
type Tri int

const (
	Unknown Tri = iota
	Yes
	No
)

// Kind is the three BSI properties of one file.
type Kind struct {
	Executable Tri
	Archive    Tri
	Structured Tri
}

// archiveMagic are signatures of structured archives: containers whose members can still be
// identified afterwards (BSI §8.1.6).
var archiveMagic = [][]byte{
	[]byte("MSCF"),                             // Microsoft cabinet
	[]byte("PK\x03\x04"),                       // zip, and everything built on it (nupkg, jar, appx)
	[]byte("7z\xBC\xAF\x27\x1C"),               // 7-Zip
	[]byte("\x1F\x8B"),                         // gzip
	[]byte("Rar!\x1A\x07"),                     // RAR
	[]byte("\xFD7zXZ\x00"),                     // xz
	[]byte("\x28\xB5\x2F\xFD"),                 // zstd
	[]byte("BZh"),                              // bzip2
	[]byte("\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1"), // OLE compound file: an archive only by extension, below
}

// scripts are file types Windows or a runtime executes although they are not PE images; the
// extension is what makes them run, so it is the fact to go by (BSI §8.1.4: interpreted code).
var scripts = map[string]bool{
	".bat": true, ".cmd": true, ".ps1": true, ".psm1": true, ".vbs": true, ".vbe": true,
	".js": true, ".mjs": true, ".cjs": true, ".jse": true, ".wsf": true, ".py": true,
	".sh": true, ".jar": true,
}

// dataExtensions are formats that are data by definition - text, markup, configuration,
// localisation - with nothing in them to execute or dissect. Only a file positively identified
// as data is stated to be non-executable, no archive and unstructured; a file msis does not
// recognise gets no properties at all (#63's review: a guessed "no" is a wrong fact).
var dataExtensions = map[string]bool{
	".txt": true, ".md": true, ".rtf": true, ".xml": true, ".xsd": true, ".json": true,
	".ini": true, ".config": true, ".yaml": true, ".yml": true, ".csv": true, ".reg": true,
	".wxs": true, ".wxl": true, ".wxi": true, ".msis": true, ".gitkeep": true,
}

// imageMagic are image formats: data, never executed or dissected.
var imageMagic = [][]byte{
	[]byte("BM"),                // BMP
	[]byte("\x89PNG\r\n\x1a\n"), // PNG
	[]byte("\x00\x00\x01\x00"),  // ICO
	[]byte("GIF8"),              // GIF
	[]byte("\xFF\xD8\xFF"),      // JPEG
}

// installerDatabases are the OLE compound files that are installer packages: MSI, merge
// module, patch, transform. Other OLE files (an old .doc) are not archives of components.
var installerDatabases = map[string]bool{".msi": true, ".msm": true, ".msp": true, ".mst": true}

// Of classifies one file by its name and bytes.
func Of(name string, data []byte) Kind {
	ext := strings.ToLower(filepath.Ext(name))

	if k, ok := portableExecutable(data); ok {
		return k
	}
	for _, m := range archiveMagic {
		if !bytes.HasPrefix(data, m) {
			continue
		}
		if m[0] == 0xD0 && !installerDatabases[ext] {
			return Kind{Executable: No, Archive: Unknown, Structured: Unknown}
		}
		// A jar is a zip the JVM runs.
		exe := No
		if scripts[ext] {
			exe = Yes
		}
		return Kind{Executable: exe, Archive: Yes, Structured: Yes}
	}
	// A tar archive's signature is not at the start but at offset 257.
	if len(data) >= 262 && string(data[257:262]) == "ustar" {
		return Kind{Executable: No, Archive: Yes, Structured: Yes}
	}
	// Java bytecode, run by the JVM.
	if bytes.HasPrefix(data, []byte("\xCA\xFE\xBA\xBE")) && ext == ".class" {
		return Kind{Executable: Yes, Archive: No, Structured: No}
	}
	if scripts[ext] {
		return Kind{Executable: Yes, Archive: No, Structured: No}
	}
	isImage := false
	for _, m := range imageMagic {
		if bytes.HasPrefix(data, m) {
			isImage = true
		}
	}
	// An empty file holds nothing to execute or dissect; a positively identified data format
	// neither. Anything else is a format msis does not recognise, and is left unknown.
	if len(data) == 0 || isImage || dataExtensions[ext] || strings.HasPrefix(filepath.Base(name), ".git") {
		return Kind{Executable: No, Archive: No, Structured: No}
	}
	return Kind{}
}

// portableExecutable recognises a PE image. It is executable; whether it is also an archive
// depends on what follows its sections. A Burn bundle carries its containers in a `.wixburn`
// section and is a structured archive. A PE whose file ends where its sections (and any
// Authenticode signature) end carries nothing else, so it is no archive: statically linked
// code is unstructured (BSI §8.1.6). Anything appended beyond that may be a self-extracting
// archive msis does not recognise, and is left unknown.
func portableExecutable(data []byte) (Kind, bool) {
	if !bytes.HasPrefix(data, []byte("MZ")) {
		return Kind{}, false
	}
	f, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return Kind{}, false
	}
	defer f.Close()

	exe := Kind{Executable: Yes, Archive: Unknown, Structured: Unknown}
	sectionsEnd := int64(0)
	for _, s := range f.Sections {
		if s.Name == ".wixburn" {
			return Kind{Executable: Yes, Archive: Yes, Structured: Yes}, true
		}
		if e := int64(s.Offset) + int64(s.Size); e > sectionsEnd {
			sectionsEnd = e
		}
	}
	end := sectionsEnd
	// The Authenticode signature is appended after the sections; its FILE offset and size are
	// in the security data directory (entry 4), not in a section. It must follow the sections
	// directly (up to the 8-byte alignment): bytes between the sections and the signature are
	// an overlay - a self-extracting payload signed along with its stub - and the signature
	// ending at the end of the file does not explain them.
	if sec, ok := securityDirectory(f); ok && sec.Size > 0 {
		start := int64(sec.VirtualAddress)
		if start < sectionsEnd || start > align8(sectionsEnd) {
			return exe, true
		}
		end = start + int64(sec.Size)
	}
	if int64(len(data)) <= align8(end) {
		exe.Archive, exe.Structured = No, No
	}
	return exe, true
}

func align8(n int64) int64 { return (n + 7) &^ 7 }

// securityDirectory returns data directory entry 4. The count comes from the file and may
// exceed the 16 entries the header can hold; Go's parser copies only those that fit, so the
// count is bounded here rather than trusted (#63's review: a crafted count must not panic).
func securityDirectory(f *pe.File) (pe.DataDirectory, bool) {
	var dirs []pe.DataDirectory
	var n uint32
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		dirs, n = oh.DataDirectory[:], oh.NumberOfRvaAndSizes
	case *pe.OptionalHeader64:
		dirs, n = oh.DataDirectory[:], oh.NumberOfRvaAndSizes
	}
	if n > uint32(len(dirs)) {
		n = uint32(len(dirs))
	}
	if n <= pe.IMAGE_DIRECTORY_ENTRY_SECURITY {
		return pe.DataDirectory{}, false
	}
	return dirs[pe.IMAGE_DIRECTORY_ENTRY_SECURITY], true
}

// Value gives a property in the words BSI §5.2.2 prescribes, or "" when it is unknown.
func (t Tri) value(yes, no string) string {
	switch t {
	case Yes:
		return yes
	case No:
		return no
	}
	return ""
}

func (k Kind) ExecutableValue() string { return k.Executable.value("executable", "non-executable") }
func (k Kind) ArchiveValue() string    { return k.Archive.value("archive", "no archive") }
func (k Kind) StructuredValue() string { return k.Structured.value("structured", "unstructured") }
