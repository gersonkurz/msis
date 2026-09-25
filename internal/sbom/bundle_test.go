package sbom

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/burnread"
)

// syntheticBundle is the shape a bundle reader produces: a bootstrapper application, one
// chained installer whose bytes are carried, and one the engine fetches at install time. The
// second is the case msis's own bundles do not have and every bundle chaining a downloaded
// runtime does.
func syntheticBundle(path string) *burnread.Bundle {
	return &burnread.Bundle{
		Path:          path,
		Code:          "{AAAAAAAA-1111-4111-8111-111111111111}",
		UpgradeCode:   "{BBBBBBBB-2222-4222-8222-222222222222}",
		Name:          "Example Suite",
		Version:       "4.1.0",
		Publisher:     "Someone",
		Scope:         "perMachine",
		EngineVersion: "7.0.0.0",
		UX: []burnread.Payload{{
			ID: "BA", Name: "ba.exe", Size: 3, Role: burnread.RoleBootstrapper,
			Carried: true, SHA256: strings.Repeat("a", 64),
			RecordedSHA512: strings.Repeat("f", 128),
		}},
		Packages: []burnread.Package{
			{
				ID: "Main", Kind: "MsiPackage", DisplayName: "Example", Version: "4.1.0",
				ProductCode: "{CCCCCCCC-3333-4333-8333-333333333333}",
				UpgradeCode: "{BBBBBBBB-2222-4222-8222-222222222222}",
				Payloads: []burnread.Payload{
					{
						ID: "Main", Name: "Example.msi", Size: 10,
						Role: burnread.RoleChained, Carried: true,
						SHA256: strings.Repeat("b", 64), SHA512: strings.Repeat("b", 128),
					},
					{
						ID: "Extra", Name: "extra.cab", Size: 5,
						Role: burnread.RoleSupplementary, Carried: true,
						SHA256: strings.Repeat("c", 64), SHA512: strings.Repeat("c", 128),
					},
				},
			},
			{
				ID: "Runtime", Kind: "ExePackage", DisplayName: "Some Runtime",
				InstallCondition: "NOT RuntimeInstalled",
				Payloads: []burnread.Payload{{
					ID: "Runtime", Name: "runtime.exe", Size: 0,
					Role: burnread.RoleChained, Carried: false,
					RecordedSHA512: strings.Repeat("e", 128),
					DownloadURL:    "https://example.invalid/runtime.exe",
					Unavailable: "runtime.exe is not carried in the bundle: the engine " +
						"downloads it from https://example.invalid/runtime.exe at install time",
				}},
			},
		},
	}
}

// writeArtifact puts a stand-in file where a bundle or installer would be, so the subject
// digest and the sidecar paths are real.
func writeArtifact(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func bundleDoc(t *testing.T, b *burnread.Bundle) *Document {
	t.Helper()
	doc, err := FromBundle(b, Options{MsisVersion: "test"})
	if err != nil {
		t.Fatalf("FromBundle: %v", err)
	}
	return doc
}

func componentByRef(doc *Document, ref string) *Component {
	for i := range doc.Components {
		if doc.Components[i].BOMRef == ref {
			return &doc.Components[i]
		}
	}
	return nil
}

// A bundle's document inventories what the bundle holds: the bootstrapper's payloads, every
// chained installer, and every supplementary payload. Anything the artifact carries and the
// document omits is silent incompleteness, which is the failure this package exists to prevent.
func TestBundleDocumentInventoriesEverythingTheBundleHolds(t *testing.T) {
	dir := t.TempDir()
	b := syntheticBundle(writeArtifact(t, dir, "setup.exe", "bundle"))
	doc := bundleDoc(t, b)

	want := map[string]string{
		"msis/bbbbbbbb-2222-4222-8222-222222222222/bundle":                         "Example Suite",
		"msis/bbbbbbbb-2222-4222-8222-222222222222/bootstrapper/ba.exe":            "ba.exe",
		"msis/bbbbbbbb-2222-4222-8222-222222222222/package/Main":                   "Example",
		"msis/bbbbbbbb-2222-4222-8222-222222222222/package/Main/payload/extra.cab": "extra.cab",
		"msis/bbbbbbbb-2222-4222-8222-222222222222/package/Runtime":                "Some Runtime",
	}
	// The root is metadata.component, not a members of components[].
	rootRef := "msis/bbbbbbbb-2222-4222-8222-222222222222/bundle"
	if doc.Metadata.Component.BOMRef != rootRef {
		t.Errorf("root ref = %q, want %q", doc.Metadata.Component.BOMRef, rootRef)
	}
	delete(want, rootRef)

	got := map[string]string{}
	for _, c := range doc.Components {
		got[c.BOMRef] = c.Name
	}
	for ref, name := range want {
		if got[ref] != name {
			t.Errorf("component %q = %q, want %q", ref, got[ref], name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("%d components, want %d: %v", len(got), len(want), got)
	}
}

// The bom-ref has to survive a version bump, which rules out the ProductCode: Windows
// Installer regenerates it on every build, so a ref derived from it would be new every release.
// The UpgradeCode is no use either here - msis's own auto-bundle gives all three architecture
// packages the same one - so the chain id is the discriminator.
func TestPackageRefsSurviveARebuild(t *testing.T) {
	dir := t.TempDir()
	path := writeArtifact(t, dir, "setup.exe", "bundle")

	before := bundleDoc(t, syntheticBundle(path))

	// A rebuild: new ProductCode, new bundle Code, new payload bytes, same authored ids.
	rebuilt := syntheticBundle(path)
	rebuilt.Code = "{99999999-9999-4999-8999-999999999999}"
	rebuilt.Version = "4.2.0"
	rebuilt.Packages[0].ProductCode = "{DDDDDDDD-4444-4444-8444-444444444444}"
	rebuilt.Packages[0].Version = "4.2.0"
	rebuilt.Packages[0].Payloads[0].SHA256 = strings.Repeat("9", 64)
	after := bundleDoc(t, rebuilt)

	refs := func(d *Document) []string {
		var out []string
		for _, c := range d.Components {
			out = append(out, c.BOMRef)
		}
		return out
	}
	b, a := refs(before), refs(after)
	if strings.Join(b, "\n") != strings.Join(a, "\n") {
		t.Errorf("a rebuild changed the bom-refs:\n before %v\n after  %v", b, a)
	}

	// ...and the version and digest, which SHOULD change, did.
	pkg := componentByRef(after, "msis/bbbbbbbb-2222-4222-8222-222222222222/package/Main")
	if pkg == nil || pkg.Version != "4.2.0" {
		t.Errorf("the rebuilt package component did not pick up the new version: %+v", pkg)
	}
	// The ProductCode is still recorded - it is not an identity for the ref, but it is a
	// fact about the build and a consumer needs it to match an installed product.
	if !hasPropertyValue(pkg.Properties, propProductCode, "{DDDDDDDD-4444-4444-8444-444444444444}") {
		t.Errorf("the new ProductCode was not recorded: %+v", pkg.Properties)
	}
}

func hasHash(hashes []Hash, content string) bool {
	for _, h := range hashes {
		if h.Content == content {
			return true
		}
	}
	return false
}

func hasPropertyValue(props []Property, name, value string) bool {
	for _, p := range props {
		if p.Name == name && p.Value == value {
			return true
		}
	}
	return false
}

func propertyValue(props []Property, name string) string {
	for _, p := range props {
		if p.Name == name {
			return p.Value
		}
	}
	return ""
}

// A payload the bundle does not carry is the one case where msis cannot produce a SHA-256. It
// must still be inventoried, must carry the digest the bundle records - which is what the
// engine enforces on the download - and must say in the component why the SHA-256 is missing.
// Dropping it, or emitting it bare, are both worse than saying so.
func TestAPayloadTheBundleDoesNotCarryIsStillDescribed(t *testing.T) {
	dir := t.TempDir()
	b := syntheticBundle(writeArtifact(t, dir, "setup.exe", "bundle"))
	doc := bundleDoc(t, b)

	c := componentByRef(doc, "msis/bbbbbbbb-2222-4222-8222-222222222222/package/Runtime")
	if c == nil {
		t.Fatal("the downloaded runtime is missing from the document entirely")
	}
	if hasSHA256(c.Hashes) {
		t.Error("msis reported a SHA-256 for bytes it does not have")
	}
	var sha512 string
	for _, h := range c.Hashes {
		if strings.EqualFold(h.Alg, "SHA-512") {
			sha512 = h.Content
		}
	}
	if sha512 == "" {
		t.Error("the digest the bundle records is the only one there is, and it was dropped")
	}
	if v := propertyValue(c.Properties, propPayloadUnavailable); !strings.Contains(v, "downloads it from") {
		t.Errorf("%s = %q, want it to say the engine downloads the payload", propPayloadUnavailable, v)
	}
	if v := propertyValue(c.Properties, propDownloadURL); v != "https://example.invalid/runtime.exe" {
		t.Errorf("%s = %q, want the download URL", propDownloadURL, v)
	}
	// #63: BSI maps the deployable form's SHA-512 to a distribution reference. This payload has
	// a real distribution location, so it gets one - carrying the digest the engine enforces.
	var dist []ExternalReference
	for _, r := range c.ExternalReferences {
		if r.Type == "distribution" {
			dist = append(dist, r)
		}
	}
	if len(dist) != 1 || dist[0].URL != "https://example.invalid/runtime.exe" ||
		len(dist[0].Hashes) != 1 || dist[0].Hashes[0].Alg != "SHA-512" || dist[0].Hashes[0].Content != sha512 {
		t.Errorf("distribution references %+v, want one at the download URL with the recorded SHA-512", dist)
	}
	if v := propertyValue(c.Properties, propCarried); v != "false" {
		t.Errorf("%s = %q, want false", propCarried, v)
	}

	// And the coverage note says it at document level, so a consumer reading only the JSON
	// header learns where the inventory stops.
	if v := propertyValue(doc.Metadata.Properties, propCoverage); !strings.Contains(v, "runtime.exe") {
		t.Errorf("the coverage note does not name the payload that could not be hashed: %q", v)
	}
}

// The rule the document must never break: a payload msis DOES hold must carry its SHA-256. A
// missing digest there is a defect, not a limitation, and the document must not go out.
func TestACarriedPayloadWithoutADigestRefusesToEmit(t *testing.T) {
	dir := t.TempDir()
	b := syntheticBundle(writeArtifact(t, dir, "setup.exe", "bundle"))
	b.Packages[0].Payloads[0].SHA256 = "" // carried, but unhashed

	_, err := FromBundle(b, Options{MsisVersion: "test"})
	if err == nil {
		t.Fatal("a document missing a digest for a payload msis holds must not be emitted")
	}
	for _, want := range []string{"package/Main", "carried in the bundle but has no SHA-256"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

// UnhashablePayloads is what a conformance check is told. Deriving it from the document would
// make that check vacuous - a component whose digest the emitter dropped would simply be
// declared exempt - so it is derived from the bundle, and this pins that.
func TestUnhashablePayloadsComesFromTheArtifact(t *testing.T) {
	dir := t.TempDir()
	b := syntheticBundle(writeArtifact(t, dir, "setup.exe", "bundle"))

	want := []string{"msis/bbbbbbbb-2222-4222-8222-222222222222/package/Runtime"}
	if got := UnhashablePayloads(b); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("UnhashablePayloads = %v, want %v", got, want)
	}

	// The refs it produces must be the ones the components actually carry, or the exemption
	// would name something that is not in the document.
	doc := bundleDoc(t, b)
	for _, ref := range UnhashablePayloads(b) {
		if componentByRef(doc, ref) == nil {
			t.Errorf("UnhashablePayloads named %q, which is not a component in the document", ref)
		}
	}
}

// --- BOM-Link -------------------------------------------------------------------------------

// childDocument writes a document beside an artifact the way /SBOM would, and returns its
// serial. Built through the real emitter so the test cannot drift from the format.
func childDocument(t *testing.T, path, sha256 string) string {
	t.Helper()
	doc := &Document{
		BOMFormat: "CycloneDX", SpecVersion: "1.6", Version: 1,
		SerialNumber: "urn:uuid:11111111-1111-4111-8111-111111111111",
		Metadata: Metadata{
			Component: Component{
				Type: "application", BOMRef: "child/product", Name: "Example", Version: "4.1.0",
				Hashes: []Hash{{Alg: "SHA-256", Content: sha256}},
			},
		},
	}
	data, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SidecarPath(path), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return doc.SerialNumber
}

// The link is emitted only when the child document provably describes the bytes the bundle
// carries. A document for the same product at the same version can still be a different build,
// and a link that resolves to the wrong build is worse than no link, because it looks
// authoritative.
func TestALinkIsMadeOnlyWhenTheDigestsAgree(t *testing.T) {
	dir := t.TempDir()
	b := syntheticBundle(writeArtifact(t, dir, "setup.exe", "bundle"))
	writeArtifact(t, dir, "Example.msi", "installer")
	carried := b.Packages[0].Payloads[0].SHA256

	mainRef := "msis/bbbbbbbb-2222-4222-8222-222222222222/package/Main"

	t.Run("no document at all", func(t *testing.T) {
		doc := bundleDoc(t, b)
		link, why := BOMLink(*componentByRef(doc, mainRef))
		if link != "" {
			t.Errorf("a link was made with no document to link to: %q", link)
		}
		if !strings.Contains(why, "no document at Example.msi.cdx.json") {
			t.Errorf("reason = %q, want it to name the document it looked for", why)
		}
	})

	t.Run("a document describing a different build", func(t *testing.T) {
		childDocument(t, filepath.Join(dir, "Example.msi"), strings.Repeat("7", 64))
		doc := bundleDoc(t, b)
		link, why := BOMLink(*componentByRef(doc, mainRef))
		if link != "" {
			t.Fatalf("a link was made to a document describing other bytes: %q", link)
		}
		for _, want := range []string{"different build", "7777777777777777", carried[:16]} {
			if !strings.Contains(why, want) {
				t.Errorf("reason = %q, want it to mention %q", why, want)
			}
		}
	})

	t.Run("a document describing exactly these bytes", func(t *testing.T) {
		serial := childDocument(t, filepath.Join(dir, "Example.msi"), carried)
		doc := bundleDoc(t, b)
		c := componentByRef(doc, mainRef)
		link, why := BOMLink(*c)
		if link == "" {
			t.Fatalf("no link was made to a matching document: %s", why)
		}
		if want := "urn:cdx:" + strings.TrimPrefix(serial, "urn:uuid:") + "/1"; link != want {
			t.Errorf("link = %q, want %q", link, want)
		}
		// externalReferences[].hashes describes the REFERENCE - the document being linked
		// to - not the thing that made it worth linking. Putting the installer's digest
		// here would hand a consumer checking the linked JSON a value that cannot match it.
		var ref *ExternalReference
		for i := range c.ExternalReferences {
			if c.ExternalReferences[i].Type == "bom" {
				ref = &c.ExternalReferences[i]
			}
		}
		if ref == nil || len(ref.Hashes) != 1 {
			t.Fatalf("the link carries no digest of the document it points at: %+v", ref)
		}
		sidecar, err := os.ReadFile(SidecarPath(filepath.Join(dir, "Example.msi")))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(sidecar)
		if want := hex.EncodeToString(sum[:]); ref.Hashes[0].Content != want {
			t.Errorf("reference hash = %s, want the SHA-256 of the linked document %s",
				ref.Hashes[0].Content, want)
		}
		if ref.Hashes[0].Content == carried {
			t.Error("the reference hash is the installer's digest; that describes the " +
				"component, not the document the reference points at")
		}
		// ...and the installer's own digest is still on the component, where it belongs.
		if !hasHash(c.Hashes, carried) {
			t.Errorf("the installer's digest was lost from the component: %+v", c.Hashes)
		}
	})
}

// Everything in a child document is untrusted input: it may be corrupt, or written by something
// else. Each of these would otherwise produce a link that cannot be followed, or - for the
// serial - put an unvalidated string into an identifier.
func TestAnUnusableChildDocumentProducesNoLink(t *testing.T) {
	mainRef := "msis/bbbbbbbb-2222-4222-8222-222222222222/package/Main"

	cases := []struct {
		name    string
		content string
		mustSay string
	}{
		{
			name:    "not JSON at all",
			content: "{this is not json",
			mustSay: "not readable as a CycloneDX document",
		},
		{
			name:    `a serial that is not a urn:uuid`,
			content: `{"serialNumber":"nonsense","version":1}`,
			mustSay: "not a well-formed urn:uuid",
		},
		{
			name: "no version, so the link cannot address one",
			content: `{"serialNumber":"urn:uuid:11111111-1111-4111-8111-111111111111",` +
				`"version":0}`,
			mustSay: "a BOM-Link addresses a specific version",
		},
		{
			name: "no subject digest to match on",
			content: `{"serialNumber":"urn:uuid:11111111-1111-4111-8111-111111111111",` +
				`"version":1,"metadata":{"component":{"name":"Example"}}}`,
			mustSay: "records no SHA-256 for its subject",
		},
		{
			// A digest too short to be one. It has to be length-checked before it is
			// compared, let alone abbreviated for a diagnostic: a three-character value
			// reached a message that sliced the first sixteen and took the process with it.
			name: "a subject digest that is not a digest",
			content: `{"serialNumber":"urn:uuid:11111111-1111-4111-8111-111111111111",` +
				`"version":1,"metadata":{"component":{"name":"Example","hashes":` +
				`[{"alg":"SHA-256","content":"abc"}]}}}`,
			mustSay: "which is not one",
		},
		{
			// The right length, but not hex - the other half of "looks like a digest".
			name: "a subject digest that is not hexadecimal",
			content: `{"serialNumber":"urn:uuid:11111111-1111-4111-8111-111111111111",` +
				`"version":1,"metadata":{"component":{"name":"Example","hashes":` +
				`[{"alg":"SHA-256","content":"` + strings.Repeat("z", 64) + `"}]}}}`,
			mustSay: "which is not one",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			b := syntheticBundle(writeArtifact(t, dir, "setup.exe", "bundle"))
			writeArtifact(t, dir, "Example.msi", "installer")
			if err := os.WriteFile(SidecarPath(filepath.Join(dir, "Example.msi")),
				[]byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}

			doc := bundleDoc(t, b)
			link, why := BOMLink(*componentByRef(doc, mainRef))
			if link != "" {
				t.Fatalf("a link was made from an unusable document: %q", link)
			}
			if !strings.Contains(why, tc.mustSay) {
				t.Errorf("reason = %q, want it to mention %q", why, tc.mustSay)
			}
		})
	}
}

// A payload name comes out of the artifact, so it is untrusted: a crafted manifest can spell
// one "..\..\something", and joining that to the bundle's directory would read - and name in
// the document - a file outside it.
func TestAPayloadNameCannotEscapeTheBundlesDirectory(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), "victim.msi")
	if err := os.WriteFile(outside, []byte("installer"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)

	// A document beside the victim that WOULD match, so the only thing preventing a link is
	// the traversal check.
	b := syntheticBundle(writeArtifact(t, dir, "setup.exe", "bundle"))
	carried := b.Packages[0].Payloads[0].SHA256
	childDocument(t, outside, carried)
	defer os.Remove(SidecarPath(outside))

	for _, name := range []string{
		`..\victim.msi`,
		"../victim.msi",
		`..\..\victim.msi`,
		`C:\victim.msi`,
	} {
		t.Run(name, func(t *testing.T) {
			b := syntheticBundle(filepath.Join(dir, "setup.exe"))
			b.Packages[0].Payloads[0].Name = name
			doc := bundleDoc(t, b)

			link, why := BOMLink(*componentByRef(doc,
				"msis/bbbbbbbb-2222-4222-8222-222222222222/package/Main"))
			if link != "" {
				t.Fatalf("a payload name escaped the bundle's directory and was linked: %q", link)
			}
			if !strings.Contains(why, "outside the bundle's directory") &&
				!strings.Contains(why, "not relative to the bundle") {
				t.Errorf("reason = %q, want it to name the traversal", why)
			}
		})
	}

	// The check must not reject an ordinary name, or it would disable linking entirely.
	if _, err := childArtifactPath(dir, "Example.msi"); err != nil {
		t.Errorf("an ordinary payload name was rejected: %v", err)
	}
	// ...nor a payload in a subdirectory, which Burn permits.
	if got, err := childArtifactPath(dir, `sub\Example.msi`); err != nil {
		t.Errorf("a payload in a subdirectory was rejected: %v", err)
	} else if got != filepath.Join(dir, "sub", "Example.msi") {
		t.Errorf("childArtifactPath = %q, want it under the bundle's directory", got)
	}
}

// --- retention ------------------------------------------------------------------------------

// The acceptance criterion for reissue: after the child and the bundle are each written twice,
// the OLD bundle document's link to the OLD child document must still resolve, and the new
// one's to the new. A customer holding the first parent cannot be reached by reissuing it, so
// losing the document its link addresses would break a bill of materials already in the field.
func TestLinksFromBothIssuesStillResolve(t *testing.T) {
	dir := t.TempDir()
	child := writeArtifact(t, dir, "Example.msi", "installer v1")
	bundlePath := writeArtifact(t, dir, "setup.exe", "bundle v1")

	b := syntheticBundle(bundlePath)
	carried := b.Packages[0].Payloads[0].SHA256
	mainRef := "msis/bbbbbbbb-2222-4222-8222-222222222222/package/Main"

	// Issue 1: the child document S1, then the bundle document P1 linking to it.
	s1 := childDocument(t, child, carried)
	p1 := bundleDoc(t, b)
	if _, _, err := Write(bundlePath, p1); err != nil {
		t.Fatal(err)
	}
	link1, why := BOMLink(*componentByRef(p1, mainRef))
	if link1 == "" {
		t.Fatalf("P1 did not link to S1: %s", why)
	}

	// Issue 2: the child is reissued as S2 - Write keeps S1 under its own serial - and the
	// bundle as P2, which links to S2.
	doc2 := &Document{
		BOMFormat: "CycloneDX", SpecVersion: "1.6", Version: 1,
		SerialNumber: "urn:uuid:22222222-2222-4222-8222-222222222222",
		Metadata: Metadata{Component: Component{
			Type: "application", BOMRef: "child/product", Name: "Example", Version: "4.1.0",
			Hashes: []Hash{{Alg: "SHA-256", Content: carried}},
		}},
	}
	if _, preserved, err := Write(child, doc2); err != nil {
		t.Fatal(err)
	} else if preserved == "" {
		t.Fatal("reissuing the child did not preserve the document P1 links to")
	}

	p2 := bundleDoc(t, b)
	if _, preserved, err := Write(bundlePath, p2); err != nil {
		t.Fatal(err)
	} else if preserved == "" {
		t.Fatal("reissuing the bundle did not preserve P1")
	}
	link2, why := BOMLink(*componentByRef(p2, mainRef))
	if link2 == "" {
		t.Fatalf("P2 did not link to S2: %s", why)
	}

	if link1 == link2 {
		t.Fatal("both issues produced the same link; the reissue was not a new document")
	}

	// Both links must resolve to a document on disk that really carries that serial.
	for _, tc := range []struct{ name, link, wantSerial string }{
		{"P1 -> S1", link1, s1},
		{"P2 -> S2", link2, doc2.SerialNumber},
	} {
		serial, version, err := ParseBOMLink(tc.link)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if serial != tc.wantSerial || version != 1 {
			t.Errorf("%s: link addresses %s/%d, want %s/1", tc.name, serial, version, tc.wantSerial)
			continue
		}
		path, err := FindBySerial(dir, serial)
		if err != nil {
			t.Errorf("%s: the link no longer resolves: %v", tc.name, err)
			continue
		}
		// ...and the document found is really the one, not merely a file that exists.
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			SerialNumber string `json:"serialNumber"`
		}
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if got.SerialNumber != serial {
			t.Errorf("%s resolved to %s, which carries serial %s", tc.name, path, got.SerialNumber)
		}
	}
}

// A BOM-Link has to round-trip, or "follow the link" is not an operation a consumer can
// perform. The fragment form addressing one component inside the target is accepted too.
func TestBOMLinkRoundTrips(t *testing.T) {
	const serial = "urn:uuid:33333333-3333-4333-8333-333333333333"
	for _, link := range []string{
		"urn:cdx:33333333-3333-4333-8333-333333333333/7",
		"urn:cdx:33333333-3333-4333-8333-333333333333/7#some/ref",
	} {
		gotSerial, gotVersion, err := ParseBOMLink(link)
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		if gotSerial != serial || gotVersion != 7 {
			t.Errorf("%s -> %s/%d, want %s/7", link, gotSerial, gotVersion, serial)
		}
	}
	for _, bad := range []string{"", "https://example.invalid/bom.json", "urn:cdx:noversion",
		"urn:cdx:x/notanumber"} {
		if _, _, err := ParseBOMLink(bad); err == nil {
			t.Errorf("%q is not a BOM-Link but parsed", bad)
		}
	}
}

// FindBySerial must not be talked into reading outside the directory, and must not report a
// document it did not find.
func TestFindBySerialIsStrict(t *testing.T) {
	dir := t.TempDir()
	if _, err := FindBySerial(dir, `urn:uuid:x\..\..\victim`); err == nil {
		t.Error("a malformed serial must be refused before it reaches the filesystem")
	}
	if _, err := FindBySerial(dir, "urn:uuid:44444444-4444-4444-8444-444444444444"); err == nil {
		t.Error("a serial no document carries must not resolve")
	}
}

// --- determinism ------------------------------------------------------------------------------

// Two documents describing one bundle must differ in exactly two fields: the timestamp, which
// NTIA requires, and the serial, which identifies the document rather than its subject.
func TestTwoBundleDocumentsDifferOnlyInTimestampAndSerial(t *testing.T) {
	dir := t.TempDir()
	b := syntheticBundle(writeArtifact(t, dir, "setup.exe", "bundle"))

	first, err := Marshal(bundleDoc(t, b))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(bundleDoc(t, b))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) == string(second) {
		t.Fatal("two documents were byte-identical, so the serial is not per-document " +
			"and this test proves nothing about the rest")
	}

	a, err := CanonicalForDiff(first)
	if err != nil {
		t.Fatal(err)
	}
	c, err := CanonicalForDiff(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(c) {
		t.Errorf("two documents for one bundle differ beyond the timestamp and serial:\n%s\n%s", a, c)
	}
}

// #72: a multi-arch bundle chains packages that share one product name; each is named by the
// file the bundle carries, with the product name beside it, and failing that by its package id.
func TestAChainedPackageIsNamedByItsFile(t *testing.T) {
	for _, tc := range []struct {
		c    Component
		want string
	}{
		{Component{Name: "MSIS", Properties: []Property{{propBSIFilename, "msis-3.0.6-x64.msi"}, {propPackageID, "MainPackage_x64"}}},
			"msis-3.0.6-x64.msi (MSIS)"},
		{Component{Name: "MSIS", Properties: []Property{{propPackageID, "MainPackage_arm64"}}}, "MainPackage_arm64 (MSIS)"},
		{Component{Name: "app.msi", Properties: []Property{{propBSIFilename, "app.msi"}}}, "app.msi"},
		{Component{Name: "MSIS"}, "MSIS"},
	} {
		if got := PackageLabel(tc.c); got != tc.want {
			t.Errorf("PackageLabel = %q, want %q", got, tc.want)
		}
	}
}
