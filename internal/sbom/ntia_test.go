package sbom

import (
	"reflect"
	"testing"

	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

// #29 D8, #59: an NTIA minimum element the artifact does not record is stated as unknown on the
// subject, and the field itself stays absent - no placeholder supplier or version is invented.
// The document is then accepted by conformance, which rejects the same gap left unstated.
func TestAnElementTheArtifactDoesNotRecordIsStatedUnknown(t *testing.T) {
	pkg := syntheticPackage()
	delete(pkg.Properties, "Manufacturer")
	delete(pkg.Properties, "ProductVersion")
	doc, err := FromPackage(pkg, Options{MsisVersion: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	root := doc.Metadata.Component
	if root.Version != "" || root.Supplier != nil || doc.Metadata.Supplier != nil || root.PURL != "" {
		t.Errorf("an absent element was filled in: version %q, supplier %v/%v, purl %q",
			root.Version, root.Supplier, doc.Metadata.Supplier, root.PURL)
	}
	want := []string{ // sorted, as every property list in the document is (D6)
		"supplier: the package records no Manufacturer",
		"version: the package records no ProductVersion",
	}
	if got := doc.NTIAUnknowns(); !reflect.DeepEqual(got, want) {
		t.Errorf("NTIAUnknowns = %q, want %q", got, want)
	}
	conforms(t, doc, conformance.Expected{})

	b := syntheticBundle("testdata/artifact.bin")
	b.Publisher = ""
	bdoc := bundleDoc(t, b)
	if got := bdoc.NTIAUnknowns(); !reflect.DeepEqual(got, []string{"supplier: the bundle records no Publisher"}) {
		t.Errorf("bundle NTIAUnknowns = %q", got)
	}
	conforms(t, bdoc, conformance.Expected{
		IdentifiedComponents: []string{bdoc.Metadata.Component.BOMRef},
		UnhashableComponents: UnhashablePayloads(b),
	})
}

// A package that records both says nothing is unknown.
func TestNothingIsUnknownWhenTheArtifactRecordsIt(t *testing.T) {
	doc, err := FromPackage(syntheticPackage(), Options{MsisVersion: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.NTIAUnknowns(); len(got) != 0 {
		t.Errorf("NTIAUnknowns = %q, want none", got)
	}
}

func conforms(t *testing.T, doc *Document, want conformance.Expected) {
	t.Helper()
	if want.IdentifiedComponents == nil && doc.Metadata.Component.PURL != "" {
		want.IdentifiedComponents = []string{doc.Metadata.Component.BOMRef}
	}
	data, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range conformance.Check(data, want) {
		t.Errorf("conformance: %v", p)
	}
}
