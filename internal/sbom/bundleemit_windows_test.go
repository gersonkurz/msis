//go:build windows

package sbom

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/burnread"
	"github.com/gersonkurz/msis/internal/msiread"
	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

func fixtureBundle() string {
	return filepath.Join("..", "burnread", "testdata", "fixture.exe")
}

// The end-to-end check for #33: a real bundle, read by the real reader, emitted, and put
// through the same conformance package the MSI emitter answers to. Expected is stated
// independently of the document, so the document is judged against what the artifact holds.
func TestRealBundleProducesAConformingDocument(t *testing.T) {
	b, err := burnread.Read(fixtureBundle())
	if err != nil {
		t.Fatalf("reading the fixture bundle: %v", err)
	}

	doc, err := FromBundle(b, Options{MsisVersion: "test"})
	if err != nil {
		t.Fatalf("FromBundle: %v", err)
	}
	data, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	want := conformance.Expected{
		// Only the bundle's own identity was read rather than inferred, so only it may
		// carry a purl. A chained installer's DisplayName is the bundle author's label for
		// it, not a package identity a CVE feed can be matched against.
		IdentifiedComponents: []string{doc.Metadata.Component.BOMRef},
		// Derived from the bundle, not from the document - see UnhashablePayloads.
		UnhashableComponents: UnhashablePayloads(b),
	}
	if problems := conformance.Check(schemaDirForTests(), data, want); len(problems) > 0 {
		for _, p := range problems {
			t.Errorf("conformance: %v", p)
		}
	}

	// The exemption must be exactly the payloads the fixture does not carry, and no more. An
	// empty or over-broad list would let the conformance check pass over a real gap; a list
	// missing one would have failed the check above.
	got := map[string]bool{}
	for _, ref := range want.UnhashableComponents {
		got[ref] = true
	}
	ns := bundleNamespace(b)
	expect := map[string]bool{
		ns + "/package/External":   true, // the uncompressed ExePackage
		ns + "/payload/beside.txt": true, // the bundle-level payload the engine expects beside it
	}
	for ref := range expect {
		if !got[ref] {
			t.Errorf("UnhashableComponents omits %q", ref)
		}
	}
	for ref := range got {
		if !expect[ref] {
			t.Errorf("UnhashableComponents includes %q, which the bundle does carry", ref)
		}
	}

	// Cross-check against the artifact a second way: every payload the READER says is not
	// carried must be exempted, and nothing else. The two are derived differently, so a
	// collection the exemption walk forgets shows up here.
	uncarried := 0
	for _, p := range b.AllPayloads() {
		if !p.Carried {
			uncarried++
		}
	}
	if uncarried != len(want.UnhashableComponents) {
		t.Errorf("the reader reports %d uncarried payloads but %d components are exempted",
			uncarried, len(want.UnhashableComponents))
	}
}

// The three things a bundle document has that an MSI document does not: the bootstrapper's
// payloads, the chain, and a payload that is not in the file at all. Each is asserted against
// the fixture rather than against the emitter's own output.
func TestRealBundleDocumentCoversTheChainAndTheBootstrapper(t *testing.T) {
	b, err := burnread.Read(fixtureBundle())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := FromBundle(b, Options{MsisVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}

	roles := map[string][]string{}
	for _, c := range doc.Components {
		roles[propertyValue(c.Properties, propRole)] = append(
			roles[propertyValue(c.Properties, propRole)], c.Name)
	}

	// WiX contributes two payloads the .wxs never mentions; they are in the shipped file,
	// so they are in the inventory.
	for _, name := range []string{
		"BootstrapperApplicationData.xml", "BootstrapperExtensionData.xml",
		"extra.txt", "fakeba.exe",
	} {
		if !contains(roles[string(burnread.RoleBootstrapper)], name) {
			t.Errorf("bootstrapper payload %q is missing from the document", name)
		}
	}
	if !contains(roles[string(burnread.RoleSupplementary)], "sidecar.txt") {
		t.Errorf("the supplementary payload is missing: %v",
			roles[string(burnread.RoleSupplementary)])
	}
	// A payload no chain package references ships inside the bundle all the same, so it is
	// inventoried all the same.
	if !contains(roles[string(burnread.RoleBundle)], "layout.txt") {
		t.Errorf("the bundle-level payload is missing from the document: %v",
			roles[string(burnread.RoleBundle)])
	}
	if got := len(roles[string(burnread.RoleChained)]); got != 2 {
		t.Errorf("%d chained installers in the document, want 2 (one embedded, one external)", got)
	}

	// Every component is reachable from the root, or the document says the bundle contains
	// things it does not.
	refs := map[string]bool{}
	for _, c := range doc.Components {
		refs[c.BOMRef] = true
	}
	var dependsOn []string
	for _, d := range doc.Dependencies {
		if d.Ref == doc.Metadata.Component.BOMRef {
			dependsOn = d.DependsOn
		}
	}
	if len(dependsOn) != len(refs) {
		t.Errorf("the root depends on %d components but the document has %d",
			len(dependsOn), len(refs))
	}
	for _, r := range dependsOn {
		if !refs[r] {
			t.Errorf("the root depends on %q, which is not a component", r)
		}
	}
}

// The link path, end to end against real artifacts: the chained installer is the repository's
// own MSI fixture, so a document written for THAT file is the one the bundle's document must
// link to - and only because the digests agree, not because the names do.
func TestRealBundleLinksToTheChainedInstallersDocument(t *testing.T) {
	dir := t.TempDir()

	// The bundle and the installer it carries, side by side as they ship.
	bundlePath := filepath.Join(dir, "fixture.exe")
	copyFile(t, fixtureBundle(), bundlePath)
	msiPath := filepath.Join(dir, "fixture.msi")
	copyFile(t, fixtureMSI(), msiPath)

	b, err := burnread.Read(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	var mainRef string
	for _, pkg := range b.Packages {
		if pkg.ID == "Embedded" {
			mainRef = bundleNamespace(b) + "/package/" + encodeRefPart(pkg.ID)
		}
	}
	if mainRef == "" {
		t.Fatal("the fixture has no Embedded package")
	}

	// Before the child document exists there is no link, and the document says so.
	doc, err := FromBundle(b, Options{MsisVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if link, why := BOMLink(*componentByRef(doc, mainRef)); link != "" {
		t.Fatalf("linked with no child document present: %q", link)
	} else if !strings.Contains(why, "no document at fixture.msi.cdx.json") {
		t.Errorf("reason = %q, want it to name the document it looked for", why)
	}

	// Write the child document the way /SBOM does, from the real MSI.
	if err := writeMSIDocument(t, msiPath); err != nil {
		t.Fatal(err)
	}

	doc, err = FromBundle(b, Options{MsisVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	link, why := BOMLink(*componentByRef(doc, mainRef))
	if link == "" {
		t.Fatalf("no link to the chained installer's own document: %s", why)
	}

	// The link must resolve to the document actually on disk.
	serial, version, err := ParseBOMLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Errorf("link version = %d, want 1", version)
	}
	found, err := FindBySerial(dir, serial)
	if err != nil {
		t.Fatalf("the link does not resolve: %v", err)
	}
	if found != SidecarPath(msiPath) {
		t.Errorf("the link resolved to %s, want %s", found, SidecarPath(msiPath))
	}

	// A document for a DIFFERENT build must break the link rather than keep it. The
	// filename, the product and the version are all still right; only the bytes differ,
	// which is precisely the case a name-based link would get wrong.
	data, err := os.ReadFile(SidecarPath(msiPath))
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatal(err)
	}
	subject := generic["metadata"].(map[string]any)["component"].(map[string]any)
	hashes := subject["hashes"].([]any)
	original := hashes[0].(map[string]any)["content"].(string)
	hashes[0].(map[string]any)["content"] = strings.Repeat("0", 64)
	stale, err := json.Marshal(generic)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SidecarPath(msiPath), stale, 0o644); err != nil {
		t.Fatal(err)
	}

	doc, err = FromBundle(b, Options{MsisVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	link, why = BOMLink(*componentByRef(doc, mainRef))
	if link != "" {
		t.Fatalf("linked to a document describing other bytes: %q", link)
	}
	for _, want := range []string{"different build", original[:16]} {
		if !strings.Contains(why, want) {
			t.Errorf("reason = %q, want it to mention %q", why, want)
		}
	}
}

// writeMSIDocument writes the document for a built MSI exactly as /SBOM would, so the link
// test exercises the real writer rather than a hand-built stand-in.
func writeMSIDocument(t *testing.T, path string) error {
	t.Helper()
	pkg, err := msiread.Read(path)
	if err != nil {
		return err
	}
	doc, err := FromPackage(pkg, Options{MsisVersion: "test"})
	if err != nil {
		return err
	}
	_, _, err = Write(path, doc)
	return err
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
