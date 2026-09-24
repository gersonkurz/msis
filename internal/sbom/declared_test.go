package sbom

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gersonkurz/msis/internal/buildrecord"
	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

func TestSameVersionAllowsOnlyTrailingZeroGroups(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"2.3.1", "2.3.1.0", true},
		{"2.3", "2.3.0.0", true},
		{"10.0", "10", true},
		{"2.3.1", "2.3.1", true},
		{"2.3.1", "2.4.0.0", false},
		{"2.3.10", "2.3.1", false},
		{"2.30", "2.3", false},
	} {
		if got := sameVersion(tc.a, tc.b); got != tc.same {
			t.Errorf("sameVersion(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.same)
		}
	}
}

func declaration(fields Declaration) Declaration {
	fields.Source = `setup.msis <component for="[INSTALLDIR]a.dll">`
	fields.Target = "[INSTALLDIR]a.dll"
	fields.FileID = "F1"
	return fields
}

// D16: a declaration's facts go onto the file's OWN component - where BSI TR-03183-2 looks for
// them - with the provenance of each field stated, and no component added.
func TestDeclaredFactsGoOntoTheFileItself(t *testing.T) {
	plain, err := FromPackage(syntheticPackage(), Options{MsisVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := FromPackage(syntheticPackage(), Options{MsisVersion: "test", Declared: []Declaration{declaration(Declaration{
		Name: "libfoo", Version: "2.3.1", Creator: "security@foo.example", License: "MIT", PURL: "pkg:nuget/Foo@2.3.1",
	})}})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Components) != len(plain.Components) {
		t.Errorf("%d components, want %d: a declaration adds no component", len(doc.Components), len(plain.Components))
	}
	ref := refWithName(plain, "a.dll")
	c := componentByRef(doc, ref)
	if c.Name != "libfoo" || c.Version != "2.3.1" || c.PURL != "pkg:nuget/Foo@2.3.1" {
		t.Errorf("name/version/purl %q/%q/%q", c.Name, c.Version, c.PURL)
	}
	if propertyValueOf(c.Properties, propBSIFilename) != "" && propertyValueOf(c.Properties, propBSIFilename) != "a.dll" {
		t.Errorf("the file's own name must stay in %s", propBSIFilename)
	}
	wantLic := []LicenseChoice{
		{License: &LicenseID{ID: "MIT", Acknowledgement: "declared"}},
		{License: &LicenseID{ID: "MIT", Acknowledgement: "concluded"}},
	}
	if !reflect.DeepEqual(c.Licenses, wantLic) {
		t.Errorf("licences %+v, want BSI's pair", c.Licenses)
	}
	if !reflect.DeepEqual(c.Manufacturer, &OrganizationalEntity{Contact: []OrganizationalContact{{Email: "security@foo.example"}}}) {
		t.Errorf("creator %+v", c.Manufacturer)
	}
	if got := propertyValueOf(c.Properties, propDeclaredBy); got != `setup.msis <component for="[INSTALLDIR]a.dll">` {
		t.Errorf("%s = %q", propDeclaredBy, got)
	}
	if got := propertyValueOf(c.Properties, propDeclaredFields); got != "creator,license,name,purl,version" {
		t.Errorf("%s = %q", propDeclaredFields, got)
	}
	if got := propertyValueOf(c.Properties, propIdentityUnknown); !strings.HasPrefix(got, "declared by ") {
		t.Errorf("%s = %q, want it to say the identity is declared", propIdentityUnknown, got)
	}
	conforms(t, doc, conformance.Expected{IdentifiedComponents: []string{doc.Metadata.Component.BOMRef, ref}})
}

// A compound expression can only be one CycloneDX 1.6 expression: it is given as the
// distribution licence BSI requires (D12, D16).
func TestACompoundDeclaredLicenceIsTheDistributionLicence(t *testing.T) {
	doc, err := FromPackage(syntheticPackage(), Options{MsisVersion: "test",
		Declared: []Declaration{declaration(Declaration{License: "Apache-2.0 OR MIT"})}})
	if err != nil {
		t.Fatal(err)
	}
	c := componentByRef(doc, refWithName(doc, "a.dll"))
	if !reflect.DeepEqual(c.Licenses, []LicenseChoice{{Expression: "Apache-2.0 OR MIT", Acknowledgement: "concluded"}}) {
		t.Errorf("licences %+v", c.Licenses)
	}
	conforms(t, doc, conformance.Expected{})
}

// Only an id on CycloneDX's SPDX list may be a license.id: a LicenseRef-, or an expression
// separated by tabs rather than spaces, is given as an expression - and the document conforms.
func TestADeclaredLicenceOutsideTheSPDXListIsAnExpression(t *testing.T) {
	for _, lic := range []string{"LicenseRef-acme-proprietary", "MIT	OR	Apache-2.0"} {
		doc, err := FromPackage(syntheticPackage(), Options{MsisVersion: "test",
			Declared: []Declaration{declaration(Declaration{License: lic})}})
		if err != nil {
			t.Fatal(err)
		}
		c := componentByRef(doc, refWithName(doc, "a.dll"))
		if !reflect.DeepEqual(c.Licenses, []LicenseChoice{{Expression: lic, Acknowledgement: "concluded"}}) {
			t.Errorf("%q: licences %+v, want one concluded expression", lic, c.Licenses)
		}
		conforms(t, doc, conformance.Expected{})
	}
}

// D13 on the file itself: a declared version that contradicts the recorded one refuses; a
// matching one keeps the recorded version; and a declared version REPLACES the build's date
// fallback (D15), which was msis's, not the package's.
func TestADeclaredVersionAndTheFilesOwn(t *testing.T) {
	pkg := syntheticPackage()
	pkg.Files[0].Version = "2.4.0.0"
	if _, err := FromPackage(pkg, Options{MsisVersion: "test", Declared: []Declaration{declaration(Declaration{Version: "2.3.1"})}}); err == nil ||
		!strings.Contains(err.Error(), "declares version 2.3.1") || !strings.Contains(err.Error(), "records version 2.4.0.0") {
		t.Fatalf("want a refusal naming both versions, got %v", err)
	}
	doc, err := FromPackage(pkg, Options{MsisVersion: "test", Declared: []Declaration{declaration(Declaration{Version: "2.4"})}})
	if err != nil {
		t.Fatal(err)
	}
	if c := componentByRef(doc, refWithName(doc, "a.dll")); c.Version != "2.4.0.0" {
		t.Errorf("a matching declaration changed the recorded version to %q", c.Version)
	}

	f := firstFile(t)
	rec := recordFor(t, buildrecord.File{FileID: f.ID, Source: "src/a.dll", SHA256: f.SHA256,
		Modified: time.Date(2025, 1, 15, 8, 30, 0, 0, time.UTC)})
	doc, err = FromPackage(syntheticPackage(), Options{MsisVersion: "test", Build: rec,
		Declared: []Declaration{declaration(Declaration{Version: "2.3.1"})}})
	if err != nil {
		t.Fatalf("a declared version was held to the date fallback: %v", err)
	}
	c := componentByRef(doc, refWithName(doc, "a.dll"))
	if c.Version != "2.3.1" || propertyValueOf(c.Properties, propBuildVersionFrom) != "" {
		t.Errorf("version %q / %s %q, want the declaration to replace the fallback",
			c.Version, propBuildVersionFrom, propertyValueOf(c.Properties, propBuildVersionFrom))
	}
}

// One file, one description: two declarations, or a declaration and a supplied document.
func TestOneDescriptionPerFile(t *testing.T) {
	d := declaration(Declaration{License: "MIT"})
	if err := OneDescriptionPerFile(nil, []Declaration{d, d}); err == nil {
		t.Error("two declarations for one file were accepted")
	}
	s := Supplied{Source: "a.cdx.json", Target: "[INSTALLDIR]a.dll", FileID: "F1"}
	if err := OneDescriptionPerFile([]Supplied{s}, []Declaration{d}); err == nil {
		t.Error("a declaration and a supplied document for one file were accepted")
	}
	if err := OneDescriptionPerFile([]Supplied{s}, nil); err != nil {
		t.Errorf("one description refused: %v", err)
	}
}
