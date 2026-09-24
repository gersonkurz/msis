// Package buildrecord collects what the build knows and the artifact cannot say (#34).
//
// A built installer records its payload, but not where that payload came from, which .msis
// produced it, what toolchain built it, whether a prerequisite is carried or merely detected,
// or where a downloaded prerequisite was fetched from. All of that is known during the build
// and nowhere afterwards.
//
// The record is CONTRIBUTED TO by each build path rather than derived by one traversal of one
// generator, because the four paths resolve different things at different times: an MSI build
// resolves payload while generating, an auto-bundle resolves prerequisites after the MSI is
// already built, an explicit <bundle> resolves chained packages instead of payload, and
// /STANDALONE resolves no chain at all - its prerequisites become launch conditions, which is a
// different category from a bundled payload and is recorded as one.
//
// Two rules run through everything here:
//
//   - Nothing absolute leaves this package. A machine path, a cache path or a template path in
//     a published document leaks the build machine's layout and is useless to the reader.
//     Sources are relative to the .msis; anything outside it is named symbolically.
//   - Every hash is of the file the BUILD ACTUALLY READ, resolved through the same ordered bind
//     paths WiX uses. That is not pedantry: on a machine with a /CUSTOMTEMPLATES overlay the
//     hook DLL that executes during installation comes from the overlay, not from the template
//     folder, and hashing the wrong one would put a confidently wrong digest against executing
//     code.
package buildrecord

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Path is which of the four build paths produced a record. They are not variations of one
// thing: what each can say differs, and a consumer needs to know which it is reading.
type Path string

const (
	// PathMSI: a plain MSI. Payload, directories, features, registry, services.
	PathMSI Path = "msi"
	// PathAutoBundle: an MSI wrapped in a Burn bundle because of <requires>. Adds the
	// prerequisites and the chained MSI, resolved AFTER the MSI was built.
	PathAutoBundle Path = "auto-bundle"
	// PathBundle: an explicit <bundle>. Chained MSIs and ExePackages, no payload of its own.
	PathBundle Path = "bundle"
	// PathStandalone: /STANDALONE. No chain at all - the prerequisites become launch
	// conditions, so they are externally required runtimes rather than anything shipped.
	PathStandalone Path = "standalone"
)

// Record is everything one build knew about what it produced.
type Record struct {
	Path   Path
	Script string // the .msis, by base name - the directory is the reader's own context

	// Toolchain is what built the artifact, as opposed to what wrote the document. #29 D7
	// puts the document's producer in metadata.tools; this belongs in properties.
	Toolchain []Tool

	// SBOMCreator is who created the SBOM - an email address or a URL, from the .msis's
	// SBOM_CREATOR (#64). Empty when the script names nobody.
	SBOMCreator string
	// DataLicense is the licence the SBOM itself is offered under, an SPDX expression, from the
	// .msis's SBOM_DATA_LICENSE (#62). Empty when the script grants none.
	DataLicense string

	Files      []File
	Binaries   []Binary
	Extensions []ExtensionFile
	Prereqs    []Prerequisite
	Chained    []Chained
	Runtimes   []Runtime
	Unresolved []string // sources the build could not find; recorded, never omitted

	// scriptDir is where the .msis lives, used to relativise and never emitted.
	scriptDir string
	// bindPaths are WiX's ordered -b directories, used to resolve what the template names.
	bindPaths []BindPath
}

// Tool is one participant in the build chain.
type Tool struct {
	Name    string
	Version string
}

// File is one payload file, joined to the artifact by its WiX File id.
type File struct {
	FileID string // FILE_ID00007 - the artifact carries this as msis:msi.fileKey
	Source string // the path as authored, slash-separated
	Root   string // WHICH bind path it resolved in; see BindPath.Name
	SHA256 string // of the file the build read

	// Modified is when that file was last written, read from the file system when the build
	// read it (#63): BSI TR-03183-2 v2.1.0 §5.2.2 makes a file's modification date its version
	// when it has none. Zero when it could not be read.
	Modified time.Time
}

// Binary is a file the TEMPLATE named rather than the script - the installer-hook DLL is the
// one msis supplies - resolved through WiX's ordered bind paths.
//
// It is joined to the artifact by CONTENT, not by name: the template declares the hook DLL as
// Binary id "binary.dll" whatever the file is called, so a name-based join would either miss it
// or claim the wrong provenance. Matching on the digest states only what can be shown.
type Binary struct {
	Name   string // the file's own name, e.g. msi-simplica.dll
	Source string // relative to the bind path that matched
	Root   string // WHICH bind path matched; see BindPath.Name
	SHA256 string
}

// ExtensionFile is one file a WiX extension the build loaded carries in its embedded .wixlib
// (#67), with what that extension's package declares. An MSI embeds some of these as
// Binary-table streams, and they are joined to the artifact by CONTENT: a stream named
// WixUI_Bmp_Banner is WiX's only if its bytes are the ones this extension shipped, because a
// template can define a stream of that name itself.
type ExtensionFile struct {
	Package string // WixToolset.UI.wixext
	Version string // the package version the build loaded
	Entry   string // the file inside the .wixlib, e.g. wix-ir/bannrbmp.bmp
	SHA256  string

	// What the package's .nuspec declares, from msis's pinned table (internal/wix): the
	// cache the build loads from does not keep the .nuspec.
	Authors    string
	Repository string
	License    string // an SPDX id or ScanCode LicenseRef, as BSI §6.1 names licences
}

// Prerequisite is a runtime the build arranged for.
type Prerequisite struct {
	Type    string // vcredist, netfx
	Version string
	Arch    string

	// Carried says the installer ships the prerequisite's bytes. False means the build only
	// arranged to DETECT it - a different category, and the difference is what a consumer
	// needs in order to know whether anything was distributed.
	Carried bool

	DownloadURL string // where the build fetched it from, when it did
	CachePath   string // symbolic; see cacheRef
	Source      string // an explicit <prerequisite source=>, relative to the .msis
	Root        string // WHICH bind path that Source resolved in; see BindPath.Name
	SHA256      string

	// Verification says what the bytes were checked against before being chained (#50):
	// VerifiedByPin for a download msis pinned (D5), VerifiedByScript for a supplied source
	// whose sha256= matched, Unverified for a supplied source with no digest to check. A
	// consumer reading the SBOM needs the distinction: the first two are evidence, the
	// third is a file somebody put there.
	Verification string
}

// Verification values.
const (
	VerifiedByPin    = "pinned-digest"
	VerifiedByScript = "script-digest"
	Unverified       = "unverified"
)

// Chained is an installer the bundle runs.
type Chained struct {
	Kind   string // MsiPackage, ExePackage
	Source string // the path as authored, slash-separated
	Root   string // WHICH bind path it resolved in; see BindPath.Name
	SHA256 string
}

// Runtime is a prerequisite that /STANDALONE turned into a launch condition: required on the
// machine, shipped by nobody, detected at install time. Emitting these as though they were
// bundled payload would claim the installer distributes something it does not.
type Runtime struct {
	Type      string
	Version   string
	Condition string // the launch condition the build generated
}

// BindPath is one of WiX's ordered -b directories, with a symbolic name for the record.
//
// The name is what gets published. The directory is a machine path - "C:\Users\someone\
// AppData\Local\MSIS\custom" - and naming it in a document would leak the build machine's
// layout while telling the reader nothing they can act on.
type BindPath struct {
	Name string // "wxs", "script", "custom-templates", "templates"
	Dir  string
}

// New starts a record for one build.
func New(p Path, scriptPath string, bindPaths []BindPath) *Record {
	dir := filepath.Dir(scriptPath)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return &Record{
		Path:      p,
		Script:    filepath.Base(scriptPath),
		scriptDir: dir,
		bindPaths: bindPaths,
	}
}

// AddTool records a participant in the build chain.
func (r *Record) AddTool(name, version string) {
	if name == "" || version == "" {
		return
	}
	r.Toolchain = append(r.Toolchain, Tool{Name: name, Version: version})
}

// AddFile records one payload file the generator resolved.
//
// source is as the generator holds it: relative to the .msis directory. It is hashed where it
// lies, because that is the file WiX will read; an unreadable one is recorded as unresolved
// rather than skipped, so the document can say the build could not account for it.
func (r *Record) AddFile(fileID, source string) {
	if fileID == "" || source == "" {
		return
	}

	// An absolute source is already decided; a relative one is what WiX will resolve, and it
	// resolves through the bind paths IN ORDER, taking the first match. The generated WXS
	// directory comes before the script's, and the two are not the same directory whenever
	// BUILD_TARGET puts the output elsewhere - so resolving against the script alone can
	// hash a different file from the one that was packaged.
	if filepath.IsAbs(source) {
		sum, err := hashFile(source)
		if err != nil {
			r.Unresolved = append(r.Unresolved,
				fmt.Sprintf("%s: the build could not read %s", fileID, r.relative(source)))
			return
		}
		r.Files = append(r.Files, File{
			FileID: fileID, Source: r.relative(source), Root: "absolute", SHA256: sum,
			Modified: modified(source)})
		return
	}

	rel := filepath.FromSlash(source)
	if root, path, sum, ok := r.resolve(rel); ok {
		r.Files = append(r.Files, File{
			FileID: fileID, Source: filepath.ToSlash(rel), Root: root, SHA256: sum,
			Modified: modified(path)})
		return
	}
	r.Unresolved = append(r.Unresolved, fmt.Sprintf(
		"%s: the build could not read %s in any of its bind paths", fileID, filepath.ToSlash(rel)))
}

// resolve walks the bind paths in WiX's own order and returns the first match.
//
// The order is load-bearing, not decorative: WiX takes the first hit, so a copy earlier in the
// list is the one that gets packaged. Falling back to the script directory keeps the common
// case working when a caller supplied no bind paths at all (the unit tests, and any future
// path that has none).
func (r *Record) resolve(rel string) (root, path, sum string, ok bool) {
	for _, bp := range r.bindPaths {
		if bp.Dir == "" {
			continue
		}
		p := filepath.Join(bp.Dir, rel)
		if s, err := hashFile(p); err == nil {
			return bp.Name, p, s, true
		}
	}
	if len(r.bindPaths) == 0 {
		p := filepath.Join(r.scriptDir, rel)
		if s, err := hashFile(p); err == nil {
			return "script", p, s, true
		}
	}
	return "", "", "", false
}

// modified is a file's last-write time as an instant, or zero when it cannot be read.
func modified(p string) time.Time {
	info, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime().UTC()
}

// Locate returns the file a relative source resolves to, by the same bind-path order resolve
// uses - which is WiX's order, so the file found here is the file WiX will package. An absolute
// source is returned as it is when it exists. The bundle generators verify a supplied
// prerequisite's sha256= against this path (#50); a digest computed on any other file would be
// a false assurance.
func (r *Record) Locate(source string) (string, bool) {
	if filepath.IsAbs(source) {
		_, err := os.Stat(source)
		return source, err == nil
	}
	rel := filepath.FromSlash(source)
	for _, bp := range r.bindPaths {
		if bp.Dir == "" {
			continue
		}
		if p := filepath.Join(bp.Dir, rel); fileExists(p) {
			return p, true
		}
	}
	if len(r.bindPaths) == 0 {
		if p := filepath.Join(r.scriptDir, rel); fileExists(p) {
			return p, true
		}
	}
	return "", false
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// AddTemplateBinary resolves a file the TEMPLATE names, through WiX's bind paths in order, and
// records the one that would actually be used.
//
// The order is the whole point. On a machine carrying a /CUSTOMTEMPLATES overlay there can be
// several copies of the installer-hook DLL - the repository's, the installed template folder's
// and the overlay's - and WiX takes the first match. Resolving "the obvious one" instead
// records a digest for bytes that never ran.
//
// rel is what the template wrote, which may name a directory and a file separately: the hook
// DLL is HookDllDir() + "/" + DLL_ENTRY, and HookDllDir names a directory.
func (r *Record) AddTemplateBinary(rel string) {
	if rel == "" {
		return
	}
	rel = filepath.FromSlash(rel)
	if root, _, sum, ok := r.resolve(rel); ok {
		r.Binaries = append(r.Binaries, Binary{
			Name:   filepath.Base(rel),
			Source: filepath.ToSlash(rel),
			Root:   root,
			SHA256: sum,
		})
		return
	}
	r.Unresolved = append(r.Unresolved,
		fmt.Sprintf("%s: named by the template but found in none of the build's bind paths",
			filepath.ToSlash(rel)))
}

// AddPrerequisiteFromSource records a prerequisite the script supplied itself.
//
// There is no download and no cache entry, so neither is claimed. Reporting a Microsoft URL for
// bytes somebody authored locally would be an invented provenance - the same class of mistake
// as an invented purl (#29 D4), and worse here because a URL looks like evidence.
// The source is resolved through the bind paths in order, exactly like payload and like a
// chained installer: WiX makes no distinction, so neither can the record. A prerequisite
// shadowed by a copy in the WXS directory is packaged from THERE, and hashing the script
// directory's copy would leave the one that shipped unattributed.
//
// digest is the script's sha256= for the source, or "" when it gave none: it says whether the
// bytes were verified before being chained (#50), which is a fact about the build the artifact
// cannot carry. The recorded SHA256 stays the hash of what is actually on disk either way.
func (r *Record) AddPrerequisiteFromSource(typ, version, source, digest string) {
	p := Prerequisite{Type: typ, Version: version, Carried: true, Verification: Unverified}
	if digest != "" {
		p.Verification = VerifiedByScript
	}

	if filepath.IsAbs(source) {
		p.Source = r.relative(source)
		if sum, err := hashFile(source); err == nil {
			p.SHA256, p.Root = sum, "absolute"
		} else {
			r.Unresolved = append(r.Unresolved, fmt.Sprintf(
				"%s %s: the build could not read its source %s", typ, version, p.Source))
		}
		r.Prereqs = append(r.Prereqs, p)
		return
	}

	rel := filepath.FromSlash(source)
	p.Source = filepath.ToSlash(rel)
	if root, _, sum, ok := r.resolve(rel); ok {
		p.SHA256, p.Root = sum, root
	} else {
		r.Unresolved = append(r.Unresolved, fmt.Sprintf(
			"%s %s: the build could not read its source %s in any of its bind paths",
			typ, version, p.Source))
	}
	r.Prereqs = append(r.Prereqs, p)
}

// AddPrerequisiteFromCache records a prerequisite the build downloaded or found cached, for one
// architecture.
//
// cachePath is where it really landed, taken from the generator rather than reconstructed: a
// bundle caches every architecture it can carry, so guessing one would attribute one
// architecture's digest to another, or look for an entry that is not there.
func (r *Record) AddPrerequisiteFromCache(typ, version, arch, cachePath, url string) {
	p := Prerequisite{
		Type:         typ,
		Version:      version,
		Arch:         arch,
		Carried:      true,
		DownloadURL:  url,
		Verification: VerifiedByPin, // a cached file is one EnsurePrerequisite verified against its pin (D5)
	}
	p.CachePath, p.SHA256 = r.cacheRef(cachePath)
	if p.SHA256 == "" {
		r.Unresolved = append(r.Unresolved, fmt.Sprintf(
			"%s %s %s: the build could not read the cached file it resolved", typ, version, arch))
	}
	r.Prereqs = append(r.Prereqs, p)
}

// AddPrerequisiteUnresolved records a prerequisite the build arranged but could not locate -
// no cache was configured, and the script named no source. It is recorded rather than dropped:
// the bundle chains it either way, and a reader needs to know msis cannot say where it came
// from.
func (r *Record) AddPrerequisiteUnresolved(typ, version, why string) {
	r.Prereqs = append(r.Prereqs, Prerequisite{Type: typ, Version: version, Carried: true})
	r.Unresolved = append(r.Unresolved, fmt.Sprintf("%s %s: %s", typ, version, why))
}

// AddChained records an installer the bundle runs.
func (r *Record) AddChained(kind, source string) {
	if source == "" {
		return
	}
	// Through the bind paths, like payload: WiX resolves a relative chain source the same
	// way it resolves everything else, so hashing the script directory's copy can describe a
	// different file from the one that was packaged.
	if filepath.IsAbs(source) {
		c := Chained{Kind: kind, Source: r.relative(source)}
		if sum, err := hashFile(source); err == nil {
			c.SHA256 = sum
		} else {
			r.Unresolved = append(r.Unresolved,
				fmt.Sprintf("%s: the build could not read the chained %s", c.Source, kind))
		}
		r.Chained = append(r.Chained, c)
		return
	}

	rel := filepath.FromSlash(source)
	c := Chained{Kind: kind, Source: filepath.ToSlash(rel)}
	if root, _, sum, ok := r.resolve(rel); ok {
		c.SHA256, c.Root = sum, root
	} else {
		r.Unresolved = append(r.Unresolved, fmt.Sprintf(
			"%s: the build could not read the chained %s in any of its bind paths",
			c.Source, kind))
	}
	r.Chained = append(r.Chained, c)
}

// AddRuntime records a prerequisite /STANDALONE turned into a launch condition.
func (r *Record) AddRuntime(typ, version, condition string) {
	r.Runtimes = append(r.Runtimes, Runtime{Type: typ, Version: version, Condition: condition})
}

// Scope is which artifact a record is being used to describe. An auto-bundle produces two, and
// they do not contain the same things.
type Scope int

const (
	// ScopeArtifactMSI: the MSI itself - its payload, the streams that run during its
	// installation, and any runtime it merely detects.
	ScopeArtifactMSI Scope = iota
	// ScopeArtifactBundle: the wrapper - the prerequisites and installers IT carries.
	ScopeArtifactBundle
)

// For returns the part of the record that describes one artifact.
//
// An auto-bundle's prerequisites and its chained MSI are in the BUNDLE, not in the MSI, and
// handing one record to both documents made the MSI's claim to contain the wrapper's payload
// and to depend on itself. What is shared is what describes the BUILD rather than the output:
// which path ran, which script, which toolchain, and what could not be accounted for.
func (r *Record) For(s Scope) *Record {
	out := &Record{
		Path:        r.Path,
		Script:      r.Script,
		Toolchain:   r.Toolchain,
		SBOMCreator: r.SBOMCreator,
		DataLicense: r.DataLicense,
		Unresolved:  r.Unresolved,
		scriptDir:   r.scriptDir,
		bindPaths:   r.bindPaths,
	}
	switch s {
	case ScopeArtifactMSI:
		out.Files = r.Files
		out.Binaries = r.Binaries
		out.Extensions = r.Extensions
		out.Runtimes = r.Runtimes
	case ScopeArtifactBundle:
		out.Prereqs = r.Prereqs
		out.Chained = r.Chained
	}
	return out
}

// Sort imposes a total order, so two builds of one input produce identical records and the
// document built from them is diffable (#29 D6).
func (r *Record) Sort() {
	sort.Slice(r.Files, func(i, j int) bool { return r.Files[i].FileID < r.Files[j].FileID })
	sort.Slice(r.Binaries, func(i, j int) bool { return r.Binaries[i].Source < r.Binaries[j].Source })
	sort.Slice(r.Extensions, func(i, j int) bool {
		a, b := r.Extensions[i], r.Extensions[j]
		return a.SHA256+"\x00"+a.Package+"\x00"+a.Entry < b.SHA256+"\x00"+b.Package+"\x00"+b.Entry
	})
	sort.Slice(r.Prereqs, func(i, j int) bool { return r.Prereqs[i].Key() < r.Prereqs[j].Key() })
	sort.Slice(r.Chained, func(i, j int) bool { return r.Chained[i].Source < r.Chained[j].Source })
	sort.Slice(r.Runtimes, func(i, j int) bool {
		if r.Runtimes[i].Type != r.Runtimes[j].Type {
			return r.Runtimes[i].Type < r.Runtimes[j].Type
		}
		return r.Runtimes[i].Version < r.Runtimes[j].Version
	})
	sort.Slice(r.Toolchain, func(i, j int) bool { return r.Toolchain[i].Name < r.Toolchain[j].Name })
	sort.Strings(r.Unresolved)
}

// Key identifies a prerequisite within a build.
func (p Prerequisite) Key() string { return p.Type + "/" + p.Version + "/" + p.Arch }

// DisplayName is what to call it in a document. The type and version are what the script
// asked for, so they are what a reader recognises; nothing here is invented.
func (p Prerequisite) DisplayName() string {
	name := p.Type
	if p.Version != "" {
		name += " " + p.Version
	}
	if p.Arch != "" {
		name += " (" + p.Arch + ")"
	}
	return name
}

// relative renders a path relative to the .msis, and refuses to emit anything that escapes it.
//
// A source above the script's directory is legitimate - "..\templates\x64" is in msis's own
// setup.msis - so it is kept as a relative path with ".." in it. What is never emitted is an
// ABSOLUTE path: it names the build machine, and a reader cannot act on it.
func (r *Record) relative(p string) string {
	if p == "" {
		return ""
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(r.scriptDir, p)
	}
	if rel, err := filepath.Rel(r.scriptDir, abs); err == nil {
		return filepath.ToSlash(rel)
	}
	// A different volume, so no relative path exists. The base name is all that can be said
	// without naming the machine.
	return "(elsewhere)/" + filepath.ToSlash(filepath.Base(p))
}

// cacheRef turns an absolute prerequisite-cache path into a symbolic one, and hashes the file
// while it still knows where it is.
//
// The cache lives under %LOCALAPPDATA%, so the real path names a user account. What a reader
// needs is which entry of the cache it was, which is the tail.
func (r *Record) cacheRef(p string) (ref, sum string) {
	if s, err := hashFile(p); err == nil {
		sum = s
	}
	norm := filepath.ToSlash(p)
	if i := strings.LastIndex(strings.ToLower(norm), "/prerequisites/"); i >= 0 {
		return "prerequisite-cache:" + norm[i+len("/prerequisites/"):], sum
	}
	return "prerequisite-cache:" + path.Base(norm), sum
}

// hashFile reads a file and digests it. A directory needs no special case - os.ReadFile
// refuses one - and HookDllDir names a directory, so that path really is reachable.
func hashFile(p string) (string, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
