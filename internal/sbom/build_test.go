package sbom

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gersonkurz/msis/internal/msiread"
)

// bom-ref derivation is the public contract this ticket freezes: ticket D indexes on it, ticket
// F keys VEX statements to it, and both break if it moves. These tests pin the cases #29 named.

func fileOf(name, target, component, id string, seq int) msiread.File {
	return msiread.File{ID: id, Name: name, Target: target, Component: component, Sequence: seq}
}

// Two features installing DIFFERENT files to the SAME destination is supported by this
// repository and covered by its own generator test. Target alone cannot tell them apart, so the
// MSI component discriminates.
func TestTwoFilesAtOneTargetGetDistinctRefs(t *testing.T) {
	const ns = "msis/abc"
	a := fileRef(ns, fileOf("config.xml", `[INSTALLDIR]config.xml`, "C_BASE", "F1", 1), "{GUID-A}")
	b := fileRef(ns, fileOf("config.xml", `[INSTALLDIR]config.xml`, "C_OVERRIDE", "F2", 2), "{GUID-B}")

	if a == b {
		t.Fatalf("both files got the same ref %q; the document could not tell them apart", a)
	}
	for _, ref := range []string{a, b} {
		if !strings.Contains(ref, "config.xml") {
			t.Errorf("ref %q does not name the target", ref)
		}
	}
}

// One source installed to two destinations - issue #21 - must also produce distinct refs.
func TestOneSourceAtTwoTargetsGetsDistinctRefs(t *testing.T) {
	const ns = "msis/abc"
	a := fileRef(ns, fileOf("app.exe", `[INSTALLDIR]app.exe`, "C_ONE", "F1", 1), "{GUID-A}")
	b := fileRef(ns, fileOf("app.exe", `[INSTALLDIR]bin\app.exe`, "C_TWO", "F2", 2), "{GUID-B}")
	if a == b {
		t.Fatalf("both destinations got the same ref %q", a)
	}
}

// A ref must survive a version bump: the same file in 4.1 and 4.2 is the same thing, which is
// what makes an index and a VEX statement usable across releases.
func TestRefsAreStableAcrossVersionsAndReordering(t *testing.T) {
	const ns = "msis/abc" // from the UpgradeCode, which does not change between releases

	v41 := fileRef(ns, fileOf("app.exe", `[INSTALLDIR]app.exe`, "C_APP", "FILE_ID00003", 3), "{GUID-A}")

	// A later release: the generated File key moved because authoring was reordered, and the
	// sequence changed. Neither identifies the thing.
	v42 := fileRef(ns, fileOf("app.exe", `[INSTALLDIR]app.exe`, "C_APP", "FILE_ID00011", 11), "{GUID-A}")
	if v41 != v42 {
		t.Errorf("the ref moved between releases:\n  %s\n  %s", v41, v42)
	}

	// Casing is not identity on Windows.
	cased := fileRef(ns, fileOf("App.exe", `[INSTALLDIR]App.exe`, "C_APP", "FILE_ID00003", 3), "{guid-a}")
	if cased != v41 {
		t.Errorf("a casing change moved the ref:\n  %s\n  %s", v41, cased)
	}
}

// When a component carries no GUID the derivation must still produce something, and must say in
// the code which fallback it used rather than silently colliding.
func TestRefFallsBackWhenThereIsNoComponentGuid(t *testing.T) {
	const ns = "msis/abc"
	withKey := fileRef(ns, fileOf("a.dll", `[INSTALLDIR]a.dll`, "C_A", "F1", 1), "")
	other := fileRef(ns, fileOf("a.dll", `[INSTALLDIR]a.dll`, "C_B", "F2", 2), "")
	if withKey == other {
		t.Error("with no GUID, the component key must still discriminate")
	}

	noComponent := fileRef(ns, fileOf("a.dll", `[INSTALLDIR]a.dll`, "", "F1", 1), "")
	// The key keeps its case: MSI identifiers are case-sensitive, so lowercasing them would
	// merge two different things.
	if noComponent == "" || !strings.Contains(noComponent, "F1") {
		t.Errorf("with neither GUID nor component, the file key is the last resort; got %q", noComponent)
	}
}

// MSI component identifiers are case-SENSITIVE, so "C_A" and "c_a" are different components.
// Normalising a fallback identifier the way a GUID is normalised merged them, and two files at
// one destination in two different components got one ref.
func TestGuidlessComponentsKeepTheirCase(t *testing.T) {
	const ns = "msis/abc"
	upper := fileRef(ns, fileOf("a.dll", `[INSTALLDIR]a.dll`, "C_A", "F1", 1), "")
	lower := fileRef(ns, fileOf("a.dll", `[INSTALLDIR]a.dll`, "c_a", "F2", 2), "")
	if upper == lower {
		t.Fatalf("two case-distinct components collapsed onto one ref: %q", upper)
	}

	// A GUID, by contrast, IS case-insensitive, so those must still merge.
	g1 := fileRef(ns, fileOf("a.dll", `[INSTALLDIR]a.dll`, "C_A", "F1", 1), "{ABCDEF01-0000-0000-0000-000000000000}")
	g2 := fileRef(ns, fileOf("a.dll", `[INSTALLDIR]a.dll`, "C_A", "F1", 1), "abcdef01-0000-0000-0000-000000000000")
	if g1 != g2 {
		t.Errorf("the same GUID in different case produced different refs:\n  %s\n  %s", g1, g2)
	}
}

// A Binary-table stream has no install target at all, so it needs its own identity scheme
// rather than an empty path.
func TestBinaryStreamsGetTheirOwnRefs(t *testing.T) {
	c := binaryComponent(msiread.Binary{Name: "Wix4UtilCA_X64", SHA256: strings.Repeat("a", 64)}, "msis/abc")
	if !strings.Contains(c.BOMRef, "/binary/") {
		t.Errorf("ref %q does not mark the stream as one", c.BOMRef)
	}
	if strings.Contains(c.BOMRef, "/file/") {
		t.Errorf("ref %q collides with the payload scheme", c.BOMRef)
	}
	if c.PURL != "" {
		t.Errorf("a binary stream must not carry a purl: %q", c.PURL)
	}
}

// The namespace is the UpgradeCode because that is what Windows Installer defines as stable
// across a product's releases; ProductCode changes every build and would renew every ref.
func TestNamespacePrefersTheUpgradeCode(t *testing.T) {
	withUpgrade := namespaceOf(&msiread.Package{Properties: map[string]string{
		"UpgradeCode": "{E7A3B8C1-5D2F-4A9E-B6C4-8F1D3E2A7B5C}",
		"ProductCode": "{57D8E494-CBEA-477C-8B2F-A6683A7F56B2}",
		"ProductName": "MSIS",
	}})
	if !strings.Contains(withUpgrade, "e7a3b8c1") {
		t.Errorf("namespace %q is not derived from the UpgradeCode", withUpgrade)
	}
	if strings.Contains(withUpgrade, "57d8e494") {
		t.Error("the namespace must not depend on the ProductCode, which changes every build")
	}

	// The fallback is weaker and must be visibly different rather than silently substituted.
	noUpgrade := namespaceOf(&msiread.Package{Properties: map[string]string{"ProductName": "MSIS"}})
	if !strings.Contains(noUpgrade, "unversioned-name") {
		t.Errorf("fallback namespace %q does not record that it is a fallback", noUpgrade)
	}

	anonymous := namespaceOf(&msiread.Package{Path: `C:\dist\Thing.msi`, Properties: map[string]string{}})
	if !strings.Contains(anonymous, "unidentified") {
		t.Errorf("last-resort namespace %q does not record what it is", anonymous)
	}
}

// Exactly two fields may differ between documents describing identical input. Anything else
// varying kills the "diff two releases" use the whole design is shaped around.
func TestOnlyTimestampAndSerialVary(t *testing.T) {
	pkg := syntheticPackage()

	first := mustBuild(t, pkg, Options{
		MsisVersion: "1.0",
		Now:         func() time.Time { return time.Unix(1000, 0) },
		NewSerial:   func() (string, error) { return "urn:uuid:11111111-1111-4111-8111-111111111111", nil },
	})
	second := mustBuild(t, pkg, Options{
		MsisVersion: "1.0",
		Now:         func() time.Time { return time.Unix(2000, 0) },
		NewSerial:   func() (string, error) { return "urn:uuid:22222222-2222-4222-8222-222222222222", nil },
	})

	if string(first) == string(second) {
		t.Fatal("the two documents are identical; the test is not varying anything")
	}

	canonFirst, err := CanonicalForDiff(first)
	if err != nil {
		t.Fatal(err)
	}
	canonSecond, err := CanonicalForDiff(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonFirst) != string(canonSecond) {
		t.Errorf("documents differ beyond the two permitted fields:\n%s\n---\n%s", canonFirst, canonSecond)
	}
}

// Determinism has to hold under varied map insertion order, not merely across repeated runs in
// one process: Go randomises map iteration, and the package's Properties are a map.
func TestDeterministicUnderVariedMapOrder(t *testing.T) {
	var reference []byte
	for attempt := 0; attempt < 12; attempt++ {
		pkg := syntheticPackage()
		// Rebuilding the map each time gives Go a fresh iteration order to choose.
		props := map[string]string{}
		for k, v := range pkg.Properties {
			props[k] = v
		}
		pkg.Properties = props

		// And the component input order is varied too. Without this the slices arrive in
		// one fixed order and the emitter's own sorting is never exercised - the test
		// passed even with sorting removed, which made it a determinism test in name only.
		if attempt%2 == 1 {
			pkg.Files[0], pkg.Files[1] = pkg.Files[1], pkg.Files[0]
		}

		got := mustBuild(t, pkg, Options{
			MsisVersion: "1.0",
			Now:         func() time.Time { return time.Unix(1000, 0) },
			NewSerial:   func() (string, error) { return "urn:uuid:11111111-1111-4111-8111-111111111111", nil },
		})
		if reference == nil {
			reference = got
			continue
		}
		if string(got) != string(reference) {
			t.Fatalf("attempt %d differs from the first:\n%s\n---\n%s", attempt, reference, got)
		}
	}
}

// #29 D4 is absolute: a component whose identity was not determined carries no purl. The
// product's own identity IS read from the package, so the root may carry one.
func TestNoInventedIdentities(t *testing.T) {
	doc, err := FromPackage(syntheticPackage(), Options{MsisVersion: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range doc.Components {
		if c.PURL != "" {
			t.Errorf("component %q carries purl %q; msis cannot know what those bytes are",
				c.BOMRef, c.PURL)
		}
		if !hasProperty(c.Properties, propIdentityUnknown) {
			t.Errorf("component %q does not record that its identity is undetermined", c.BOMRef)
		}
	}
	if doc.Metadata.Component.PURL == "" {
		t.Error("the product's identity is read from the package and should carry a purl")
	}
}

// An empty dependsOn asserts "depends on nothing". For an opaque payload that is false, so no
// dependency entry is emitted for one - compositions say the dependencies are unknown instead.
func TestOpaqueComponentsGetNoEmptyDependsOn(t *testing.T) {
	doc, err := FromPackage(syntheticPackage(), Options{MsisVersion: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range doc.Dependencies {
		if d.Ref != doc.Metadata.Component.BOMRef {
			t.Errorf("a dependency entry exists for %q; what an opaque payload depends on is "+
				"unknown and must be said through compositions", d.Ref)
		}
	}
	var unknown *Composition
	for i := range doc.Compositions {
		if doc.Compositions[i].Aggregate == aggregateUnknown {
			unknown = &doc.Compositions[i]
		}
	}
	if unknown == nil {
		t.Fatal("no composition marks the payload's contents unknown")
	}
	if len(unknown.Assemblies) != len(doc.Components) {
		t.Errorf("the unknown composition names %d components, want all %d - a completeness "+
			"declaration does not cascade", len(unknown.Assemblies), len(doc.Components))
	}
}

func syntheticPackage() *msiread.Package {
	return &msiread.Package{
		// A real file is needed: the subject artifact is hashed.
		Path: "testdata/cyclonedx/spdx.schema.json",
		Properties: map[string]string{
			"ProductName":    "Synthetic",
			"ProductVersion": "2.1.0",
			"Manufacturer":   "Acme",
			"UpgradeCode":    "{AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE}",
			"ProductCode":    "{11111111-2222-4333-8444-555555555555}",
		},
		Components: []msiread.Component{
			{ID: "C_A", GUID: "{GUID-A}", Directory: "INSTALLDIR"},
			{ID: "C_B", GUID: "{GUID-B}", Directory: "INSTALLDIR"},
		},
		Files: []msiread.File{
			{ID: "F1", Name: "a.dll", Target: `[INSTALLDIR]a.dll`, Component: "C_A",
				Sequence: 1, SHA256: strings.Repeat("a", 64)},
			{ID: "F2", Name: "b.dll", Target: `[INSTALLDIR]b.dll`, Component: "C_B",
				Sequence: 2, SHA256: strings.Repeat("b", 64)},
		},
		Binaries: []msiread.Binary{
			{Name: "CustomActionDll", Size: 10, SHA256: strings.Repeat("c", 64)},
		},
		Media: []msiread.Media{{DiskID: 1, Cabinet: "#c.cab", LastSequence: 2}},
	}
}

func mustBuild(t *testing.T, pkg *msiread.Package, opts Options) []byte {
	t.Helper()
	doc, err := FromPackage(pkg, opts)
	if err != nil {
		t.Fatalf("FromPackage: %v", err)
	}
	data, err := Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var check any
	if err := json.Unmarshal(data, &check); err != nil {
		t.Fatalf("the document is not JSON: %v", err)
	}
	return data
}

func hasProperty(props []Property, name string) bool {
	for _, p := range props {
		if p.Name == name {
			return true
		}
	}
	return false
}
