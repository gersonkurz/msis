package wix

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// ExtensionPackage is what one WiX extension's NuGet package declares in its .nuspec (#67),
// pinned per package version. An installer built with the extension embeds some of its files
// as Binary-table streams - the WixUI bitmaps, the Util custom-action DLL - and these facts
// are what the SBOM can then say about them.
//
// The extension cache `wix extension add` fills keeps only the DLL, not the .nuspec, so the
// facts cannot be read at build time. They are pinned here instead, like the prerequisite pins
// (decisions D5), and `just wix-packages-check` - which gates a release - compares every entry
// with what nuget.org serves for that exact version.
type ExtensionPackage struct {
	Authors string // <authors>
	// Repository is <repository url>. The packages declare no projectUrl, and the repository
	// is where their authors are reached - the same way msis names itself, by its repository.
	Repository string
	// LicenseFile is the <license type="file"> the package declares, and LicenseSHA256 the
	// digest of that file in the package: the check fails when WiX changes the text, which is
	// when License has to be looked at again.
	LicenseFile   string
	LicenseSHA256 string
	// License is the licence as BSI TR-03183-2 v2.1.0 §6.1 names it: the SPDX id, else the
	// ScanCode LicenseDB id. The agreement has no SPDX id; ScanCode's is
	// LicenseRef-scancode-os-maintenance-fee-eula (decisions D18).
	License string
}

// osmf is what every WiX 6 and 7 extension package declares: the binary release comes under
// the Open Source Maintenance Fee Agreement, whose own text states that the source is MS-RL.
// 6.x and 7.0.0 ship different texts - 7.0.0 limits the fee to users with an annual gross
// revenue of US$10,000 or more - so each text is pinned by its own digest.
func osmf(sha256 string) ExtensionPackage {
	return ExtensionPackage{
		Authors:       "WiX Toolset Team",
		Repository:    "https://github.com/wixtoolset/wix",
		LicenseFile:   "OSMFEULA.txt",
		LicenseSHA256: sha256,
		License:       "LicenseRef-scancode-os-maintenance-fee-eula",
	}
}

var (
	osmf6 = osmf("d7383eccaa4f11f856ce5767864315265f164f85f03e50a5be8cf1e7da302996")
	osmf7 = osmf("3358af585772039e45d1192213838f906a478463ac3bb4da208ec0d874c2219b")
)

// extensionPackages is keyed "<package id>/<version>". Only the extensions an MSI build loads
// embed Binary-table streams, so only they are pinned; a version that is not here is not
// attributed at all, which is the failure a missing pin should have.
var extensionPackages = map[string]ExtensionPackage{
	extUI + "/6.0.0":   osmf6,
	extUI + "/6.0.1":   osmf6,
	extUI + "/6.0.2":   osmf6,
	extUI + "/7.0.0":   osmf7,
	extUtil + "/6.0.0": osmf6,
	extUtil + "/6.0.1": osmf6,
	extUtil + "/6.0.2": osmf6,
	extUtil + "/7.0.0": osmf7,
}

// PinnedExtensionPackages returns the pinned entries, for the check against nuget.org.
func PinnedExtensionPackages() map[string]ExtensionPackage { return extensionPackages }

// ExtensionPackageFacts returns what the package id at version declares, if msis pins it.
func ExtensionPackageFacts(id, version string) (ExtensionPackage, bool) {
	p, ok := extensionPackages[id+"/"+version]
	return p, ok
}

// MSIExtensions are the extensions an MSI build loads.
func MSIExtensions() []string { return append([]string(nil), msiExtensions...) }

// ResolvedExtension is one extension as `wix build` loads it (#67).
type ResolvedExtension struct {
	ID      string
	Version string // the version folder chosen; "" when msis did not resolve it
	Path    string // the DLL; "" when unresolved, and wix is then handed the bare id
}

// arg is what `wix build -ext` is given: the resolved DLL, so WiX loads exactly the file msis
// read - a rooted path is loaded as it is - or, unresolved, the bare id for WiX to find.
func (r ResolvedExtension) arg() string {
	if r.Path != "" {
		return r.Path
	}
	return r.ID
}

// ResolveExtensions resolves each id the way WiX's ExtensionManager.Load resolves a bare
// extension reference (WiX 6/7, src/wix/WixToolset.Core/ExtensibilityServices/ExtensionManager.cs),
// for a build run from workDir. The build is then handed the result, so what an SBOM attributes
// a stream to is by construction what the build loaded (decisions D18).
func ResolveExtensions(workDir string, ids []string, wixMajor int) []ResolvedExtension {
	out := make([]ResolvedExtension, 0, len(ids))
	for _, id := range ids {
		out = append(out, resolveExtension(workDir, extensionCacheLocations(workDir), id, wixMajor))
	}
	return out
}

// extensionCacheLocations is GetCacheLocations, as absolute paths as WiX sees them from workDir,
// its working directory: the project cache there, the user cache (WIX_EXTENSIONS, else the
// profile), then the machine caches under Common Files - the 64-bit one on a 64-bit OS, then the
// x86 one. A location msis cannot determine exactly is "", and resolution stops at it: skipping
// it could hand WiX a file from a later location that WiX itself would never have reached.
//
// WiX's last location, the tool's own folder, is not listed: an extension found only there stays
// unresolved, is handed to wix by bare id, and WiX finds it itself - msis then attributes
// nothing to it, which is the safe side.
func extensionCacheLocations(workDir string) []string {
	var out []string
	// add places base/parts as WiX would: a relative base is relative to WiX's working
	// directory, which is workDir, not this process's.
	add := func(base string, parts ...string) {
		switch {
		case base == "":
			out = append(out, "")
		case filepath.IsAbs(base):
			out = append(out, filepath.Join(append([]string{base}, parts...)...))
		case workDir != "" && filepath.IsAbs(workDir):
			out = append(out, filepath.Join(append([]string{workDir, base}, parts...)...))
		default:
			out = append(out, "")
		}
	}
	add(workDir, ".wix", "extensions")
	user := os.Getenv("WIX_EXTENSIONS")
	if user == "" {
		user, _ = os.UserHomeDir()
	}
	add(user, ".wix", "extensions")
	// Environment.Is64BitOperatingSystem: a 64-bit process, or a 32-bit one under WOW64. On a
	// 64-bit Windows CommonProgramW6432 is set whatever the process's bitness; on a 32-bit one
	// WiX skips this location, and so does this, and the x86 folder is CommonProgramFiles.
	x86 := os.Getenv("CommonProgramFiles(x86)")
	if runtime.GOARCH != "386" || os.Getenv("PROCESSOR_ARCHITEW6432") != "" {
		add(os.Getenv("CommonProgramW6432"), "WixToolset", "extensions")
	} else {
		x86 = os.Getenv("CommonProgramFiles")
	}
	add(x86, "WixToolset", "extensions")
	return out
}

// resolveExtension is Load's order. First the reference itself is tried as a FILE in WiX's
// working directory: a file named like the id there wins over every cache, so it is left to WiX
// and nothing is attributed. Then the first location holding a folder for id wins, and within
// it only the LATEST version folder is tried - if that version has no DLL for this WiX major,
// WiX moves on to the next location, not to an older version, and so does this.
func resolveExtension(workDir string, locations []string, id string, wixMajor int) ResolvedExtension {
	unresolved := ResolvedExtension{ID: id}
	if workDir == "" || !filepath.IsAbs(workDir) {
		return unresolved // the direct-file check cannot be made as WiX makes it
	}
	if info, err := os.Stat(filepath.Join(workDir, id)); err == nil && !info.IsDir() {
		return unresolved
	}
	for _, loc := range locations {
		if loc == "" {
			return unresolved
		}
		folder := filepath.Join(loc, id)
		if info, err := os.Stat(folder); err != nil || !info.IsDir() {
			continue
		}
		version, ok := latestVersionIn(folder)
		if !ok {
			continue
		}
		p := filepath.Join(folder, version, fmt.Sprintf("wixext%d", wixMajor), id+".dll")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return ResolvedExtension{ID: id, Version: version, Path: p}
		}
	}
	return unresolved
}

// latestVersionIn is TryFindLatestVersionInFolder: the highest valid WixVersion among the
// folder's subdirectories; on a tie the first in directory order stays.
func latestVersionIn(folder string) (string, bool) {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return "", false
	}
	var best string
	var bestV wixVersion
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		v, ok := parseWixVersion(e.Name())
		if ok && (best == "" || compareWixVersion(bestV, v) < 0) {
			best, bestV = e.Name(), v
		}
	}
	return best, best != ""
}

// wixVersion is WiX's WixVersion, the parts its comparer uses: four numbers and the release
// labels. Build metadata is ignored for a valid version, as WixVersionComparer ignores it.
type wixVersion struct {
	parts  [4]uint32
	labels []wixLabel
}

type wixLabel struct {
	text    string
	numeric bool
	n       uint32
}

// wixVersionPattern is WixVersion.Parse's grammar for a VALID version: an optional v, one to
// four dot-separated numbers, optional "-" release labels of [0-9A-Za-z-], optional "+" metadata.
var wixVersionPattern = regexp.MustCompile(`^[vV]?([0-9]+(?:\.[0-9]+){0,3})(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+.*)?$`)

func parseWixVersion(s string) (wixVersion, bool) {
	m := wixVersionPattern.FindStringSubmatch(s)
	if m == nil {
		return wixVersion{}, false
	}
	var v wixVersion
	for i, part := range strings.Split(m[1], ".") {
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return wixVersion{}, false // WiX's UInt32.TryParse: out of range is invalid
		}
		v.parts[i] = uint32(n)
	}
	if m[2] != "" {
		for _, l := range strings.Split(m[2], ".") {
			label := wixLabel{text: l}
			if n, err := strconv.ParseUint(l, 10, 32); err == nil {
				label.numeric, label.n = true, uint32(n)
			}
			v.labels = append(v.labels, label)
		}
	}
	return v, true
}

// compareWixVersion is WixVersionComparer.Compare for two valid versions: the numbers, then a
// release above any prerelease, then the labels pairwise - a numeric label below an alphabetic
// one, numbers numerically, text ordinally ignoring case, a missing label below a present one.
func compareWixVersion(a, b wixVersion) int {
	for i := range a.parts {
		if a.parts[i] != b.parts[i] {
			if a.parts[i] < b.parts[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.labels) > 0 && len(b.labels) == 0:
		return -1
	case len(a.labels) == 0 && len(b.labels) > 0:
		return 1
	}
	for i := 0; i < len(a.labels) || i < len(b.labels); i++ {
		switch {
		case i >= len(a.labels):
			return -1
		case i >= len(b.labels):
			return 1
		}
		x, y := a.labels[i], b.labels[i]
		switch {
		case x.numeric && y.numeric:
			if x.n != y.n {
				if x.n < y.n {
					return -1
				}
				return 1
			}
		case x.numeric:
			return -1
		case y.numeric:
			return 1
		default:
			if c := strings.Compare(strings.ToUpper(x.text), strings.ToUpper(y.text)); c != 0 {
				return c
			}
		}
	}
	return 0
}

// ExtensionPayloads returns the files an extension DLL carries for the build, by SHA-256: the
// entry name inside its embedded .wixlib, e.g. "wix-ir/bannrbmp.bmp". A .wixlib is a zip, and
// the DLL embeds one per culture or platform, so every embedded zip is read.
//
// Each zip is located from its end-of-central-directory record, which says where the zip
// starts; nothing is found by searching for a header that merely looks right.
func ExtensionPayloads(dll string) (map[string]string, error) {
	data, err := os.ReadFile(dll)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	eocd := []byte("PK\x05\x06")
	for i := 0; ; {
		j := bytes.Index(data[i:], eocd)
		if j < 0 {
			break
		}
		j += i
		i = j + len(eocd)
		if j+22 > len(data) {
			break
		}
		// In int64: the fields are unsigned 32-bit, and on a 32-bit build an int would wrap and
		// let a bogus record through the bounds check below.
		cdSize := int64(binary.LittleEndian.Uint32(data[j+12:]))
		cdOffset := int64(binary.LittleEndian.Uint32(data[j+16:]))
		commentLen := int64(binary.LittleEndian.Uint16(data[j+20:]))
		start, end := int64(j)-cdSize-cdOffset, int64(j)+22+commentLen
		if start < 0 || start >= end || end > int64(len(data)) {
			continue // four bytes that are not a real record
		}
		z, err := zip.NewReader(bytes.NewReader(data[start:end]), int64(end-start))
		if err != nil {
			continue
		}
		for _, f := range z.File {
			sum, err := hashEntry(f)
			if err != nil {
				continue
			}
			if _, seen := out[sum]; !seen {
				out[sum] = f.Name
			}
		}
	}
	return out, nil
}

func hashEntry(f *zip.File) (string, error) {
	r, err := f.Open()
	if err != nil {
		return "", err
	}
	defer r.Close()
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
