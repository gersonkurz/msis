// Package burnread reads a built Burn bundle: its identity, the payloads its bootstrapper
// application carries, and the packages it chains.
//
// It is the bundle counterpart of internal/msiread and exists for the same reason: the .msis
// script is not the best description of what shipped. A bundle is also the only artifact that
// records what its engine actually embedded - the bootstrapper application, its theme and
// localisation, and the exact bytes of every chained installer.
//
// Reading is strictly passive, as in msiread. A bundle is an executable, and the one thing this
// package must never do is run it: the layout is read out of the PE section Burn writes for the
// purpose, and the payloads out of the cabinets it appends.
package burnread

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/gersonkurz/msis/internal/cabinet"
)

// Bundle is everything read out of one bundle executable.
//
// As in msiread, the order is defined so that two reads of one bundle produce identical output:
// Packages keep the chain's own order, which is meaningful, and payloads are sorted.
type Bundle struct {
	Path string

	// Identity, read from the manifest's Registration and Arp elements. Never inferred from
	// the filename: a bundle records what it is, and guessing would put a wrong name in a
	// document whose whole purpose is to be verifiable.
	Code        string // Registration/@Code, the bundle's own identifier
	UpgradeCode string // Registration/@PrimaryUpgradeCode, stable across releases
	Name        string // Arp/@DisplayName
	Version     string // Registration/@Version
	Publisher   string // Arp/@Publisher
	Scope       string // perMachine or perUser

	EngineVersion string // the Burn engine that built it

	UX       []Payload // bootstrapper-application container payloads
	Packages []Package // the chain, in chain order

	// Loose is every payload the manifest declares that no chain package references - a
	// LayoutOnly file, say. Burn writes all non-UX payloads at bundle level, so these appear
	// nowhere else; reading only the chain's PayloadRefs dropped them without a word.
	Loose []Payload
}

// Role says what a payload is for. It is deliberately separate from Carried: a chained
// installer may be embedded or fetched at install time, and those are two different questions.
type Role string

const (
	// RoleBootstrapper: carried in the UX container and run by the engine to drive the
	// install. It is never installed onto the machine.
	RoleBootstrapper Role = "bootstrapper"
	// RoleChained: the installer of a chain package - the thing that actually installs.
	RoleChained Role = "chained-installer"
	// RoleSupplementary: an additional payload a chain package needs, beside its installer.
	RoleSupplementary Role = "supplementary"
	// RoleBundle: carried by the bundle but belonging to no chain package. A LayoutOnly
	// payload is the usual case - laid out beside the bundle rather than installed.
	RoleBundle Role = "bundle-payload"
)

// Payload is one file the bundle references.
type Payload struct {
	ID   string
	Name string // the FilePath the manifest gives it
	Size int
	Role Role

	// Carried says the bytes are inside this file and msis got them out. When false the
	// payload is required at install time but lives elsewhere - beside the bundle, or at
	// DownloadURL - so there is nothing here to hash.
	Carried bool

	SHA256 string // computed by msis from the extracted bytes; empty when not Carried

	// RecordedSHA512 is the digest the bundle itself records for the payload, which the
	// engine enforces before using it. For a carried payload it is what the extracted bytes
	// were checked against; for one that is not carried it is the only digest there is, and
	// the only thing a consumer can verify the eventual download against.
	RecordedSHA512 string

	Packaging   string // the manifest's Packaging attribute: embedded or external
	DownloadURL string
	Container   string

	// LayoutOnly marks a payload Burn lays out beside the bundle and never installs. It is a
	// separate claim from Role: an unreferenced payload need not be layout-only, and a
	// layout-only one is not installed even though it ships.
	LayoutOnly bool

	// Unavailable says why there is no digest, and is empty when there is one. As in msiread,
	// a payload without a digest has to be explicable rather than merely absent.
	Unavailable string
}

// Package is one entry of the chain.
type Package struct {
	ID          string
	Kind        string // MsiPackage, ExePackage, MspPackage, MsuPackage
	DisplayName string
	Version     string
	ProductCode string // MSI packages only
	UpgradeCode string

	InstallCondition string // when set, the package installs only where this holds
	Vital            bool
	Permanent        bool

	// Payloads in a defined order: the installer first, then any supplementary payloads
	// sorted by name.
	Payloads []Payload
}

// AllPayloads is every file the bundle references, whatever its role.
//
// It exists because the payloads live in three places - the bootstrapper's container, the
// chain, and the bundle level - and a consumer that walks only the ones it happens to know
// about produces an inventory with a hole in it. That has now happened twice, so anything
// asking "what does this bundle contain" asks here rather than remembering three fields.
func (b *Bundle) AllPayloads() []Payload {
	all := make([]Payload, 0, len(b.UX)+len(b.Loose))
	all = append(all, b.UX...)
	all = append(all, b.Loose...)
	for i := range b.Packages {
		all = append(all, b.Packages[i].Payloads...)
	}
	return all
}

// Installer is the payload that is the package itself, or nil when the manifest named none.
func (p *Package) Installer() *Payload {
	for i := range p.Payloads {
		if p.Payloads[i].Role == RoleChained {
			return &p.Payloads[i]
		}
	}
	return nil
}

// Read opens a bundle and reads everything this package understands.
func Read(path string) (*Bundle, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return read(path, raw)
}

// read is the whole of Read once the bytes are in hand, so the layout and manifest handling can
// be exercised without a file on disk.
func read(path string, raw []byte) (*Bundle, error) {
	section, signed, err := wixburnSection(raw)
	if err != nil {
		return nil, err
	}
	hdr, err := parseSection(section, len(raw))
	if err != nil {
		return nil, err
	}

	// Burn has a third way of locating the attached containers, for an engine that is signed
	// but whose containers have not been attached yet. A finished bundle in that state is
	// one WiX cannot produce and Burn cannot run: signing a bundle means detaching the
	// engine, signing it, reattaching - which records OriginalSignatureOffset - and only
	// then signing the whole file. Refusing is the honest answer; guessing an offset would
	// surface later as "this file is corrupt", which would be a true symptom and a false
	// diagnosis.
	if signed && hdr.OriginalSignatureOffset == 0 && len(hdr.ContainerSizes) > 1 {
		return nil, fmt.Errorf(
			"this bundle carries an Authenticode signature but records no original engine "+
				"signature, so msis cannot say where its %d attached containers begin"+
				"\n  a bundle is signed by detaching the engine, signing it, reattaching it "+
				"(which records that offset) and then signing the bundle; one signed without "+
				"that step cannot be read - by msis or by Burn",
			len(hdr.ContainerSizes)-1)
	}

	ux, err := hdr.container(raw, 0)
	if err != nil {
		return nil, fmt.Errorf("locating the bootstrapper-application container: %w", err)
	}
	uxFiles, err := cabinet.Extract(ux)
	if err != nil {
		return nil, fmt.Errorf("extracting the bootstrapper-application container: %w", err)
	}
	manifestXML, ok := uxFiles[manifestEntry]
	if !ok {
		return nil, fmt.Errorf(
			"the bootstrapper-application container holds no entry %q, so the bundle manifest "+
				"cannot be read", manifestEntry)
	}

	var m manifest
	if err := xml.Unmarshal(manifestXML, &m); err != nil {
		return nil, fmt.Errorf("parsing the bundle manifest: %w", err)
	}

	b := &Bundle{
		Path:          path,
		Code:          m.Registration.Code,
		UpgradeCode:   m.Registration.PrimaryUpgradeCode,
		Name:          m.Registration.Arp.DisplayName,
		Version:       m.Registration.Version,
		Publisher:     m.Registration.Arp.Publisher,
		Scope:         m.Registration.Scope,
		EngineVersion: m.EngineVersion,
	}

	// The section header's GUID is the bundle code, written independently of the manifest.
	// Two records of one identity that disagree mean the file is not what it says it is.
	if !sameGUID(hdr.BundleCode, b.Code) {
		return nil, fmt.Errorf(
			"the bundle's PE section records code %s but its manifest records %s; the file is "+
				"inconsistent and was not read further", hdr.BundleCode, b.Code)
	}

	if err := b.readUX(&m, uxFiles); err != nil {
		return nil, err
	}
	if err := b.readChain(&m, attachedContainers(&m, hdr, raw)); err != nil {
		return nil, err
	}
	return b, nil
}

// manifestEntry is the name Burn gives the manifest inside the UX container. The entries are
// numbered rather than named - "0" is the manifest and "u0", "u1", ... are the bootstrapper's
// own files - so the manifest is found by this name and not by position: a cabinet is extracted
// into a map, and depending on entry order would make the result depend on something the format
// does not promise.
const manifestEntry = "0"

func (b *Bundle) readUX(m *manifest, uxFiles map[string][]byte) error {
	for _, p := range m.UX.Payloads {
		pay := Payload{
			ID:             p.ID,
			Name:           p.FilePath,
			Role:           RoleBootstrapper,
			Packaging:      p.Packaging,
			RecordedSHA512: p.Hash,
		}
		data, ok := uxFiles[p.SourcePath]
		if !ok {
			return fmt.Errorf(
				"the manifest lists bootstrapper payload %q at %q, which is not in the "+
					"container", p.FilePath, p.SourcePath)
		}
		if err := pay.take(data, p.Hash); err != nil {
			return fmt.Errorf("bootstrapper payload %q: %w", p.FilePath, err)
		}
		b.UX = append(b.UX, pay)
	}
	sortPayloads(b.UX)
	return nil
}

// openContainer yields the files of an attached container by manifest id. It is a parameter of
// readChain rather than something readChain builds, because everything else about the chain -
// which payload is the installer, which container a payload names, what happens when it is not
// carried - is decidable from the manifest alone, and injecting this is what lets those be
// tested without a PE and a cabinet.
type openContainer func(id string) (map[string][]byte, error)

// attachedContainers opens attached containers out of the bundle's own bytes, extracting each
// at most once: a bundle chaining several runtimes holds tens of megabytes, and every package
// usually lives in the same container.
func attachedContainers(m *manifest, hdr *sectionHeader, raw []byte) openContainer {
	extracted := map[string]map[string][]byte{}
	return func(id string) (map[string][]byte, error) {
		if f, ok := extracted[id]; ok {
			return f, nil
		}
		c := m.container(id)
		if c == nil {
			return nil, fmt.Errorf("the manifest references container %q but does not declare it", id)
		}
		data, err := hdr.container(raw, c.AttachedIndex)
		if err != nil {
			return nil, fmt.Errorf("locating container %q: %w", id, err)
		}
		// The container's own recorded hash is checked before anything is read out of it.
		// Being within the file's bounds only proves the offsets are plausible; this proves
		// they are right, and that the bytes are the ones the bundle was built from.
		if err := verify(data, c.Hash); err != nil {
			return nil, fmt.Errorf("container %q: %w", id, err)
		}
		files, err := cabinet.Extract(data)
		if err != nil {
			return nil, fmt.Errorf("extracting container %q: %w", id, err)
		}
		extracted[id] = files
		return files, nil
	}
}

func (b *Bundle) readChain(m *manifest, containerFiles openContainer) error {
	byID := map[string]*manifestPayload{}
	for i := range m.Payloads {
		byID[m.Payloads[i].ID] = &m.Payloads[i]
	}
	// Which payloads a chain package claims. Whatever is left over at the end is carried by
	// the bundle without belonging to any package, and has to be inventoried too.
	claimed := map[string]bool{}

	for _, entry := range m.Chain.Entries {
		if !strings.HasSuffix(entry.XMLName.Local, "Package") {
			continue
		}
		pkg := Package{
			ID:               entry.ID,
			Kind:             entry.XMLName.Local,
			Version:          entry.Version,
			ProductCode:      entry.ProductCode,
			UpgradeCode:      entry.UpgradeCode,
			InstallCondition: entry.InstallCondition,
			Vital:            entry.Vital == "yes",
			Permanent:        entry.Permanent == "yes",
			DisplayName:      entry.DisplayName,
		}
		if pkg.DisplayName == "" && len(entry.Provides) > 0 {
			pkg.DisplayName = entry.Provides[0].DisplayName
		}

		// The package's own installer is the FIRST PayloadRef. That is the format's rule,
		// not a heuristic: WiX writes the package payload's reference first and Burn takes
		// payload[0] as the package file. Matching on "the payload whose id equals the
		// package id" is usually the same thing and is wrong when it is not - a
		// supplementary payload that happens to carry the package's id would be handed the
		// installer's role, and with it the digest and the child-document lookup.
		var supplementary []Payload
		for i, ref := range entry.PayloadRefs {
			mp := byID[ref.ID]
			if mp == nil {
				return fmt.Errorf("package %q references payload %q, which the manifest does "+
					"not declare", entry.ID, ref.ID)
			}
			claimed[ref.ID] = true

			pay := Payload{
				ID:             mp.ID,
				Name:           mp.FilePath,
				Size:           mp.FileSize,
				Role:           RoleSupplementary,
				Packaging:      mp.Packaging,
				RecordedSHA512: mp.Hash,
				DownloadURL:    mp.DownloadURL,
				Container:      mp.Container,
				LayoutOnly:     mp.LayoutOnly == "yes",
			}
			if i == 0 {
				pay.Role = RoleChained
			}

			if err := b.fill(&pay, m, mp, containerFiles); err != nil {
				return err
			}

			if pay.Role == RoleChained {
				pkg.Payloads = append(pkg.Payloads, pay)
			} else {
				supplementary = append(supplementary, pay)
			}
		}
		sortPayloads(supplementary)
		pkg.Payloads = append(pkg.Payloads, supplementary...)
		b.Packages = append(b.Packages, pkg)
	}

	// Anything the manifest declares that no chain package referenced. Burn writes every
	// non-UX payload at bundle level, so a payload belonging to no package - a LayoutOnly
	// file, say - appears here and nowhere else. Processing only PayloadRefs dropped it
	// silently, which is exactly the shape of incompleteness this package exists to prevent.
	for i := range m.Payloads {
		mp := &m.Payloads[i]
		if claimed[mp.ID] {
			continue
		}
		pay := Payload{
			ID:             mp.ID,
			Name:           mp.FilePath,
			Size:           mp.FileSize,
			Role:           RoleBundle,
			Packaging:      mp.Packaging,
			RecordedSHA512: mp.Hash,
			DownloadURL:    mp.DownloadURL,
			Container:      mp.Container,
			LayoutOnly:     mp.LayoutOnly == "yes",
		}
		if err := b.fill(&pay, m, mp, containerFiles); err != nil {
			return err
		}
		b.Loose = append(b.Loose, pay)
	}
	sortPayloads(b.Loose)
	return nil
}

// fill gets a payload's bytes where the bundle carries them, and says why not where it does
// not. It is shared by the chain and the bundle-level payloads so that the two cannot come to
// disagree about what "carried" means.
func (b *Bundle) fill(pay *Payload, m *manifest, mp *manifestPayload,
	containerFiles openContainer) error {

	if mp.Container == "" {
		// Required at install time, but its bytes are not in this file. msis reports what
		// it is and where the bundle expects to get it, and does not go and fetch it -
		// reading an artifact must not reach the network.
		pay.Unavailable = notCarried(mp, nil)
		return nil
	}

	c := m.container(mp.Container)
	if c == nil {
		return fmt.Errorf("payload %q names container %q, which the manifest does not declare",
			mp.FilePath, mp.Container)
	}
	// A detached container is a separate file beside the bundle or at a URL; it has no
	// attached index, and reading one as if it had would take index 0 - the bootstrapper's
	// own container - and check it against this container's digest.
	if !c.attached() {
		pay.Unavailable = notCarried(mp, c)
		return nil
	}

	files, err := containerFiles(mp.Container)
	if err != nil {
		return err
	}
	data, ok := files[mp.SourcePath]
	if !ok {
		return fmt.Errorf("the manifest places payload %q at %q in container %q, where it is not",
			mp.FilePath, mp.SourcePath, mp.Container)
	}
	if err := pay.take(data, mp.Hash); err != nil {
		return fmt.Errorf("payload %q: %w", mp.FilePath, err)
	}
	return nil
}

// notCarried explains an absent payload precisely enough to act on. c is the detached container
// holding it, where one does.
func notCarried(mp *manifestPayload, c *manifestContainer) string {
	if c != nil {
		where := "a separate file the engine expects beside the bundle"
		if c.DownloadURL != "" {
			where = "downloaded from " + c.DownloadURL + " at install time"
		}
		name := c.FilePath
		if name == "" {
			name = c.ID
		}
		return fmt.Sprintf(
			"%s is not carried in the bundle: it lives in detached container %s, %s, so it "+
				"is not in this file to hash", mp.FilePath, name, where)
	}
	if mp.DownloadURL != "" {
		return fmt.Sprintf(
			"%s is not carried in the bundle: the engine downloads it from %s at install "+
				"time, so msis cannot hash the bytes that will actually be used",
			mp.FilePath, mp.DownloadURL)
	}
	return fmt.Sprintf(
		"%s is not carried in the bundle: it is an external payload the engine expects to "+
			"find beside the installer, so it is not in this file to hash", mp.FilePath)
}

// take records the bytes of a carried payload, checking them against what the manifest recorded.
func (p *Payload) take(data []byte, want string) error {
	if err := verify(data, want); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	p.SHA256 = hex.EncodeToString(sum[:])
	p.Size = len(data)
	p.Carried = true
	return nil
}

// verify checks extracted bytes against the SHA-512 Burn recorded for them.
//
// The engine records a digest for every payload and container so that it can detect a corrupt
// download; here it serves as an independent check that msis extracted the right bytes. A
// mismatch is an error rather than a note: an SBOM exists to be verified against an
// installation, and inventorying bytes that are not the ones the bundle was built from would
// produce a document that is confidently wrong.
func verify(data []byte, want string) error {
	if want == "" {
		return nil
	}
	sum := sha512.Sum512(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf(
			"the bytes read do not match the SHA-512 the bundle records for them "+
				"(recorded %s..., read %s...); the file is corrupt or has been modified",
			strings.ToLower(first(want, 16)), first(got, 16))
	}
	return nil
}

func first(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}

func sortPayloads(p []Payload) {
	sort.Slice(p, func(i, j int) bool {
		if p[i].Name != p[j].Name {
			return p[i].Name < p[j].Name
		}
		return p[i].ID < p[j].ID
	})
}

// sameGUID compares two GUID spellings. Braces are decoration and case is not significant.
func sameGUID(a, b string) bool {
	norm := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.TrimPrefix(s, "{")
		s = strings.TrimSuffix(s, "}")
		return strings.ToLower(s)
	}
	return norm(a) == norm(b)
}

// --- the .wixburn section -----------------------------------------------------------------

// sectionHeader is BURN_SECTION_HEADER, the fixed record Burn writes into the .wixburn section
// so that the engine can find its own containers at runtime. It is the only description of the
// layout: everything after the stub is opaque appended data with no self-describing framing.
type sectionHeader struct {
	BundleCode string
	StubSize   uint32
	Format     uint32

	// OriginalSignature* record where the ENGINE's own Authenticode signature was before the
	// containers were appended. WiX's reattach step writes them; they are zero for an
	// unsigned engine. They matter because they move everything after the stub - see
	// EngineSize.
	OriginalSignatureOffset uint32
	OriginalSignatureSize   uint32

	// ContainerSizes are in attached-index order, starting with the bootstrapper-application
	// container at index 0.
	ContainerSizes []uint32

	// EngineSize is where the ATTACHED containers begin - those at index 1 and above. It is
	// NOT simply stub + container[0]: when the engine was signed before the containers were
	// attached, its signature sits between them, and the attached containers start after it.
	// Burn computes exactly this (src/burn/engine/section.cpp), and a bundle signed the
	// supported way - detach the engine, sign it, reattach, sign the bundle - takes the
	// first branch. Assuming stub + container[0] reads a signed bundle at the wrong offset
	// and then reports it as corrupt.
	EngineSize int64
}

const (
	// burnMagic and burnVersion identify the record. Reading a section that is not this
	// would be reading arbitrary bytes as offsets.
	burnMagic   = 0x00f14300
	burnVersion = 0x00000002

	// Field offsets: magic, version, the bundle GUID, then the DWORDs that precede the
	// container count.
	offVersion           = 4
	offBundleCode        = 8
	offStubSize          = 24
	offOriginalSigOffset = 32
	offOriginalSigSize   = 36
	offFormat            = 40
	offContainerCount    = 44
	offContainerSizes    = 48
)

// maxContainers bounds the count before it is used to size anything. The field comes out of a
// file msis was merely pointed at, and four billion is not a plausible bundle.
const maxContainers = 1024

func wixburnSection(raw []byte) (data []byte, signed bool, err error) {
	f, err := pe.NewFile(bytes.NewReader(raw))
	if err != nil {
		return nil, false, fmt.Errorf("reading the executable: %w", err)
	}
	defer f.Close()

	s := f.Section(".wixburn")
	if s == nil {
		return nil, false, fmt.Errorf(
			"this executable has no .wixburn section, so it is not a Burn bundle" +
				"\n  hint: a bundle is what msis produces for a package with <requires>, or " +
				"what WiX builds from a <Bundle> element")
	}
	data, err = s.Data()
	if err != nil {
		return nil, false, fmt.Errorf("reading the .wixburn section: %w", err)
	}
	return data, hasCertificateTable(f), nil
}

// certificateTable is the data directory that holds an Authenticode signature. Unlike every
// other directory entry its VirtualAddress is a raw FILE offset, which is why Burn can use it
// to work out where the signed part of the file ends.
const certificateTable = 4

func hasCertificateTable(f *pe.File) bool {
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		return len(h.DataDirectory) > certificateTable &&
			h.DataDirectory[certificateTable].Size != 0
	case *pe.OptionalHeader64:
		return len(h.DataDirectory) > certificateTable &&
			h.DataDirectory[certificateTable].Size != 0
	}
	return false
}

// parseSection decodes the header and checks that the layout it describes fits the file. total
// is the bundle's size on disk.
func parseSection(data []byte, total int) (*sectionHeader, error) {
	if len(data) < offContainerSizes {
		return nil, fmt.Errorf("the .wixburn section is %d bytes, too short to hold a header",
			len(data))
	}
	u32 := func(off int) uint32 { return binary.LittleEndian.Uint32(data[off:]) }

	if magic := u32(0); magic != burnMagic {
		return nil, fmt.Errorf("the .wixburn section begins %#x, not the Burn magic %#x",
			magic, burnMagic)
	}
	if v := u32(offVersion); v != burnVersion {
		return nil, fmt.Errorf(
			"the bundle uses .wixburn format version %d; msis reads version %d, and guessing "+
				"at an unknown layout would produce an inventory of the wrong bytes",
			v, burnVersion)
	}

	count := u32(offContainerCount)
	if count == 0 {
		return nil, fmt.Errorf("the bundle declares no containers, so it carries no manifest")
	}
	if count > maxContainers {
		return nil, fmt.Errorf("the bundle declares %d containers, which is not credible", count)
	}
	need := offContainerSizes + int(count)*4
	if len(data) < need {
		return nil, fmt.Errorf(
			"the bundle declares %d containers, which needs %d bytes of header; the .wixburn "+
				"section is %d", count, need, len(data))
	}

	h := &sectionHeader{
		BundleCode:              guidAt(data[offBundleCode : offBundleCode+16]),
		StubSize:                u32(offStubSize),
		Format:                  u32(offFormat),
		OriginalSignatureOffset: u32(offOriginalSigOffset),
		OriginalSignatureSize:   u32(offOriginalSigSize),
	}
	for i := uint32(0); i < count; i++ {
		h.ContainerSizes = append(h.ContainerSizes, u32(offContainerSizes+int(i)*4))
	}

	// Offsets are computed in int64 throughout, so sizes summing past 2^32 are caught here
	// rather than wrapping into an offset that looks reasonable.
	h.EngineSize = int64(h.StubSize) + int64(h.ContainerSizes[0])
	if h.OriginalSignatureOffset != 0 {
		h.EngineSize = int64(h.OriginalSignatureOffset) + int64(h.OriginalSignatureSize)
	}

	// Every container must lie inside the file. A bundle signed after its containers were
	// attached carries that signature at the end, so the containers may finish before the
	// file does - but never after it.
	for i := range h.ContainerSizes {
		end := h.offset(i) + int64(h.ContainerSizes[i])
		if end > int64(total) {
			return nil, fmt.Errorf(
				"the bundle's header puts container %d at %d..%d, but the file is %d bytes; "+
					"it is truncated or not the file the header was written for",
				i, h.offset(i), end, total)
		}
	}
	return h, nil
}

// offset is where a container begins in the file.
//
// The bootstrapper's container sits immediately after the stub; the attached containers begin
// at EngineSize and follow one another. This mirrors Burn's own SectionGetAttachedContainerInfo
// - the two differ whenever the engine carried a signature of its own, and a reader that
// assumed one layout for both would read a signed bundle at the wrong place.
func (h *sectionHeader) offset(index int) int64 {
	if index <= 0 {
		return int64(h.StubSize)
	}
	off := h.EngineSize
	for i := 1; i < index; i++ {
		off += int64(h.ContainerSizes[i])
	}
	return off
}

// container returns the bytes of the container at an attached index.
func (h *sectionHeader) container(raw []byte, index int) ([]byte, error) {
	if index < 0 || index >= len(h.ContainerSizes) {
		return nil, fmt.Errorf("container index %d, but the bundle declares %d",
			index, len(h.ContainerSizes))
	}
	start := h.offset(index)
	end := start + int64(h.ContainerSizes[index])
	// parseSection already proved every container fits; this proves it again for the one
	// slice about to be taken, so a change there cannot turn into an out-of-range read here.
	if start < 0 || end > int64(len(raw)) {
		return nil, fmt.Errorf("container %d lies at %d..%d, outside a file of %d bytes",
			index, start, end, len(raw))
	}
	return raw[start:end], nil
}

// guidAt formats a GUID in the mixed-endian layout Windows uses on disk.
func guidAt(b []byte) string {
	return fmt.Sprintf("{%08X-%04X-%04X-%04X-%012X}",
		binary.LittleEndian.Uint32(b[0:4]),
		binary.LittleEndian.Uint16(b[4:6]),
		binary.LittleEndian.Uint16(b[6:8]),
		binary.BigEndian.Uint16(b[8:10]),
		b[10:16])
}

// --- the manifest -------------------------------------------------------------------------

// The manifest elements msis reads. Namespaces are left off deliberately: encoding/xml then
// matches on local name whatever namespace the element carries, so a Burn schema revision that
// changes only the namespace URI still reads.
type manifest struct {
	EngineVersion string `xml:"EngineVersion,attr"`

	UX struct {
		PrimaryPayloadID string            `xml:"PrimaryPayloadId,attr"`
		Payloads         []manifestPayload `xml:"Payload"`
	} `xml:"UX"`

	Containers []manifestContainer `xml:"Container"`
	Payloads   []manifestPayload   `xml:"Payload"`

	Registration struct {
		Code               string `xml:"Code,attr"`
		Version            string `xml:"Version,attr"`
		Scope              string `xml:"Scope,attr"`
		PrimaryUpgradeCode string `xml:"PrimaryUpgradeCode,attr"`
		Arp                struct {
			DisplayName    string `xml:"DisplayName,attr"`
			DisplayVersion string `xml:"DisplayVersion,attr"`
			Publisher      string `xml:"Publisher,attr"`
		} `xml:"Arp"`
	} `xml:"Registration"`

	Chain struct {
		Entries []chainEntry `xml:",any"`
	} `xml:"Chain"`
}

type manifestContainer struct {
	ID            string `xml:"Id,attr"`
	AttachedIndex int    `xml:"AttachedIndex,attr"`
	Attached      string `xml:"Attached,attr"`
	Hash          string `xml:"Hash,attr"`
	FileSize      int    `xml:"FileSize,attr"`
	FilePath      string `xml:"FilePath,attr"`
	DownloadURL   string `xml:"DownloadUrl,attr"`
}

// attached reports whether the container is inside the bundle. A DETACHED container omits
// AttachedIndex entirely, which unmarshals to 0 - the bootstrapper's own container - so the
// index must never be used without asking this first.
func (c *manifestContainer) attached() bool { return c.Attached == "yes" }

type manifestPayload struct {
	ID          string `xml:"Id,attr"`
	FilePath    string `xml:"FilePath,attr"`
	FileSize    int    `xml:"FileSize,attr"`
	Hash        string `xml:"Hash,attr"`
	Packaging   string `xml:"Packaging,attr"`
	SourcePath  string `xml:"SourcePath,attr"`
	Container   string `xml:"Container,attr"`
	DownloadURL string `xml:"DownloadUrl,attr"`
	LayoutOnly  string `xml:"LayoutOnly,attr"`
}

type chainEntry struct {
	XMLName xml.Name

	ID               string `xml:"Id,attr"`
	DisplayName      string `xml:"DisplayName,attr"`
	Version          string `xml:"Version,attr"`
	ProductCode      string `xml:"ProductCode,attr"`
	UpgradeCode      string `xml:"UpgradeCode,attr"`
	InstallCondition string `xml:"InstallCondition,attr"`
	Vital            string `xml:"Vital,attr"`
	Permanent        string `xml:"Permanent,attr"`

	Provides []struct {
		Key         string `xml:"Key,attr"`
		DisplayName string `xml:"DisplayName,attr"`
	} `xml:"Provides"`

	PayloadRefs []struct {
		ID string `xml:"Id,attr"`
	} `xml:"PayloadRef"`
}

func (m *manifest) container(id string) *manifestContainer {
	for i := range m.Containers {
		if m.Containers[i].ID == id {
			return &m.Containers[i]
		}
	}
	return nil
}
