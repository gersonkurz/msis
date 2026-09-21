//go:build windows

package burnread

import (
	"bytes"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const fixture = "testdata/fixture.exe"

func readFixture(t *testing.T) *Bundle {
	t.Helper()
	b, err := Read(fixture)
	if err != nil {
		t.Fatalf("reading the fixture bundle: %v", err)
	}
	return b
}

// The identity goes straight into the document's metadata, so it is read from the manifest and
// never inferred. Two records of the bundle code exist - the PE section and the manifest - and
// Read requires them to agree; this pins the value they agree on.
func TestFixtureIdentity(t *testing.T) {
	b := readFixture(t)

	for _, tc := range []struct{ field, got, want string }{
		{"Name", b.Name, "Burnread Fixture"},
		{"Version", b.Version, "1.2.3"},
		{"Publisher", b.Publisher, "NG Branch Technology GmbH"},
		{"UpgradeCode", b.UpgradeCode, "{6E3B2A14-9C5D-4F7E-8A1B-2D4C6E8F0A12}"},
		{"Scope", b.Scope, "perMachine"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.field, tc.got, tc.want)
		}
	}
	// The bundle code is generated per build, so only its shape is asserted.
	if len(b.Code) != 38 || !strings.HasPrefix(b.Code, "{") {
		t.Errorf("Code = %q, want a braced GUID", b.Code)
	}
	if b.EngineVersion == "" {
		t.Error("EngineVersion is empty; the document records which engine built the bundle")
	}
}

// The bootstrapper application's payloads are part of the inventory: they are executed during
// the install, which is exactly the class of code a consumer needs to know about even though
// nothing installs them.
func TestFixtureInventoriesTheBootstrapperPayloads(t *testing.T) {
	b := readFixture(t)

	var names []string
	for _, p := range b.UX {
		names = append(names, p.Name)
		if !p.Carried || p.SHA256 == "" {
			t.Errorf("UX payload %q: carried=%v sha256=%q, want carried with a digest",
				p.Name, p.Carried, p.SHA256)
		}
		if p.Role != RoleBootstrapper {
			t.Errorf("UX payload %q has role %q, want %q", p.Name, p.Role, RoleBootstrapper)
		}
	}

	// WiX contributes BootstrapperApplicationData.xml and BootstrapperExtensionData.xml on top
	// of what the .wxs names. That is the point of reading the artifact: the script does not
	// mention them, and they are in the shipped file.
	want := []string{
		"BootstrapperApplicationData.xml",
		"BootstrapperExtensionData.xml",
		"extra.txt",
		"fakeba.exe",
	}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("UX payloads = %v, want %v", names, want)
	}

	// The manifest itself is not a payload. Entry "0" of the UX container is the manifest,
	// and emitting it as a bootstrapper file would inventory a thing that does not exist.
	for _, n := range names {
		if n == manifestEntry || n == "" {
			t.Errorf("the manifest leaked into the payload inventory as %q", n)
		}
	}
}

// The three payload roles and the two carriage states are different code paths, and the
// difference between them is what a consumer acts on: a carried payload can be verified from
// the file in hand, one the engine fetches later cannot.
func TestFixtureChainDistinguishesRolesAndCarriage(t *testing.T) {
	b := readFixture(t)

	if len(b.Packages) != 2 {
		t.Fatalf("%d chain packages, want 2", len(b.Packages))
	}

	embedded, external := b.Packages[0], b.Packages[1]

	if embedded.ID != "Embedded" || embedded.Kind != "MsiPackage" {
		t.Errorf("first package = %s/%s, want Embedded/MsiPackage", embedded.ID, embedded.Kind)
	}
	if embedded.ProductCode == "" {
		t.Error("an MsiPackage records a ProductCode; none was read")
	}

	// The installer comes first and the supplementary payload after it, both carried.
	if got := len(embedded.Payloads); got != 2 {
		t.Fatalf("the embedded package has %d payloads, want 2", got)
	}
	inst := embedded.Installer()
	switch {
	case inst == nil:
		t.Fatal("the embedded package has no installer payload")
	case inst.Name != "fixture.msi":
		t.Errorf("installer = %q, want fixture.msi", inst.Name)
	case !inst.Carried || inst.SHA256 == "":
		t.Errorf("the embedded installer must be carried and hashed; got carried=%v sha=%q",
			inst.Carried, inst.SHA256)
	}
	if supp := embedded.Payloads[1]; supp.Role != RoleSupplementary || supp.Name != "sidecar.txt" {
		t.Errorf("supplementary payload = %q/%q, want sidecar.txt/%s",
			supp.Name, supp.Role, RoleSupplementary)
	}

	// The uncompressed package's bytes are not in the bundle. It must still be inventoried,
	// with no digest and a stated reason - dropping it would hide a chained installer.
	if external.ID != "External" || external.Kind != "ExePackage" {
		t.Errorf("second package = %s/%s, want External/ExePackage", external.ID, external.Kind)
	}
	ei := external.Installer()
	switch {
	case ei == nil:
		t.Fatal("the external package has no installer payload")
	case ei.Carried:
		t.Error("an uncompressed package's payload is not carried in the bundle")
	case ei.SHA256 != "":
		t.Errorf("msis cannot hash bytes it does not have, but reported %q", ei.SHA256)
	case ei.Unavailable == "":
		t.Error("a payload with no digest must say why; this one says nothing")
	case !strings.Contains(ei.Unavailable, "beside the installer"):
		t.Errorf("Unavailable = %q, want it to say where the engine looks", ei.Unavailable)
	}
	// It is still described by the digest the BUNDLE records, which is what the engine
	// enforces and the only thing a consumer can check the eventual file against.
	if len(ei.RecordedSHA512) != 128 {
		t.Errorf("RecordedSHA512 = %q, want the 128-hex-digit digest the manifest records",
			ei.RecordedSHA512)
	}
}

// The digests are the whole product of this package, so they are checked against the files the
// fixture was built from rather than against another call to the same code.
func TestFixtureDigestsMatchTheSourceFiles(t *testing.T) {
	b := readFixture(t)

	digestOf := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}

	want := map[string]string{
		"layout.txt":  digestOf(filepath.Join("testdata", "layout.txt")),
		"extra.txt":   digestOf(filepath.Join("testdata", "extra.txt")),
		"fakeba.exe":  digestOf(filepath.Join("testdata", "fakeba.exe")),
		"sidecar.txt": digestOf(filepath.Join("testdata", "sidecar.txt")),
		"fixture.msi": digestOf(filepath.Join("..", "msiread", "testdata", "fixture.msi")),
	}

	got := map[string]string{}
	for _, p := range append(append([]Payload{}, b.UX...), b.Loose...) {
		got[p.Name] = p.SHA256
	}
	for _, pkg := range b.Packages {
		for _, p := range pkg.Payloads {
			if p.Carried {
				got[p.Name] = p.SHA256
			}
		}
	}

	for name, sum := range want {
		if got[name] != sum {
			t.Errorf("%s: digest %q, want %q (the file the fixture was built from)",
				name, got[name], sum)
		}
	}
}

// Two reads of one bundle have to produce identical output, or the SBOM built on top cannot be
// diffed between releases. Cabinets extract into a map, so any order taken from one is random.
func TestReadingTwiceGivesTheSameResult(t *testing.T) {
	first, second := readFixture(t), readFixture(t)
	if !reflect.DeepEqual(first, second) {
		t.Error("two reads of one bundle differ; the output is not diffable")
	}
	// DeepEqual on the whole struct would also pass if both reads were empty, so the shape
	// that has to be ordered is asserted explicitly.
	if len(first.UX) < 2 || len(first.Packages) < 2 {
		t.Fatalf("the fixture should have several of each; got %d UX, %d packages",
			len(first.UX), len(first.Packages))
	}
	for i := 1; i < len(first.UX); i++ {
		if first.UX[i-1].Name > first.UX[i].Name {
			t.Errorf("UX payloads are not sorted: %q before %q",
				first.UX[i-1].Name, first.UX[i].Name)
		}
	}
	// The chain keeps the manifest's order, which is the install order and is meaningful.
	if first.Packages[0].ID != "Embedded" || first.Packages[1].ID != "External" {
		t.Errorf("chain order = %s,%s; want the manifest's order Embedded,External",
			first.Packages[0].ID, first.Packages[1].ID)
	}
}

// A modified bundle must not be inventoried as if it were intact. The recorded digests are the
// only thing standing between "these are the bytes that shipped" and a document that is
// confidently wrong.
func TestATamperedContainerIsRefused(t *testing.T) {
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}

	// Flip a byte deep inside the attached container - past the stub and the UX container,
	// so it is payload rather than framing.
	section, _, err := wixburnSection(raw)
	if err != nil {
		t.Fatal(err)
	}
	h, err := parseSection(section, len(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(h.ContainerSizes) < 2 {
		t.Skip("the fixture has no attached container to tamper with")
	}
	at := int(h.StubSize) + int(h.ContainerSizes[0]) + int(h.ContainerSizes[1])/2

	tampered := make([]byte, len(raw))
	copy(tampered, raw)
	tampered[at] ^= 0xFF

	_, err = read(fixture, tampered)
	if err == nil {
		t.Fatal("a bundle whose container no longer matches its recorded digest was read " +
			"as if intact")
	}
	if !strings.Contains(err.Error(), "corrupt or has been modified") {
		t.Errorf("error = %v, want it to say the file was modified", err)
	}

	// The unmodified copy of the same bytes must still read, so the test is about tampering
	// and not about the copy.
	if _, err := read(fixture, raw); err != nil {
		t.Errorf("the untampered bundle must still read: %v", err)
	}
}

// Pointing the reader at something that is not a bundle has to say so, rather than fail deep
// inside a cabinet extractor with an error nobody can act on.
func TestAPlainExecutableIsNotABundle(t *testing.T) {
	// The test binary itself: a real PE with no .wixburn section.
	self, err := os.Executable()
	if err != nil {
		t.Skip("cannot locate the test binary")
	}
	_, err = Read(self)
	if err == nil {
		t.Fatal("a plain executable must not read as a bundle")
	}
	if !strings.Contains(err.Error(), "not a Burn bundle") {
		t.Errorf("error = %v, want it to say this is not a bundle", err)
	}
}

// The bundle code is recorded twice, in the PE section and in the manifest, and the two are
// written by different parts of the toolchain. Read requires them to agree: a file whose two
// self-descriptions disagree is not the thing either of them claims, and inventorying it under
// one of the two identities would put a wrong bundle code in the document.
func TestASectionAndManifestThatDisagreeAreRefused(t *testing.T) {
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}

	// Locate the .wixburn section in the file so the GUID can be changed where the reader
	// will look for it.
	pf, err := pe.NewFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer pf.Close()
	sec := pf.Section(".wixburn")
	if sec == nil {
		t.Fatal("the fixture has no .wixburn section")
	}

	tampered := make([]byte, len(raw))
	copy(tampered, raw)
	at := int(sec.Offset) + offBundleCode
	tampered[at] ^= 0xFF

	_, err = read(fixture, tampered)
	if err == nil {
		t.Fatal("a bundle whose section and manifest record different codes was read anyway")
	}
	if !strings.Contains(err.Error(), "inconsistent") {
		t.Errorf("error = %v, want it to say the file's two records disagree", err)
	}

	// The untampered bytes must still read, so this is about the disagreement and not the
	// copy or the offset.
	b, err := read(fixture, raw)
	if err != nil {
		t.Fatalf("the untampered bundle must still read: %v", err)
	}
	if b.Code == "" {
		t.Error("the bundle code was not read at all, so the cross-check compares nothing")
	}
}

// A payload no chain package references still ships inside the bundle, so it still belongs in
// the inventory. Burn writes every non-UX payload at bundle level; reading only the chain's
// PayloadRefs dropped this one silently - a file that is distributed and appears nowhere.
func TestFixtureInventoriesAPayloadNoPackageReferences(t *testing.T) {
	b := readFixture(t)

	byName := map[string]Payload{}
	for _, p := range b.Loose {
		byName[p.Name] = p
	}
	if len(b.Loose) != 2 {
		t.Fatalf("%d bundle-level payloads, want 2 (one carried, one not): %+v", len(b.Loose), b.Loose)
	}

	// Carried: inside the attached container, so it has a digest like anything else.
	carried, ok := byName["layout.txt"]
	switch {
	case !ok:
		t.Fatalf("layout.txt is missing: %v", byName)
	case carried.Role != RoleBundle:
		t.Errorf("role = %q, want %q", carried.Role, RoleBundle)
	case !carried.LayoutOnly:
		t.Error("WiX marked it LayoutOnly; the document has to be able to say it is never installed")
	case !carried.Carried || carried.SHA256 == "":
		t.Errorf("it is in the attached container, so it must be carried and hashed: %+v", carried)
	}

	// Not carried: the engine expects it beside the bundle, so there is nothing to hash -
	// and that has to be said, not left as an empty column.
	absent, ok := byName["beside.txt"]
	switch {
	case !ok:
		t.Fatalf("beside.txt is missing: %v", byName)
	case absent.Carried || absent.SHA256 != "":
		t.Errorf("msis reported a digest for bytes it does not have: %+v", absent)
	case absent.Unavailable == "":
		t.Error("a payload with no digest must say why")
	case len(absent.RecordedSHA512) != 128:
		t.Errorf("the digest the bundle records is the only one there is, and it was dropped: %+v", absent)
	}

	// Neither may also be counted as a bootstrapper payload or a package payload.
	for _, name := range []string{"layout.txt", "beside.txt"} {
		for _, u := range b.UX {
			if u.Name == name {
				t.Errorf("%s was also inventoried as a bootstrapper payload", name)
			}
		}
		for _, pkg := range b.Packages {
			for _, pay := range pkg.Payloads {
				if pay.Name == name {
					t.Errorf("%s was also attributed to package %s", name, pkg.ID)
				}
			}
		}
	}
}

// AllPayloads is what every consumer outside this package walks, so it has to yield all three
// collections. A consumer that walked only the ones it knew about is how a payload went
// missing from the CLI twice.
func TestAllPayloadsYieldsEveryCollection(t *testing.T) {
	b := readFixture(t)

	want := len(b.UX) + len(b.Loose)
	for _, pkg := range b.Packages {
		want += len(pkg.Payloads)
	}
	all := b.AllPayloads()
	if len(all) != want {
		t.Fatalf("AllPayloads gave %d payloads, want %d", len(all), want)
	}

	seen := map[string]bool{}
	for _, p := range all {
		seen[p.Name] = true
	}
	// One from each collection, named explicitly: a count alone would pass if two
	// collections were swapped.
	for _, name := range []string{"fakeba.exe", "layout.txt", "beside.txt", "fixture.msi",
		"sidecar.txt", "external.exe"} {
		if !seen[name] {
			t.Errorf("AllPayloads omits %q", name)
		}
	}
}
