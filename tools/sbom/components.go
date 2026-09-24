package main

import (
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Component documents: what msis cannot see inside the files it packages, supplied by the build
// that produced them (#36). setup.msis names one for msis.exe and one for each hook DLL with
// `<sbom source= for=>`, so msis's own installers carry the Go modules and native libraries inside
// those files in their SBOM, instead of listing them as opaque bytes with a hash.
//
// msis checks each document's SHA-256 against the file it packages and fails the build on a
// mismatch, which is what binds these documents to the bytes that shipped - a stale document, or
// one for another architecture, cannot be attached by mistake.

// composition is the CycloneDX coverage statement, as the merge imports it.
type composition struct {
	Aggregate    string   `json:"aggregate"`
	Assemblies   []string `json:"assemblies,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
}

type componentDoc struct {
	BOMFormat   string `json:"bomFormat"`
	SpecVersion string `json:"specVersion"`
	Version     int    `json:"version"`
	Metadata    struct {
		Lifecycles []lifecycle         `json:"lifecycles"`
		Licenses   []dataLicenseChoice `json:"licenses"`
		Component  component           `json:"component"`
	} `json:"metadata"`
	Compositions []composition `json:"compositions"`
}

// componentsDir is where the documents are written: inside dist, which clean-bootstrap wipes, so
// one cannot outlive the release run that wrote it; and outside every folder setup.msis packages.
func componentsDir(dist string) string { return filepath.Join(dist, "components") }

func binaryDocName(arch string) string { return "msis-" + arch + ".exe.cdx.json" }
func hookDocName(arch string) string   { return "msi-simplica-" + arch + ".dll.cdx.json" }

// writeComponentDocs writes a document for the msis binary of each of binArches, and one for the
// hook DLL of every architecture: every MSI packages all three DLLs, whichever msis.exe it carries.
func writeComponentDocs(version, dist, binDir, templatesDir string, binArches []string) error {
	out := componentsDir(dist)
	if err := os.MkdirAll(out, 0755); err != nil {
		return err
	}
	env, err := readGoEnv()
	if err != nil {
		return err
	}
	own, err := ownLicense(env)
	if err != nil {
		return err
	}
	for _, arch := range binArches {
		doc, err := binaryDoc(version, filepath.Join(binDir, "msis-"+arch+".exe"), env, own)
		if err != nil {
			return err
		}
		if err := writeDoc(filepath.Join(out, binaryDocName(arch)), doc); err != nil {
			return err
		}
	}
	for _, arch := range arches {
		doc, err := hookDoc(version, filepath.Join(templatesDir, arch, "msi-simplica.dll"), own)
		if err != nil {
			return err
		}
		if err := writeDoc(filepath.Join(out, hookDocName(arch)), doc); err != nil {
			return err
		}
	}
	return nil
}

// binaryDoc describes one msis binary: every Go module linked into it and the standard library of
// the toolchain that built it, all read back out of the binary. Go links statically and records
// every module that contributed code, so the assemblies are complete; build info carries no
// module graph, so which module depends on which stays unknown.
func binaryDoc(version, path string, env goEnv, own string) (*componentDoc, error) {
	sum, err := sha256File(path)
	if err != nil {
		return nil, err
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading build info from %s: %w", path, err)
	}
	byPath, std, err := moduleLicenses(info, env)
	if err != nil {
		return nil, err
	}
	parts := moduleComponents(info)
	attachLicences(parts, byPath)
	goVersion := strings.TrimPrefix(info.GoVersion, "go")
	parts = append(parts, component{
		Type:     "library",
		Name:     "stdlib",
		Version:  goVersion,
		PURL:     "pkg:golang/stdlib@" + goVersion,
		Licenses: licensed(std),
	})
	for i := range parts {
		parts[i].BOMRef = parts[i].PURL
	}
	root := component{
		Type:       "application",
		BOMRef:     "msis",
		Name:       "msis",
		Version:    version,
		PURL:       "pkg:golang/github.com/gersonkurz/msis@v" + version,
		Hashes:     []hash{{Alg: "SHA-256", Content: sum}},
		Licenses:   licensed(own),
		Components: parts,
	}
	return newComponentDoc(root, "complete"), nil
}

// hookDoc describes one hook DLL: the two pinned NuGet libraries it links. It also links the MSVC
// runtime statically (RuntimeLibrary=MultiThreaded), whose version the DLL does not record, so the
// assemblies are declared incomplete rather than complete.
func hookDoc(version, path, own string) (*componentDoc, error) {
	sum, err := sha256File(path)
	if err != nil {
		return nil, fmt.Errorf("%w\nthe hook DLLs are staged by `just build-hooks`", err)
	}
	parts := append([]component(nil), nativeComponents...)
	for i := range parts {
		parts[i].BOMRef = parts[i].PURL
	}
	root := component{
		Type:        "library",
		BOMRef:      "msi-simplica",
		Name:        "msi-simplica",
		Version:     version,
		PURL:        "pkg:generic/msi-simplica@" + version,
		Description: "Native installer-hook DLL built from native/msi-simplica; the statically linked MSVC runtime is not listed",
		Hashes:      []hash{{Alg: "SHA-256", Content: sum}},
		Licenses:    licensed(own),
		Components:  parts,
	}
	return newComponentDoc(root, "incomplete"), nil
}

func newComponentDoc(root component, assemblies string) *componentDoc {
	doc := &componentDoc{BOMFormat: "CycloneDX", SpecVersion: "1.6", Version: 1}
	doc.Metadata.Lifecycles = postBuild
	doc.Metadata.Licenses = dataLicense
	doc.Metadata.Component = root
	doc.Compositions = []composition{
		{Aggregate: assemblies, Assemblies: []string{root.BOMRef}},
		{Aggregate: "unknown", Dependencies: []string{root.BOMRef}},
	}
	return doc
}

func writeDoc(path string, doc *componentDoc) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}
