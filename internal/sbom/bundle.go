package sbom

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gersonkurz/msis/internal/burnread"
)

// FromBundle builds a document describing one Burn bundle.
//
// A bundle is a container of installers, so the document is shaped differently from an MSI's:
// the things it inventories are the bootstrapper application's payloads and the chained
// packages, and a chained package's own contents are described by that package's document
// rather than repeated here. Where such a document exists and provably describes the bytes this
// bundle carries, the component carries a BOM-Link to it.
func FromBundle(b *burnread.Bundle, opts Options) (*Document, error) {
	opts.withDefaults()

	serial, err := opts.NewSerial()
	if err != nil {
		return nil, fmt.Errorf("generating a serial number: %w", err)
	}
	subject, err := hashFile(b.Path)
	if err != nil {
		return nil, fmt.Errorf("hashing the subject artifact: %w", err)
	}

	ns := bundleNamespace(b)
	root := bundleComponent(b, ns, subject)

	doc := &Document{
		BOMFormat:    "CycloneDX",
		SpecVersion:  "1.6",
		SerialNumber: serial,
		Version:      1,
		Metadata: Metadata{
			Timestamp: opts.Now().UTC().Format(time.RFC3339),
			Tools:     Tools{Components: []Component{msisTool(opts)}},
			Component: root,
			Supplier:  supplierOf(b.Publisher),
			Properties: []Property{
				{propSubjectArtifact, baseName(b.Path)},
				{propBundleCode, b.Code},
				{propEngineVersion, b.EngineVersion},
				{propCoverage, bundleCoverage(b)},
			},
		},
	}
	if b.UpgradeCode != "" {
		doc.Metadata.Properties = append(doc.Metadata.Properties,
			Property{propUpgradeCode, b.UpgradeCode})
	}

	doc.Components = []Component{}
	var contained []string   // everything the bundle carries or chains
	var opaque []string      // components whose own contents this document does not describe
	var describedBy []string // components a child document describes in full

	for _, p := range b.UX {
		c := bootstrapperComponent(p, ns)
		doc.Components = append(doc.Components, c)
		contained = append(contained, c.BOMRef)
		opaque = append(opaque, c.BOMRef)
	}

	// Payloads the bundle carries that belong to no chain package. They are in the artifact,
	// so they are in the inventory.
	for _, p := range b.Loose {
		c := loosePayloadComponent(p, ns)
		doc.Components = append(doc.Components, c)
		contained = append(contained, c.BOMRef)
		opaque = append(opaque, c.BOMRef)
	}

	dir := filepath.Dir(b.Path)
	for _, pkg := range b.Packages {
		c := packageComponent(pkg, ns)

		// A link is emitted only when a document beside the bundle provably describes the
		// very bytes carried here. Anything less - no document, an unreadable one, or one
		// whose subject digest differs - produces no link and a property saying why, because
		// a link that resolves to the wrong build is worse than no link at all.
		if link, why := childLink(dir, pkg.Installer()); link != nil {
			c.ExternalReferences = append(c.ExternalReferences, *link)
			describedBy = append(describedBy, c.BOMRef)
		} else {
			c.Properties = append(c.Properties, Property{propBOMLink, why})
			opaque = append(opaque, c.BOMRef)
		}

		doc.Components = append(doc.Components, c)
		contained = append(contained, c.BOMRef)

		for _, pay := range pkg.Payloads {
			if pay.Role == burnread.RoleChained {
				continue // the package component is the installer
			}
			sc := supplementaryComponent(pay, pkg, ns)
			doc.Components = append(doc.Components, sc)
			contained = append(contained, sc.BOMRef)
			opaque = append(opaque, sc.BOMRef)
		}
	}

	doc.Dependencies = []Dependency{{Ref: root.BOMRef, DependsOn: contained}}

	// The bundle's own contents are known exactly - that is what the manifest records - but
	// what any of it was built FROM is not, so the assemblies claim and the dependency claim
	// are made separately, as on the MSI side.
	doc.Compositions = []Composition{
		{Aggregate: aggregateIncomplete, Assemblies: []string{root.BOMRef}},
		{Aggregate: aggregateComplete, Dependencies: []string{root.BOMRef}},
	}
	if len(opaque) > 0 {
		doc.Compositions = append(doc.Compositions, Composition{
			Aggregate:    aggregateUnknown,
			Assemblies:   opaque,
			Dependencies: opaque,
		})
	}
	if len(describedBy) > 0 {
		// Described elsewhere, not described here: incomplete is the honest aggregate for a
		// component this document deliberately does not expand, and the link says where the
		// rest is. Its dependency graph is a separate question and remains unknown.
		doc.Compositions = append(doc.Compositions,
			Composition{Aggregate: aggregateIncomplete, Assemblies: describedBy},
			Composition{Aggregate: aggregateUnknown, Dependencies: describedBy})
	}

	if err := enrich(doc, opts.Build); err != nil {
		return nil, err
	}

	sortDocument(doc)

	if err := requireBundleDigests(b, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// UnhashablePayloads lists the bom-refs of the components whose bytes are not in the bundle.
//
// It is derived from the BUNDLE, never from the document. Reading the answer out of the
// document would make a conformance check vacuous: a component the emitter wrongly dropped a
// digest from would simply be declared exempt and the check would pass. Derived from the
// artifact, the two disagree exactly when there is a defect.
func UnhashablePayloads(b *burnread.Bundle) []string {
	ns := bundleNamespace(b)
	var refs []string
	for _, p := range b.UX {
		if !p.Carried {
			refs = append(refs, bootstrapperRef(ns, p))
		}
	}
	for _, p := range b.Loose {
		if !p.Carried {
			refs = append(refs, looseRef(ns, p))
		}
	}
	for _, pkg := range b.Packages {
		if inst := pkg.Installer(); inst == nil || !inst.Carried {
			refs = append(refs, packageRef(ns, pkg))
		}
		for _, p := range pkg.Payloads {
			if p.Role != burnread.RoleChained && !p.Carried {
				refs = append(refs, supplementaryRef(ns, pkg, p))
			}
		}
	}
	return refs
}

// The three bom-ref shapes, in one place because UnhashablePayloads has to produce exactly the
// refs the components carry - a second spelling would silently stop matching.
func bootstrapperRef(ns string, p burnread.Payload) string {
	return ns + "/bootstrapper/" + encodeRefPart(p.Name)
}

func packageRef(ns string, pkg burnread.Package) string {
	return ns + "/package/" + encodeRefPart(pkg.ID)
}

func looseRef(ns string, p burnread.Payload) string {
	return ns + "/payload/" + encodeRefPart(p.Name)
}

func supplementaryRef(ns string, pkg burnread.Package, p burnread.Payload) string {
	return packageRef(ns, pkg) + "/payload/" + encodeRefPart(p.Name)
}

// requireBundleDigests is the bundle's form of the rule that a document must not look more
// complete than it is.
//
// #29 D5 asks for SHA-256 on every payload, and for a carried payload that is absolute: msis
// holds the bytes, so a missing digest is a defect. A payload the engine fetches at install
// time is the one case where the bytes are genuinely not here, and refusing outright would
// make msis unable to describe any bundle that chains a downloaded runtime. Those are admitted
// on two conditions instead: the document must carry the digest the BUNDLE records - which is
// what the engine will enforce, and what a consumer can check the download against - and it
// must say in the component itself why there is no SHA-256.
func requireBundleDigests(b *burnread.Bundle, doc *Document) error {
	// Keyed by bom-ref, not by name. A package component is named after the PACKAGE while
	// its bytes are a payload with a different name, so a name-keyed lookup missed every
	// chained installer - and an installer that was carried but unhashed would have been
	// judged by the rule written for one that is not carried at all.
	ns := bundleNamespace(b)
	carried := map[string]bool{}
	for _, p := range b.UX {
		carried[bootstrapperRef(ns, p)] = p.Carried
	}
	for _, p := range b.Loose {
		carried[looseRef(ns, p)] = p.Carried
	}
	for _, pkg := range b.Packages {
		inst := pkg.Installer()
		carried[packageRef(ns, pkg)] = inst != nil && inst.Carried
		for _, p := range pkg.Payloads {
			if p.Role != burnread.RoleChained {
				carried[supplementaryRef(ns, pkg, p)] = p.Carried
			}
		}
	}

	var problems []string
	for _, c := range doc.Components {
		if hasSHA256(c.Hashes) {
			continue
		}
		switch {
		case carried[c.BOMRef]:
			problems = append(problems, fmt.Sprintf(
				"%s is carried in the bundle but has no SHA-256", c.BOMRef))
		case len(c.Hashes) == 0:
			problems = append(problems, fmt.Sprintf(
				"%s has no digest of any kind, so nothing about it can be verified", c.BOMRef))
		case !hasPropertyNamed(c.Properties, propPayloadUnavailable):
			problems = append(problems, fmt.Sprintf(
				"%s has no SHA-256 and the document does not say why", c.BOMRef))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf(
		"cannot write an SBOM for %s:\n  %s\n  a document that omits a digest without saying "+
			"so cannot be verified against an installation, so msis does not emit one",
		baseName(b.Path), strings.Join(problems, "\n  "))
}

func hasPropertyNamed(props []Property, name string) bool {
	for _, p := range props {
		if p.Name == name {
			return true
		}
	}
	return false
}

// bundleNamespace mirrors namespaceOf: the identifier that is stable across releases, so a
// bom-ref survives a version bump.
//
// PrimaryUpgradeCode is that identifier for a bundle, exactly as UpgradeCode is for an MSI. The
// bundle Code changes whenever the bundle's version does, so it would make every ref new every
// release.
func bundleNamespace(b *burnread.Bundle) string {
	if b.UpgradeCode != "" {
		return "msis/" + encodeGUID(b.UpgradeCode)
	}
	if b.Code != "" {
		return "msis/bundle-code/" + encodeGUID(b.Code)
	}
	if b.Name != "" {
		return "msis/unversioned-name/" + encodeRefPart(b.Name)
	}
	return "msis/unidentified/" + encodeRefPart(baseName(b.Path))
}

func bundleComponent(b *burnread.Bundle, ns, subject string) Component {
	name := b.Name
	if name == "" {
		name = baseName(b.Path)
	}
	c := Component{
		Type:     "application",
		BOMRef:   ns + "/bundle",
		Name:     name,
		Version:  b.Version,
		Supplier: supplierOf(b.Publisher),
		Hashes:   []Hash{{Alg: "SHA-256", Content: subject}},
	}
	if b.Name != "" && b.Version != "" {
		c.PURL = "pkg:generic/" + purlEscape(b.Name) + "@" + purlEscape(b.Version)
	}
	return c
}

// bootstrapperComponent describes one file the bootstrapper application carries. Like a
// Binary-table stream in an MSI it runs during installation and is never installed, which is a
// different thing from payload and is marked as such.
func bootstrapperComponent(p burnread.Payload, ns string) Component {
	c := Component{
		Type:        "file",
		BOMRef:      bootstrapperRef(ns, p),
		Name:        p.Name,
		Description: "bootstrapper-application payload: runs the install, not installed by it",
		Properties: []Property{
			{propRole, string(p.Role)},
			{propCarried, strconv.FormatBool(p.Carried)},
			{propIdentityUnknown, "undetermined: msis reads the bytes, not their provenance"},
		},
	}
	applyDigests(&c, p)
	return c
}

// packageComponent describes one entry of the chain: an installer the bundle runs.
//
// The bom-ref is built from the package's chain id, not its ProductCode. A ProductCode is
// regenerated on every build - the repository's own fixture documents this - so a ref derived
// from it would be new every release, which is the opposite of what a bom-ref is for. The chain
// id is authored, unique within the bundle, and stable across releases. Nor is the UpgradeCode
// usable here: msis's own auto-bundle gives all three architecture packages the same one.
//
// It carries no purl, for the same reason a file component does not. DisplayName is the bundle
// author's label for the package, not a package identity a vulnerability feed can be matched
// against, and a wrong purl produces false CVE matches and hides real ones. The identity that
// IS authoritative - ProductCode, UpgradeCode, version - is recorded as properties, and where a
// document for the chained installer exists the BOM-Link points at it.
func packageComponent(pkg burnread.Package, ns string) Component {
	name := pkg.DisplayName
	if name == "" {
		if inst := pkg.Installer(); inst != nil {
			name = inst.Name
		} else {
			name = pkg.ID
		}
	}

	c := Component{
		Type:    "application",
		BOMRef:  packageRef(ns, pkg),
		Name:    name,
		Version: pkg.Version,
		Properties: []Property{
			{propRole, string(burnread.RoleChained)},
			{propPackageID, pkg.ID},
			{propPackageKind, pkg.Kind},
			{propIdentityUnknown, "undetermined: msis reads the bytes, not their provenance"},
		},
	}
	if pkg.ProductCode != "" {
		c.Properties = append(c.Properties, Property{propProductCode, pkg.ProductCode})
	}
	if pkg.UpgradeCode != "" {
		c.Properties = append(c.Properties, Property{propUpgradeCode, pkg.UpgradeCode})
	}
	if pkg.InstallCondition != "" {
		// What is in the bundle and what lands on a machine are different things. The
		// condition is recorded so a consumer can tell which of three architecture packages
		// a given machine would actually have installed.
		c.Properties = append(c.Properties,
			Property{propInstallCondition, pkg.InstallCondition})
	}
	if inst := pkg.Installer(); inst != nil {
		c.Properties = append(c.Properties,
			Property{propCarried, strconv.FormatBool(inst.Carried)})
		applyDigests(&c, *inst)
	} else {
		c.Properties = append(c.Properties, Property{propPayloadUnavailable,
			"the manifest declares no payload for this package, so there are no bytes to hash"})
	}
	return c
}

// loosePayloadComponent describes a payload the bundle carries that no chain package claims.
// A LayoutOnly file is the usual case: it ships inside the bundle and is written out beside it
// in layout mode, so it is part of what was distributed even though nothing installs it.
func loosePayloadComponent(p burnread.Payload, ns string) Component {
	description := "carried by the bundle; referenced by no chain package"
	if p.LayoutOnly {
		description = "layout-only payload: written out beside the bundle, never installed"
	}
	c := Component{
		Type:        "file",
		BOMRef:      looseRef(ns, p),
		Name:        p.Name,
		Description: description,
		Properties: []Property{
			{propRole, string(p.Role)},
			{propCarried, strconv.FormatBool(p.Carried)},
			{propIdentityUnknown, "undetermined: msis reads the bytes, not their provenance"},
		},
	}
	applyDigests(&c, p)
	return c
}

func supplementaryComponent(p burnread.Payload, pkg burnread.Package, ns string) Component {
	c := Component{
		Type:        "file",
		BOMRef:      supplementaryRef(ns, pkg, p),
		Name:        p.Name,
		Description: "supplementary payload of chain package " + pkg.ID,
		Properties: []Property{
			{propRole, string(p.Role)},
			{propPackageID, pkg.ID},
			{propCarried, strconv.FormatBool(p.Carried)},
			{propIdentityUnknown, "undetermined: msis reads the bytes, not their provenance"},
		},
	}
	applyDigests(&c, p)
	return c
}

// applyDigests puts on a component every digest there is for a payload, and - when there is no
// SHA-256 - says why in the component itself rather than leaving a reader to infer it.
func applyDigests(c *Component, p burnread.Payload) {
	if p.SHA256 != "" {
		c.Hashes = append(c.Hashes, Hash{Alg: "SHA-256", Content: p.SHA256})
	}
	if p.RecordedSHA512 != "" {
		c.Hashes = append(c.Hashes, Hash{Alg: "SHA-512", Content: strings.ToLower(p.RecordedSHA512)})
	}
	if p.SHA256 != "" {
		return
	}
	reason := p.Unavailable
	if reason == "" {
		reason = "msis could not read the payload's bytes from the bundle"
	}
	c.Properties = append(c.Properties, Property{propPayloadUnavailable, reason})
	if p.DownloadURL != "" {
		c.Properties = append(c.Properties, Property{propDownloadURL, p.DownloadURL})
	}
}

// childLink looks for a document beside the bundle that describes a chained installer, and
// returns a BOM-Link to it only when it provably does.
//
// The proof is the subject digest: a document's metadata.component carries the SHA-256 of the
// artifact it describes, and the bundle carries the bytes it chained. A link is emitted when
// those agree and not otherwise - a document for the same product at the same version may
// still be a different build, and a link that resolves to the wrong build is worse than none,
// because it looks authoritative.
//
// The second return value is the reason there is no link, and is empty when there is one.
func childLink(dir string, inst *burnread.Payload) (*ExternalReference, string) {
	if inst == nil {
		return nil, "no link: the manifest declares no installer payload for this package"
	}
	if !inst.Carried || inst.SHA256 == "" {
		return nil, "no link: the bundle does not carry this payload, so no document can be " +
			"matched to the bytes that will actually be installed"
	}

	artifact, err := childArtifactPath(dir, inst.Name)
	if err != nil {
		return nil, "no link: " + err.Error()
	}
	path := SidecarPath(artifact)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Sprintf("no link: no document at %s", baseName(path))
	}
	if err != nil {
		return nil, fmt.Sprintf("no link: %s could not be read: %v", baseName(path), err)
	}

	var child struct {
		SerialNumber string `json:"serialNumber"`
		Version      int    `json:"version"`
		Metadata     struct {
			Component struct {
				Hashes []Hash `json:"hashes"`
			} `json:"component"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(data, &child); err != nil {
		return nil, fmt.Sprintf("no link: %s is not readable as a CycloneDX document: %v",
			baseName(path), err)
	}

	// The serial goes into the link, so it is validated exactly as it is before going into a
	// filename. A document is untrusted input wherever it came from.
	if !serialPattern.MatchString(child.SerialNumber) {
		return nil, fmt.Sprintf(
			"no link: %s carries serial %q, which is not a well-formed urn:uuid",
			baseName(path), child.SerialNumber)
	}
	if child.Version < 1 {
		return nil, fmt.Sprintf(
			"no link: %s declares version %d; a BOM-Link addresses a specific version",
			baseName(path), child.Version)
	}

	subject := ""
	for _, h := range child.Metadata.Component.Hashes {
		if strings.EqualFold(h.Alg, "SHA-256") {
			subject = strings.ToLower(h.Content)
			break
		}
	}
	switch {
	case subject == "":
		return nil, fmt.Sprintf(
			"no link: %s records no SHA-256 for its subject, so it cannot be matched to the "+
				"bytes this bundle carries", baseName(path))
	case !isSHA256(subject):
		// Length-checked before it is compared, let alone abbreviated. The content comes
		// from a file on disk that msis did not necessarily write, and a two-character
		// digest reached a %s...-style diagnostic that sliced it.
		return nil, fmt.Sprintf(
			"no link: %s records %q as its subject SHA-256, which is not one",
			baseName(path), subject)
	case subject != strings.ToLower(inst.SHA256):
		return nil, fmt.Sprintf(
			"no link: %s describes an artifact with SHA-256 %s..., but this bundle carries "+
				"%s...; it is a different build",
			baseName(path), abbreviate(subject), abbreviate(inst.SHA256))
	}

	// The hashes on an external reference describe the REFERENCE, not the thing that made it
	// worth referencing - so this is the digest of the document being linked to, and the
	// installer's own digest stays on the component. Putting the installer's digest here
	// would hand a consumer checking the linked JSON a value that cannot match it.
	sum := sha256.Sum256(data)

	return &ExternalReference{
		Type: "bom",
		// The CycloneDX BOM-Link form: urn:cdx:<serial without its urn:uuid prefix>/<version>.
		URL: "urn:cdx:" + strings.TrimPrefix(child.SerialNumber, "urn:uuid:") +
			"/" + strconv.Itoa(child.Version),
		Comment: "the SBOM for this chained installer, matched to the bytes carried here by " +
			"its subject SHA-256",
		Hashes: []Hash{{Alg: "SHA-256", Content: hex.EncodeToString(sum[:])}},
	}, ""
}

// isSHA256 reports whether s is a 64-digit lowercase hex digest. Anything read out of a
// document is checked before it is compared or abbreviated.
func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// abbreviate shortens a digest for a diagnostic without assuming it is long enough to shorten.
func abbreviate(s string) string {
	if len(s) <= 16 {
		return s
	}
	return s[:16]
}

// childArtifactPath resolves a payload's manifest name against the bundle's directory.
//
// The name comes out of the artifact, so it is untrusted: a manifest can spell a payload
// "..\..\something", and interpolating that into a path would read - and, through SidecarPath,
// name - a file outside the directory. The result is required to stay under dir, checked after
// joining so no amount of traversal in the input escapes.
func childArtifactPath(dir, name string) (string, error) {
	if name == "" {
		return "", errors.New("the manifest gives this payload no file name")
	}
	if filepath.IsAbs(name) || strings.ContainsAny(name, ":") {
		return "", fmt.Errorf("the payload name %q is not relative to the bundle", name)
	}
	target := filepath.Join(dir, filepath.Clean(name))
	rel, err := filepath.Rel(dir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("the payload name %q points outside the bundle's directory", name)
	}
	return target, nil
}

// FindBySerial locates the document with a given serial in a directory: the current sidecar if
// it still carries that serial, otherwise the copy Write archived under it.
//
// This is the readback half of the retention Write performs. A BOM-Link addresses a serial, not
// a filename, so "does this link still resolve" is answered by searching - and being able to
// answer it is the whole reason the previous document is kept.
func FindBySerial(dir, serial string) (string, error) {
	if !serialPattern.MatchString(serial) {
		return "", fmt.Errorf("%q is not a well-formed urn:uuid", serial)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cdx.json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var partial struct {
			SerialNumber string `json:"serialNumber"`
		}
		if err := json.Unmarshal(data, &partial); err != nil {
			continue
		}
		if partial.SerialNumber == serial {
			return path, nil
		}
	}
	return "", fmt.Errorf("no document in %s carries serial %s", dir, serial)
}

// ParseBOMLink splits a BOM-Link back into the serial and version it addresses, so a link can
// be followed.
func ParseBOMLink(link string) (serial string, version int, err error) {
	rest, ok := strings.CutPrefix(link, "urn:cdx:")
	if !ok {
		return "", 0, fmt.Errorf("%q is not a BOM-Link", link)
	}
	uuid, v, ok := strings.Cut(rest, "/")
	if !ok {
		return "", 0, fmt.Errorf("BOM-Link %q names no version", link)
	}
	// A link may carry a #bom-ref fragment addressing one component inside the document.
	v, _, _ = strings.Cut(v, "#")
	version, err = strconv.Atoi(v)
	if err != nil {
		return "", 0, fmt.Errorf("BOM-Link %q has a non-numeric version: %w", link, err)
	}
	return "urn:uuid:" + uuid, version, nil
}

// bundleCoverage states where the inventory stops, in the document as well as the terminal.
func bundleCoverage(b *burnread.Bundle) string {
	note := "the bootstrapper application's payloads and the chained packages, read from the " +
		"bundle; a chained installer's own contents are described by that installer's own " +
		"document, linked where one could be matched to the bytes carried here; install " +
		"conditions decide which chained packages a given machine actually installs"
	var absent []string
	for _, p := range b.AllPayloads() {
		if !p.Carried {
			absent = append(absent, p.Name)
		}
	}
	if len(absent) > 0 {
		note += "; additionally, these payloads are not carried in the bundle and could not be " +
			"hashed from it: " + strings.Join(absent, ", ")
	}
	return note
}

// BOMLink reports a component's BOM-Link and, when there is none, the reason recorded for why
// it was not made. It lives here because the property vocabulary does: a caller reading a
// document back should not have to spell msis's property names itself.
func BOMLink(c Component) (link, why string) {
	for _, r := range c.ExternalReferences {
		if r.Type == "bom" {
			link = r.URL
		}
	}
	for _, p := range c.Properties {
		if p.Name == propBOMLink {
			why = strings.TrimPrefix(p.Value, "no link: ")
		}
	}
	return link, why
}
