// Package analyze runs a software-composition analyzer over an installer's payload (/ANALYZE,
// #82, D25) and keeps only what packages declare about themselves.
//
// msis identifies nothing itself. It extracts the MSI's payload to a temporary folder in its
// install layout - exactly the files the package carries, nothing from the build tree - runs
// syft on it (found on PATH, never downloaded, as /SCAN runs grype), and joins each finding
// back to the payload file its evidence is in. Of syft's identifications only three kinds are
// kept, each a package's own statement: a Python distribution's dist-info METADATA, a jar's
// Maven pom.properties, a .NET application's .deps.json. What syft infers - from a PE version
// resource, from a jar's file name - is counted and left out: a wrong identity produces false
// CVE matches and hides real ones, which is what D4 exists to prevent.
package analyze

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gersonkurz/msis/internal/msiread"
	"github.com/gersonkurz/msis/internal/sbom"
)

// Analyzer is the one tool /ANALYZE drives; the product owner's decision for #82 was syft.
const Analyzer = "syft"

// ErrNoAnalyzer is returned when syft is not on PATH.
var ErrNoAnalyzer = errors.New("syft is not on PATH; /ANALYZE runs it but never installs it - " +
	"install it from https://github.com/anchore/syft, then run again")

// Skip reasons, as the document and the terminal state them.
const (
	skipPE       = "from PE version resources"
	skipJarNoPOM = "jars without pom.properties"
	skipPython   = "Python packages without dist-info METADATA"
	skipNoPURL   = "without a purl"
	skipOutside  = "whose evidence is not a payload file"
	skipOther    = "by other catalogers"
	skipProject  = ".deps.json project entries (not packages)"
)

// Syft extracts the MSI at path, runs syft over the payload and returns the package, as
// msiread.Read would, and what syft identified from package declarations, joined to the
// package's File ids. The package comes from the same read, so a large installer is read once.
func Syft(path string) (*msiread.Package, *sbom.Analyzed, error) {
	exe, err := exec.LookPath(Analyzer)
	if err != nil {
		return nil, nil, ErrNoAnalyzer
	}
	dir, err := os.MkdirTemp("", "msis-analyze-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	pkg, files, err := msiread.ExtractTo(path, dir)
	if err != nil {
		return nil, nil, err
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(exe, "dir:.", "-o", "syft-json", "-q")
	cmd.Dir = dir // relative, so the report names payload paths and not this temp folder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("syft on the payload of %s: %v\n%s", filepath.Base(path), err,
			strings.TrimSpace(stderr.String()))
	}
	a, err := Parse(stdout.Bytes(), files)
	if err != nil {
		return nil, nil, err
	}
	// The digest of the program that ran, not of a launcher in front of it; where that program
	// cannot be named, no digest rather than one of the wrong file.
	if program := launched(exe); program != "" {
		sum, err := sha256File(program)
		if err != nil {
			return nil, nil, fmt.Errorf("hashing %s for metadata.tools: %w", program, err)
		}
		a.Tool.Hashes = []sbom.Hash{{Alg: "SHA-256", Content: sum}}
	}
	return pkg, a, nil
}

// launched is the program exe starts: exe itself, or for a Scoop shim - an exe beside a .shim
// file naming the real program - the program the shim names. A Chocolatey shim cannot be read
// that way, so it gives "", and so does a .shim that names nothing.
func launched(exe string) string {
	shim := strings.TrimSuffix(exe, filepath.Ext(exe)) + ".shim"
	if data, err := os.ReadFile(shim); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if ok && strings.TrimSpace(key) == "path" {
				return strings.Trim(strings.TrimSpace(value), `"`)
			}
		}
		return ""
	}
	if root := os.Getenv("ChocolateyInstall"); root != "" &&
		strings.EqualFold(filepath.Dir(exe), filepath.Join(root, "bin")) {
		return ""
	}
	return exe
}

// report is the part of syft's native JSON msis reads. The native format rather than syft's
// CycloneDX, because only it carries the metadata that says what an identity rests on - a
// jar's pom.properties, a Python package's dist-info - and CycloneDX flattens that away.
type report struct {
	Descriptor struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"descriptor"`
	Artifacts []artifact `json:"artifacts"`
}

type artifact struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	FoundBy      string `json:"foundBy"`
	PURL         string `json:"purl"`
	MetadataType string `json:"metadataType"`
	Locations    []struct {
		Path        string            `json:"path"`
		Annotations map[string]string `json:"annotations"`
	} `json:"locations"`
	Metadata struct {
		Type          string `json:"type"` // a .deps.json entry: package or project
		VirtualPath   string `json:"virtualPath"`
		PomProperties *struct {
			GroupID    string `json:"groupId"`
			ArtifactID string `json:"artifactId"`
			Version    string `json:"version"`
		} `json:"pomProperties"`
	} `json:"metadata"`
}

// Parse reads a syft-json report of a payload extracted by msiread.ExtractTo; files maps each
// extracted path, relative to the scanned folder, to its File id. It keeps the identifications
// that rest on a package's own declaration and counts the rest by reason.
func Parse(data []byte, files map[string]string) (*sbom.Analyzed, error) {
	var r report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("syft's report is not readable JSON: %w", err)
	}
	if r.Descriptor.Name != Analyzer || r.Descriptor.Version == "" {
		return nil, fmt.Errorf("the report does not come from %s (descriptor %q %q)", Analyzer,
			r.Descriptor.Name, r.Descriptor.Version)
	}
	fileOf := map[string]string{}
	for rel, id := range files {
		fileOf[strings.ToLower(filepath.Clean(rel))] = id
	}

	a := &sbom.Analyzed{
		Tool: sbom.Component{Type: "application", Name: Analyzer, Version: r.Descriptor.Version,
			Supplier: &sbom.Supplier{Name: "Anchore, Inc."}},
		Skipped: map[string]int{},
	}
	for _, art := range r.Artifacts {
		basis, within, skip := classify(art)
		if skip == "" && art.PURL == "" {
			skip = skipNoPURL
		}
		var fileID string
		if skip == "" {
			fileID = fileOf[strings.ToLower(evidencePath(art))]
			if fileID == "" {
				skip = skipOutside
			}
		}
		if skip != "" {
			a.Skipped[skip]++
			continue
		}
		a.Packages = append(a.Packages, sbom.AnalyzedPackage{FileID: fileID, Name: art.Name,
			Version: art.Version, PURL: art.PURL, Cataloger: art.FoundBy, Basis: basis, Within: within})
	}
	sort.Slice(a.Packages, func(i, j int) bool {
		p, q := a.Packages[i], a.Packages[j]
		if p.FileID != q.FileID {
			return p.FileID < q.FileID
		}
		if p.PURL != q.PURL {
			return p.PURL < q.PURL
		}
		return p.Within < q.Within
	})
	return a, nil
}

// classify says what an identification rests on, or why it is not kept.
func classify(art artifact) (basis, within, skip string) {
	switch art.FoundBy {
	case "python-installed-package-cataloger":
		path := strings.ReplaceAll(evidencePath(art), `\`, "/")
		if art.MetadataType == "python-package" && strings.HasSuffix(filepathDir(path), ".dist-info") &&
			strings.EqualFold(filepathBase(path), "METADATA") {
			return "dist-info METADATA", "", ""
		}
		return "", "", skipPython
	case "java-archive-cataloger":
		pom := art.Metadata.PomProperties
		// The identity is the pom's, and syft's name and version must be the pom's too: a
		// name syft took from the manifest or the file name is an inference.
		if pom != nil && pom.GroupID != "" && pom.ArtifactID != "" && pom.Version != "" &&
			art.Name == pom.ArtifactID && art.Version == pom.Version && strings.HasPrefix(art.PURL, "pkg:maven/") {
			return "pom.properties", nestedPath(art), ""
		}
		return "", "", skipJarNoPOM
	case "dotnet-deps-binary-cataloger":
		switch {
		case art.MetadataType != "dotnet-deps-entry":
			return "", "", skipPE // dotnet-portable-executable-entry: the assembly's version resource
		case art.Metadata.Type != "package":
			// The application's own project: syft gives it a nuget purl, but it is not a
			// NuGet package, and a purl naming one would be an identity nobody declared.
			return "", "", skipProject
		}
		return ".deps.json", "", ""
	case "pe-binary-package-cataloger":
		return "", "", skipPE
	}
	return "", "", skipOther
}

// evidencePath is the primary evidence location, relative to the scanned folder.
func evidencePath(art artifact) string {
	for _, l := range art.Locations {
		if l.Annotations["evidence"] == "primary" {
			return trimRoot(l.Path)
		}
	}
	if len(art.Locations) > 0 {
		return trimRoot(art.Locations[0].Path)
	}
	return ""
}

func trimRoot(p string) string {
	return filepath.FromSlash(strings.TrimLeft(strings.ReplaceAll(p, `\`, "/"), "/"))
}

// nestedPath is where inside the jar a nested jar sits, "" for the jar itself: syft's virtual
// path is outer.jar:inner.jar.
func nestedPath(art artifact) string {
	if _, inner, ok := strings.Cut(art.Metadata.VirtualPath, ":"); ok {
		return strings.ReplaceAll(inner, `\`, "/")
	}
	return ""
}

func filepathDir(slashPath string) string {
	if i := strings.LastIndex(slashPath, "/"); i >= 0 {
		return slashPath[:i]
	}
	return ""
}

func filepathBase(slashPath string) string { return slashPath[strings.LastIndex(slashPath, "/")+1:] }

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
