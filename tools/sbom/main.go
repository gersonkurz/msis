// Command sbom writes a CycloneDX software bill of materials for a release.
//
// Go hands us most of one for free: every binary carries its module graph, the exact version of
// each dependency and the same h1: dirhash that go.sum records, and debug/buildinfo reads that
// back out of the shipped file. So the Go half needs no external tool and no network - it is
// derived from the artifacts that actually shipped, not from go.mod, which is a different claim.
//
// The half Go knows nothing about is written down here: the native hook DLL that ships inside
// every MSI and runs as a custom action, and its two pinned NuGet libraries. An SBOM that
// silently omitted a native DLL would be worse than none.
//
// Usage: sbom -version X.Y.Z -dist DIR [-bin DIR] [-out FILE]
//
//	sbom -components ...   the per-file documents msis's own installers compose (components.go)
//	sbom -gate ...         fail if a release SBOM's coverage fell below the baseline (gate.go)
package main

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gersonkurz/msis/internal/wix"
)

// arches are the three architectures a release builds, named as the justfile names the binaries.
var arches = []string{"x64", "x86", "arm64"}

// nativeComponents are the parts of a release that never appear in a Go module graph: the hook
// DLL's two pinned NuGet libraries. Their versions live in the .vcxproj, which Go cannot read, so
// this is the one hand-kept copy in the file - TestNugetVersionsMatchVcxproj reads the project
// file and fails if they drift. Their licence is the one each package DECLARES in its .nuspec;
// TestNugetLicencesMatchTheNuspec checks it where the NuGet cache is present.
var nativeComponents = []component{
	{
		Type:         "library",
		Name:         "WixToolset.WcaUtil",
		Version:      "5.0.2",
		PURL:         "pkg:nuget/WixToolset.WcaUtil@5.0.2",
		Description:  "WiX custom-action utility library, linked into msi-simplica.dll",
		Licenses:     licensed("MS-RL"),
		Manufacturer: wixCreator,
	},
	{
		Type:         "library",
		Name:         "WixToolset.DUtil",
		Version:      "5.0.2",
		PURL:         "pkg:nuget/WixToolset.DUtil@5.0.2",
		Description:  "WiX base utility library, linked into msi-simplica.dll",
		Licenses:     licensed("MS-RL"),
		Manufacturer: wixCreator,
	},
}

// creator is CycloneDX's organizationalEntity, the parts used here.
type creator struct {
	Name string   `json:"name,omitempty"`
	URL  []string `json:"url,omitempty"`
}

// msisCreator is msis's own repository: the creator of msis.exe and msi-simplica.dll.
var msisCreator = &creator{URL: []string{"https://github.com/gersonkurz/msis"}}

// wixCreator is what both WiX packages declare in their .nuspec (authors, projectUrl);
// TestNugetLicencesMatchTheNuspec checks it.
var wixCreator = &creator{Name: "WiX Toolset Team", URL: []string{"https://wixtoolset.org/"}}

type hash struct {
	Alg     string `json:"alg"`
	Content string `json:"content"`
}

type component struct {
	Type        string `json:"type"`
	BOMRef      string `json:"bom-ref,omitempty"`
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	PURL        string `json:"purl,omitempty"`
	Description string `json:"description,omitempty"`
	Hashes      []hash `json:"hashes,omitempty"`

	Licenses []licenseChoice `json:"licenses,omitempty"`

	// Manufacturer is the component's creator (BSI TR-03183-2 v2.1.0 §5.2.2, #65): only where it
	// is declared - a NuGet package's authors and projectUrl, msis's own repository - never
	// derived from a name.
	Manufacturer *creator `json:"manufacturer,omitempty"`

	// Components nests what is linked INTO this one; only the component documents use it.
	Components []component `json:"components,omitempty"`
}

// tool identifies what produced this BOM. CycloneDX reserves metadata.tools for tools that
// create, enrich or validate the DOCUMENT - not for the toolchain that built its subject, which
// belongs in properties below.
type tool struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Vendor  string `json:"vendor,omitempty"`
}

type property struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type metadata struct {
	Lifecycles []lifecycle `json:"lifecycles,omitempty"`
	// Licenses is the documents' own data licence; see dataLicense.
	Licenses  []dataLicenseChoice `json:"licenses,omitempty"`
	Component component           `json:"component"`
	Tools     []tool              `json:"tools,omitempty"`

	// Properties carry the build provenance: the commit the artifacts were built from, whether
	// that tree was clean, and the observed build toolchain. A "+dirty" release cannot be
	// reproduced, and saying so in the SBOM is the point of having one.
	Properties []property `json:"properties,omitempty"`
}

// lifecycle is NTIA's generation context (#62). Everything tools/sbom writes is read from built
// binaries and packaged artifacts: post-build.
type lifecycle struct {
	Phase string `json:"phase"`
}

var postBuild = []lifecycle{{Phase: "post-build"}}

// dataLicense is the licence msis's own SBOMs are offered under: CC0-1.0, the data licence SPDX
// mandates for its documents, so anyone may read, copy, index and republish them without asking
// (#62; README "SBOM"). bootstrap/setup.msis grants the same through SBOM_DATA_LICENSE.
type dataLicenseChoice struct {
	Expression string `json:"expression"`
}

var dataLicense = []dataLicenseChoice{{Expression: "CC0-1.0"}}

type bom struct {
	BOMFormat   string      `json:"bomFormat"`
	SpecVersion string      `json:"specVersion"`
	Version     int         `json:"version"`
	Metadata    metadata    `json:"metadata"`
	Components  []component `json:"components"`
}

func main() {
	var (
		version   = flag.String("version", "", "release version, e.g. 3.0.5")
		dist      = flag.String("dist", "bootstrap/dist", "directory holding the released artifacts")
		bin       = flag.String("bin", "bootstrap", "directory holding the built msis binaries")
		out       = flag.String("out", "", "output file (default <dist>/msis-<version>.cdx.json)")
		doCapture = flag.Bool("capture", false, "record the binaries and toolchain before packaging")
		doSeal    = flag.Bool("seal", false, "record the artifacts after packaging succeeds")
		doComps   = flag.Bool("components", false, "write the component documents setup.msis composes, before packaging")
		compArch  = flag.String("arches", strings.Join(arches, ","), "with -components: the msis binaries to describe")
		templates = flag.String("templates", "templates", "with -components: the folder holding the staged hook DLLs")
		doGate    = flag.Bool("gate", false, "after packaging: fail if a release SBOM's coverage fell below the baseline (gate.go)")
	)
	flag.Parse()

	if *version == "" {
		fatal(fmt.Errorf("-version is required"))
	}
	if *out == "" {
		*out = filepath.Join(*dist, "msis-"+*version+".cdx.json")
	}

	// The two capture points run inside the release pipeline; generation is the default and can
	// be run again later, but only over a tree that still matches what packaging recorded.
	if *doCapture {
		if err := capture(*version, *dist, *bin, wix.GetWixVersion()); err != nil {
			fatal(err)
		}
		fmt.Printf("Recorded %d binaries and the build toolchain in %s\n", len(arches), manifestPath(*dist))
		return
	}
	if *doComps {
		if err := writeComponentDocs(*version, *dist, *bin, *templates, strings.Split(*compArch, ",")); err != nil {
			fatal(err)
		}
		fmt.Printf("Wrote the component documents in %s\n", componentsDir(*dist))
		return
	}
	if *doGate {
		if err := gate(*version, *dist); err != nil {
			fatal(err)
		}
		fmt.Println("SBOM coverage is at or above the baseline")
		return
	}
	if *doSeal {
		if err := seal(*version, *dist, *bin); err != nil {
			fatal(err)
		}
		fmt.Printf("Sealed %s with the packaged artifacts\n", manifestPath(*dist))
		return
	}

	doc, err := build(*version, *dist, *bin)
	if err != nil {
		fatal(err)
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0644); err != nil {
		fatal(err)
	}
	fmt.Printf("Wrote %s: %d components\n", *out, len(doc.Components))
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "sbom: %v\n", err)
	os.Exit(1)
}

// fileFacts is what we can say about a file on disk without opening it as a program.
type fileFacts struct {
	name    string
	path    string
	sha256  string
	modTime time.Time
}

// build assembles the document from the packaging manifest: every recorded hash must still match
// the file on disk, so the document describes one release run rather than whatever happens to be
// lying in the two directories.
func build(version, dist, binDir string) (*bom, error) {
	m, err := readManifest(dist)
	if err != nil {
		return nil, err
	}
	if m.Version != version {
		return nil, fmt.Errorf("packaging manifest is for %s, not %s", m.Version, version)
	}
	if len(m.Artifacts) == 0 {
		return nil, fmt.Errorf("packaging manifest was never sealed: packaging did not finish")
	}
	if err := verifyRecorded(binDir, m.Binaries, "binary"); err != nil {
		return nil, err
	}
	if err := verifyRecorded(dist, m.Artifacts, "artifact"); err != nil {
		return nil, err
	}

	binaries, err := statFiles(binDir, binaryNames())
	if err != nil {
		return nil, err
	}
	artifacts, err := statFiles(dist, artifactNames(version))
	if err != nil {
		return nil, err
	}

	mods, provenance, err := goComponents(binaries)
	if err != nil {
		return nil, err
	}
	// goComponents has already required the three binaries to agree on every module, so one
	// binary's build info names the licences for all of them.
	env, err := readGoEnv()
	if err != nil {
		return nil, err
	}
	info, err := buildinfo.ReadFile(binaries[0].path)
	if err != nil {
		return nil, err
	}
	byPath, _, err := moduleLicenses(info, env)
	if err != nil {
		return nil, err
	}
	attachLicences(mods, byPath)
	own, err := ownLicense(env)
	if err != nil {
		return nil, err
	}

	components := make([]component, 0, len(artifacts)+len(binaries)+len(mods)+len(nativeComponents)+1)
	for _, a := range artifacts {
		kind := "application"
		if strings.HasSuffix(a.name, ".msi") {
			kind = "file"
		}
		components = append(components, component{
			Type:    kind,
			Name:    a.name,
			Version: version,
			Hashes:  []hash{{Alg: "SHA-256", Content: a.sha256}},
		})
	}
	// The binaries the inventory below was read from, by hash, so the document names the exact
	// bytes it describes rather than leaving that to be inferred from a directory listing.
	for _, b := range binaries {
		components = append(components, component{
			Type:        "application",
			Name:        b.name,
			Version:     version,
			Description: "msis binary inspected to produce this inventory; hashed before packaging began in the release run that produced the artifacts above",
			Hashes:      []hash{{Alg: "SHA-256", Content: b.sha256}},
		})
	}
	components = append(components, mods...)
	// The hook DLL is built from this repo and versioned with it, so it carries the release
	// version; its libraries carry their own.
	components = append(components, component{
		Type:        "library",
		Name:        "msi-simplica",
		Version:     version,
		PURL:        "pkg:generic/msi-simplica@" + version,
		Description: "Native installer-hook DLL (x86/x64/arm64) built from native/msi-simplica and shipped inside every MSI, where it runs as a custom action",
		Licenses:    licensed(own),
	})
	components = append(components, nativeComponents...)

	provenance = append(provenance, toolchainProvenance(m.WixVersion)...)

	return &bom{
		BOMFormat:   "CycloneDX",
		SpecVersion: "1.6",
		Version:     1,
		Metadata: metadata{
			Lifecycles: postBuild,
			Licenses:   dataLicense,
			Component: component{
				Type:        "application",
				Name:        "msis",
				Version:     version,
				PURL:        "pkg:golang/github.com/gersonkurz/msis@v" + version,
				Description: "Windows installer generator: .msis scripts to MSI packages via WiX",
				Licenses:    licensed(own),
			},
			Tools: []tool{{
				Name:    "msis tools/sbom",
				Version: version,
				Vendor:  "NG Branch Technology GmbH",
			}},
			Properties: provenance,
		},
		Components: components,
	}, nil
}

// toolchainProvenance records the WiX that is actually present. wix.DefaultVersion is only what
// /SETUP-WIX installs when given no override - msis supports whichever of WiX 6 or 7 is
// installed, so asserting the default would report a WiX 6 build as 7.0.0. When WiX cannot be
// queried the field is left out rather than guessed: an absent fact beats a fabricated one.
//
// The extension IDs are a different kind of claim - they are the set this build loads, which is
// configuration rather than an observation - so they are labelled as such.
func toolchainProvenance(wixVersion string) []property {
	var props []property
	if v := strings.TrimSpace(wixVersion); v != "" && v != "(unavailable)" {
		props = append(props, property{Name: "msis:wix.version.observed", Value: v})
	}
	props = append(props, property{
		Name:  "msis:wix.extensions.loaded",
		Value: strings.Join(wix.AllExtensions, ","),
	})
	return props
}

func binaryNames() []string {
	names := make([]string, 0, len(arches))
	for _, a := range arches {
		names = append(names, "msis-"+a+".exe")
	}
	return names
}

func artifactNames(version string) []string {
	names := make([]string, 0, len(arches)+1)
	for _, a := range arches {
		names = append(names, "msis-"+version+"-"+a+".msi")
	}
	return append(names, "msis-"+version+"-setup.exe")
}

// statFiles hashes and stats each named file, failing if any is missing: an SBOM naming a file
// that was not built would be a claim about a release that does not exist.
func statFiles(dir string, names []string) ([]fileFacts, error) {
	facts := make([]fileFacts, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("release file missing: %w\nrun `just release-all` first", err)
		}
		sum, err := sha256File(path)
		if err != nil {
			return nil, err
		}
		facts = append(facts, fileFacts{name: name, path: path, sha256: sum, modTime: info.ModTime()})
	}
	return facts, nil
}

// goComponents reads the module graph back out of the built binaries. All three architectures are
// read and required to agree on everything that is not architecture: same main module, same
// commit, same Go toolchain, same dependencies down to the go.sum dirhash. They are built from
// one tree in one pass, so a difference means a stale or mixed build - which would otherwise
// produce a document that attributes x64's commit to a release containing someone else's x86.
func goComponents(binaries []fileFacts) ([]component, []property, error) {
	var first []component
	var provenance []property
	var firstFacts map[string]string
	var firstName string

	for _, b := range binaries {
		info, err := buildinfo.ReadFile(b.path)
		if err != nil {
			return nil, nil, fmt.Errorf("reading build info from %s: %w", b.path, err)
		}

		comps := moduleComponents(info)
		facts := comparableFacts(info, comps)

		if first == nil {
			first, firstFacts, firstName = comps, facts, b.name
			provenance = buildProvenance(info)
			continue
		}
		if diff := describeDiff(firstFacts, facts); diff != "" {
			return nil, nil, fmt.Errorf("%s and %s disagree: %s\n"+
				"the three binaries must come from one build of one tree; rebuild before generating an SBOM",
				firstName, b.name, diff)
		}
	}

	return first, provenance, nil
}

func moduleComponents(info *buildinfo.BuildInfo) []component {
	comps := make([]component, 0, len(info.Deps))
	for _, dep := range info.Deps {
		// A replaced module reports its replacement's identity; that is what shipped.
		if dep.Replace != nil {
			dep = dep.Replace
		}
		c := component{
			Type:    "library",
			Name:    dep.Path,
			Version: dep.Version,
			PURL:    "pkg:golang/" + dep.Path + "@" + dep.Version,
		}
		if dep.Sum != "" {
			// The go.sum dirhash, as recorded in the binary. Not a plain digest of a file,
			// so it is named for what it is rather than passed off as sha256.
			c.Description = "go.sum " + dep.Sum
		}
		comps = append(comps, c)
	}
	sort.Slice(comps, func(i, j int) bool { return comps[i].Name < comps[j].Name })
	return comps
}

// comparableFacts is everything the three binaries must agree on. GOARCH and the like are
// deliberately absent: those are supposed to differ.
func comparableFacts(info *buildinfo.BuildInfo, comps []component) map[string]string {
	facts := map[string]string{
		"go version":   info.GoVersion,
		"main module":  info.Main.Path + "@" + info.Main.Version,
		"module count": fmt.Sprintf("%d", len(comps)),
	}
	for _, s := range info.Settings {
		if strings.HasPrefix(s.Key, "vcs") {
			facts[s.Key] = s.Value
		}
	}
	for _, c := range comps {
		// Version AND dirhash: an unchanged version with a different hash is the case worth
		// catching, and it is invisible if only names and versions are compared.
		facts["module "+c.Name] = c.Version + " " + c.Description
	}
	return facts
}

// describeDiff names the first disagreement in key order, so the message is stable rather than
// dependent on map iteration.
func describeDiff(a, b map[string]string) string {
	keys := make([]string, 0, len(a)+len(b))
	seen := map[string]bool{}
	for _, m := range []map[string]string{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)

	for _, k := range keys {
		av, aok := a[k]
		bv, bok := b[k]
		switch {
		case aok && !bok:
			return fmt.Sprintf("%s: %q vs absent", k, av)
		case !aok && bok:
			return fmt.Sprintf("%s: absent vs %q", k, bv)
		case av != bv:
			return fmt.Sprintf("%s: %q vs %q", k, av, bv)
		}
	}
	return ""
}

// buildProvenance records what the binary says about where it came from.
func buildProvenance(info *buildinfo.BuildInfo) []property {
	props := []property{
		{Name: "msis:go.version", Value: info.GoVersion},
		{Name: "msis:go.module", Value: info.Main.Version},
	}
	want := map[string]bool{"vcs": true, "vcs.revision": true, "vcs.time": true, "vcs.modified": true}
	for _, s := range info.Settings {
		if want[s.Key] {
			props = append(props, property{Name: "msis:" + s.Key, Value: s.Value})
		}
	}
	return props
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hashing release artifact: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hashing %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
