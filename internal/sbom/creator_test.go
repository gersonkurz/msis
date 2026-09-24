package sbom

import (
	"reflect"
	"testing"

	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

// #64: the product creator's contact is read from the package, and only what IS a URL or an
// email address is taken - ARPURLINFOABOUT and ARPCONTACT are free text in a package msis did
// not build.
func TestTheProductCreatorComesFromThePackage(t *testing.T) {
	pkg := syntheticPackage()
	pkg.Properties["ARPURLINFOABOUT"] = "https://acme.example"
	pkg.Properties["ARPCONTACT"] = "support@acme.example"
	doc, err := FromPackage(pkg, Options{MsisVersion: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	// Both are in the package; BSI takes the email, and the URL only when there is none.
	want := &OrganizationalEntity{Name: "Acme", Contact: []OrganizationalContact{{Email: "support@acme.example"}}}
	if got := doc.Metadata.Component.Manufacturer; !reflect.DeepEqual(got, want) {
		t.Errorf("manufacturer %+v, want %+v", got, want)
	}
	conforms(t, doc, conformance.Expected{})

	pkg.Properties["ARPURLINFOABOUT"] = "acme.example"
	pkg.Properties["ARPCONTACT"] = "Call us, we are friendly"
	doc, err = FromPackage(pkg, Options{MsisVersion: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Metadata.Component.Manufacturer; got != nil {
		t.Errorf("free-text ARP values became a creator: %+v", got)
	}

	// With no usable email, the URL is the fallback.
	pkg.Properties["ARPURLINFOABOUT"] = "https://acme.example"
	doc, err = FromPackage(pkg, Options{MsisVersion: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := doc.Metadata.Component.Manufacturer, (&OrganizationalEntity{Name: "Acme", URL: []string{"https://acme.example"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("URL fallback: manufacturer %+v, want %+v", got, want)
	}
	if doc.Metadata.Manufacturer != nil {
		t.Error("an artifact read on its own named an SBOM creator; only the build can")
	}
}

func TestABundlesCreatorIsItsAboutURL(t *testing.T) {
	b := syntheticBundle("testdata/artifact.bin")
	b.AboutURL = "https://someone.example/about"
	doc := bundleDoc(t, b)
	want := &OrganizationalEntity{Name: "Someone", URL: []string{"https://someone.example/about"}}
	if got := doc.Metadata.Component.Manufacturer; !reflect.DeepEqual(got, want) {
		t.Errorf("manufacturer %+v, want %+v", got, want)
	}
}

// The SBOM's own creator is named by the build (SBOM_CREATOR), as an email contact or a URL.
func TestTheSBOMCreatorComesFromTheBuild(t *testing.T) {
	for creator, want := range map[string]*OrganizationalEntity{
		"sbom@acme.example":     {Contact: []OrganizationalContact{{Email: "sbom@acme.example"}}},
		"https://acme.example/": {URL: []string{"https://acme.example/"}},
	} {
		rec := recordFor(t)
		rec.SBOMCreator = creator
		doc, err := docWith(t, rec)
		if err != nil {
			t.Fatal(err)
		}
		if got := doc.Metadata.Manufacturer; !reflect.DeepEqual(got, want) {
			t.Errorf("SBOM_CREATOR %q: metadata.manufacturer %+v, want %+v", creator, got, want)
		}
	}
}

// #62: the document's data licence is the build's grant (SBOM_DATA_LICENSE), never msis's
// default: an SBOM msis writes for a customer's product is the customer's document.
func TestTheDataLicenceIsTheBuildsGrant(t *testing.T) {
	rec := recordFor(t)
	rec.DataLicense = "CC0-1.0"
	doc, err := docWith(t, rec)
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Metadata.Licenses; !reflect.DeepEqual(got, []LicenseExpression{{Expression: "CC0-1.0"}}) {
		t.Errorf("metadata.licenses %+v, want CC0-1.0", got)
	}
	conforms(t, doc, conformance.Expected{})

	plain, err := docWith(t, recordFor(t))
	if err != nil {
		t.Fatal(err)
	}
	if plain.Metadata.Licenses != nil {
		t.Errorf("no grant, but metadata.licenses %+v", plain.Metadata.Licenses)
	}
}
