package sbom

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gersonkurz/msis/internal/msiread"
)

// Options carries what the document needs but the package cannot supply.
//
// Now and NewSerial are injectable so a test can pin the only two fields that legitimately vary
// between documents describing identical inputs; nothing else in the document may depend on
// them.
type Options struct {
	MsisVersion string
	MsisPath    string // the running binary, hashed into metadata.tools

	Now       func() time.Time
	NewSerial func() (string, error)
}

func (o *Options) withDefaults() {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.NewSerial == nil {
		o.NewSerial = newSerialNumber
	}
	if o.MsisVersion == "" {
		o.MsisVersion = "unknown"
	}
}

// FromPackage builds a document describing one installer.
func FromPackage(pkg *msiread.Package, opts Options) (*Document, error) {
	opts.withDefaults()

	serial, err := opts.NewSerial()
	if err != nil {
		return nil, fmt.Errorf("generating a serial number: %w", err)
	}
	subject, err := hashFile(pkg.Path)
	if err != nil {
		return nil, fmt.Errorf("hashing the subject artifact: %w", err)
	}

	ns := namespaceOf(pkg)
	root := rootComponent(pkg, ns, subject)

	doc := &Document{
		BOMFormat:    "CycloneDX",
		SpecVersion:  "1.6",
		SerialNumber: serial,
		Version:      1,
		Metadata: Metadata{
			// The one wall clock in the document. NTIA's minimum elements require a
			// timestamp; determinism is preserved by confining variation to this field and
			// the serial number, and by the canonical diff that excludes exactly those two.
			Timestamp: opts.Now().UTC().Format(time.RFC3339),
			Tools:     Tools{Components: []Component{msisTool(opts)}},
			Component: root,
			Supplier:  supplierOf(pkg.Properties["Manufacturer"]),
		},
	}

	if v := pkg.Properties["ProductCode"]; v != "" {
		doc.Metadata.Properties = append(doc.Metadata.Properties, Property{propProductCode, v})
	}
	if v := pkg.Properties["UpgradeCode"]; v != "" {
		doc.Metadata.Properties = append(doc.Metadata.Properties, Property{propUpgradeCode, v})
	}
	doc.Metadata.Properties = append(doc.Metadata.Properties,
		Property{propSubjectArtifact, baseName(pkg.Path)},
		Property{propCoverage, coverageNote(pkg)},
	)

	// Explicitly empty, not nil: a nil slice marshals as JSON null, and both "components"
	// and "dependsOn" are declared as arrays. A registry-only installer with no files and no
	// Binary streams reaches exactly this path and would have produced an invalid document.
	doc.Components = []Component{}
	payloadRefs := []string{}

	guidOf := map[string]string{}
	for _, c := range pkg.Components {
		guidOf[c.ID] = c.GUID
	}

	for _, f := range pkg.Files {
		c := fileComponent(f, ns, guidOf[f.Component])
		doc.Components = append(doc.Components, c)
		payloadRefs = append(payloadRefs, c.BOMRef)
	}
	for _, b := range pkg.Binaries {
		c := binaryComponent(b, ns)
		doc.Components = append(doc.Components, c)
		payloadRefs = append(payloadRefs, c.BOMRef)
	}

	// The package contains these: a known relationship, so it is stated as one. No dependency
	// entry is emitted for the payload components themselves - what THEY depend on is unknown,
	// and an entry with an empty dependsOn would assert they depend on nothing.
	doc.Dependencies = []Dependency{{Ref: root.BOMRef, DependsOn: payloadRefs}}

	// Coverage, naming the affected components explicitly - a completeness declaration does
	// not cascade through containment.
	//
	// assemblies and dependencies are separate fields in the schema and mean different things:
	// assemblies says how completely the thing itself is described, dependencies says how
	// completely its dependency graph is known. An opaque payload needs BOTH - msis has its
	// bytes but not its contents, and knows nothing at all about what it depends on.
	doc.Compositions = []Composition{
		// The product itself cannot be described completely - the provenance of its payload
		// is not knowable from the artifact - but what it CONTAINS is known exactly, and the
		// two are separate claims. Saying only the first would leave the root's dependency
		// graph unstated, which reads as "nobody checked" rather than "this is all of it".
		{Aggregate: aggregateIncomplete, Assemblies: []string{root.BOMRef}},
		{Aggregate: aggregateComplete, Dependencies: []string{root.BOMRef}},
	}
	if len(payloadRefs) > 0 {
		doc.Compositions = append(doc.Compositions, Composition{
			Aggregate:    aggregateUnknown,
			Assemblies:   payloadRefs,
			Dependencies: payloadRefs,
		})
	}

	sortDocument(doc)

	// #29 D5 admits no exceptions: the digest is what makes a document verifiable against a
	// customer's installation, so a document without one for every payload does not go out.
	// The reader deliberately returns unhashed files when a cabinet did not travel with the
	// package (#31); that is a reason the ARTIFACT cannot be described, not a licence to
	// publish a document that looks complete. Refusing here, before Write touches anything,
	// also means no existing sidecar is disturbed by a run that was never going to succeed.
	if err := requireDigests(pkg, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// requireDigests refuses a document whose payload is not fully hashed, naming the reason the
// reader gave where there is one.
func requireDigests(pkg *msiread.Package, doc *Document) error {
	var missing []string
	for _, c := range doc.Components {
		if !hasSHA256(c.Hashes) {
			missing = append(missing, c.Name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)

	reason := ""
	for _, m := range pkg.Media {
		if m.Unavailable != "" {
			reason = "; " + m.Unavailable
			break
		}
	}
	return fmt.Errorf(
		"cannot write an SBOM for %s: %d payload component(s) have no SHA-256 (%s)%s"+
			"\n  an SBOM without a digest for every payload cannot be verified against an "+
			"installation, so msis does not emit one",
		baseName(pkg.Path), len(missing), strings.Join(missing, ", "), reason)
}

func hasSHA256(hashes []Hash) bool {
	for _, h := range hashes {
		if strings.EqualFold(h.Alg, "SHA-256") && len(h.Content) == 64 {
			return true
		}
	}
	return false
}

// rootComponent is the product, carrying the digest of the artifact this document describes.
// A BOM-Link that resolves can still point at the wrong build of the same name and version;
// the subject digest is what makes the link checkable.
func rootComponent(pkg *msiread.Package, ns, subject string) Component {
	name := pkg.Properties["ProductName"]
	if name == "" {
		name = baseName(pkg.Path)
	}
	version := pkg.Properties["ProductVersion"]

	c := Component{
		Type:     "application",
		BOMRef:   ns + "/product",
		Name:     name,
		Version:  version,
		Supplier: supplierOf(pkg.Properties["Manufacturer"]),
		Hashes:   []Hash{{Alg: "SHA-256", Content: subject}},
	}
	// The product's identity is read out of the package rather than guessed, so it may carry
	// a purl. No payload component does - see fileComponent.
	if name != "" && version != "" {
		c.PURL = "pkg:generic/" + purlEscape(name) + "@" + purlEscape(version)
	}
	return c
}

// fileComponent describes one installed file.
//
// It carries no purl. msis can see the bytes and where they land; it cannot see what they were
// built from, and a wrong purl produces false CVE matches and hides real ones. The identity is
// recorded as unknown instead, and the digest is what a consumer can actually act on.
func fileComponent(f msiread.File, ns, componentGUID string) Component {
	c := Component{
		Type:    "file",
		BOMRef:  fileRef(ns, f, componentGUID),
		Name:    f.Name,
		Version: f.Version,
		Properties: []Property{
			{propRole, rolePayload},
			{propInstallTarget, f.Target},
			{propFileKey, f.ID},
			{propComponentID, f.Component},
			{propSequence, strconv.Itoa(f.Sequence)},
			{propIdentityUnknown, "undetermined: msis reads the bytes, not their provenance"},
		},
	}
	if componentGUID != "" {
		c.Properties = append(c.Properties, Property{propComponentGUID, componentGUID})
	}
	if f.SHA256 != "" {
		c.Hashes = []Hash{{Alg: "SHA-256", Content: f.SHA256}}
	}
	return c
}

// binaryComponent describes a Binary-table stream: a custom action or UI resource held in the
// database. It is executed during installation and never installed, which is a different thing
// from payload and is marked as such.
func binaryComponent(b msiread.Binary, ns string) Component {
	return Component{
		Type:        "file",
		BOMRef:      ns + "/binary/" + encodeRefPart(b.Name),
		Name:        b.Name,
		Description: "Binary-table stream: executed during installation, not installed",
		Hashes:      []Hash{{Alg: "SHA-256", Content: b.SHA256}},
		Properties: []Property{
			{propRole, roleBinary},
			{propIdentityUnknown, "undetermined: msis reads the bytes, not their provenance"},
		},
	}
}

// namespaceOf gives the document's identity namespace.
//
// The UpgradeCode is used because that is precisely what it is for: Windows Installer defines it
// as the identifier that stays the same across a product's releases, which is what a bom-ref has
// to be to survive a version bump. ProductCode changes per build and would make every ref new
// every release. A package without an UpgradeCode falls back to its product name, which is
// weaker - it is not guaranteed unique - so the fallback is stated rather than silent.
func namespaceOf(pkg *msiread.Package) string {
	if up := pkg.Properties["UpgradeCode"]; up != "" {
		return "msis/" + encodeGUID(up)
	}
	if name := pkg.Properties["ProductName"]; name != "" {
		return "msis/unversioned-name/" + encodeRefPart(name)
	}
	return "msis/unidentified/" + encodeRefPart(baseName(pkg.Path))
}

// fileRef derives a payload component's bom-ref.
//
// Target alone is not enough: two features may install different files to the same destination,
// which this repository already supports and tests (TestDuplicateTargetFilesGetShortName). The
// MSI component discriminates them, and its GUID is used in preference to its key because
// Windows Installer requires the GUID to be stable for a component across releases while the
// key may be generated.
//
// The discriminator is ALWAYS included, not only when a collision exists: appending it
// conditionally would change a file's ref the day a second file arrived at its destination,
// which is exactly the cross-version stability the ref is supposed to provide.
//
// The version is excluded throughout, so the same file in 4.1 and 4.2 carries the same ref.
func fileRef(ns string, f msiread.File, componentGUID string) string {
	// The encoding depends on WHAT the discriminator is, not merely on it being a string. A
	// GUID is case-insensitive and brace-wrapped by convention, so it is normalised; an MSI
	// identifier is case-SENSITIVE, so it is not. Normalising both alike made the GUID-less
	// components "C_A" and "c_a" - two different components - produce one ref.
	switch {
	case componentGUID != "":
		return ns + "/file/" + encodeTarget(f.Target) + "#" + encodeGUID(componentGUID)
	case f.Component != "":
		return ns + "/file/" + encodeTarget(f.Target) + "#" + encodeRefPart(f.Component)
	default:
		// Nothing to discriminate with. The file key is not stable across authoring changes,
		// so this is the weakest case; it is used rather than producing a colliding ref.
		return ns + "/file/" + encodeTarget(f.Target) + "#" + encodeRefPart(f.ID)
	}
}

// encodeRefPart makes a ref segment safe to put in an identifier without merging two things
// that are different.
//
// Percent-encoding, not substitution. Replacing spaces with hyphens collapsed
// "[INSTALLDIR]a b.dll" and "[INSTALLDIR]a-b.dll" onto one ref - two genuinely different
// destinations, and within a single MSI component the discriminator cannot tell them apart
// either. An encoding that is reversible cannot do that.
func encodeRefPart(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-', c == '~':
			b.WriteByte(c)
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02x", c)
		}
	}
	return b.String()
}

// encodeTarget encodes an install path. Lowercased first, because Windows paths are
// case-insensitive and a casing change is not a change of identity - unlike the substitutions
// this replaced, lowercasing merges only things that really are the same file.
func encodeTarget(target string) string {
	return encodeRefPart(strings.ToLower(target))
}

// encodeGUID normalises a GUID and nothing else. Brace-stripping is applied here rather than to
// every ref segment: braces are decoration on a GUID and meaningful in a filename.
func encodeGUID(g string) string {
	g = strings.TrimSpace(g)
	if len(g) > 1 && g[0] == '{' && g[len(g)-1] == '}' {
		g = g[1 : len(g)-1]
	}
	return encodeRefPart(strings.ToLower(g))
}

// purlEscape percent-encodes a purl name or version segment.
//
// Every character outside the unreserved set is encoded, not a hand-picked few: a product named
// "Tool#Pro" produced "pkg:generic/Tool#Pro@1.0", where the "#" begins a subpath and the
// identity silently became something else. "%", "?" and "/" have the same problem.
func purlEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '-', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02x", c)
		}
	}
	return b.String()
}

func supplierOf(name string) *Supplier {
	if name == "" {
		return nil
	}
	return &Supplier{Name: name}
}

// msisTool identifies what produced the document - and hashes it, because the generator is part
// of the build chain and an auditor will ask which one ran.
func msisTool(opts Options) Component {
	c := Component{
		Type:     "application",
		Name:     "msis",
		Version:  opts.MsisVersion,
		Supplier: &Supplier{Name: "NG Branch Technology GmbH"},
	}
	if opts.MsisPath != "" {
		if sum, err := hashFile(opts.MsisPath); err == nil {
			c.Hashes = []Hash{{Alg: "SHA-256", Content: sum}}
		}
	}
	return c
}

// coverageNote states in the document what the inventory does not cover, so a consumer reading
// only the JSON can tell where it stops.
func coverageNote(pkg *msiread.Package) string {
	note := "payload and installation-time metadata read from the artifact; the provenance of " +
		"payload bytes is not determined, and install-time conditions decide what actually " +
		"lands on a machine"
	for _, m := range pkg.Media {
		if m.Unavailable != "" {
			return note + "; additionally: " + m.Unavailable
		}
	}
	return note
}

// newSerialNumber returns a fresh RFC 4122 version 4 UUID as a urn, which is what CycloneDX's
// serialNumber pattern requires. A new one per document is deliberate: the serial identifies the
// DOCUMENT, while bom-refs identify the things it describes.
func newSerialNumber() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func baseName(path string) string {
	if i := strings.LastIndexAny(path, `\/`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// sortDocument imposes a total order on every collection. MSI returns rows in no guaranteed
// order and Go maps iterate randomly, so without this two reads of one package would produce
// documents that differ in ways that have nothing to do with the package.
func sortDocument(d *Document) {
	sort.Slice(d.Components, func(i, j int) bool { return d.Components[i].BOMRef < d.Components[j].BOMRef })
	for i := range d.Components {
		sortProperties(d.Components[i].Properties)
		sort.Slice(d.Components[i].ExternalReferences, func(a, b int) bool {
			return d.Components[i].ExternalReferences[a].URL <
				d.Components[i].ExternalReferences[b].URL
		})
	}
	sortProperties(d.Metadata.Properties)
	sortProperties(d.Metadata.Component.Properties)
	for i := range d.Metadata.Tools.Components {
		sortProperties(d.Metadata.Tools.Components[i].Properties)
	}
	for i := range d.Dependencies {
		sort.Strings(d.Dependencies[i].DependsOn)
	}
	sort.Slice(d.Dependencies, func(i, j int) bool { return d.Dependencies[i].Ref < d.Dependencies[j].Ref })
	for i := range d.Compositions {
		sort.Strings(d.Compositions[i].Assemblies)
		sort.Strings(d.Compositions[i].Dependencies)
	}
	sort.Slice(d.Compositions, func(i, j int) bool {
		if d.Compositions[i].Aggregate != d.Compositions[j].Aggregate {
			return d.Compositions[i].Aggregate < d.Compositions[j].Aggregate
		}
		return strings.Join(d.Compositions[i].Assemblies, ",") <
			strings.Join(d.Compositions[j].Assemblies, ",")
	})
}

func sortProperties(p []Property) {
	sort.Slice(p, func(i, j int) bool {
		if p[i].Name != p[j].Name {
			return p[i].Name < p[j].Name
		}
		return p[i].Value < p[j].Value
	})
}
