package sbom

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/msiread"
	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

// A registry-only installer has no files and no Binary streams. Nil slices marshal as JSON
// null, and both "components" and "dependsOn" are declared as arrays, so that package used to
// produce a document the schema rejects.
func TestEmptyInventoryIsStillValid(t *testing.T) {
	pkg := &msiread.Package{
		Path: "testdata/cyclonedx/spdx.schema.json",
		Properties: map[string]string{
			"ProductName": "RegistryOnly", "ProductVersion": "1.0",
			"Manufacturer": "Acme", "UpgradeCode": "{AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE}",
		},
	}
	doc, err := FromPackage(pkg, Options{MsisVersion: "test"})
	if err != nil {
		t.Fatalf("FromPackage: %v", err)
	}
	data, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if got := string(raw["components"]); got != "[]" {
		t.Errorf("components = %s, want an empty array rather than null", got)
	}
	if strings.Contains(string(data), "null") {
		t.Errorf("the document contains a null where the schema wants an array:\n%s", data)
	}

	for _, p := range conformance.Check("testdata/cyclonedx", data, conformance.Expected{
		IdentifiedComponents: []string{doc.Metadata.Component.BOMRef},
	}) {
		t.Errorf("conformance: %v", p)
	}
}

// #29 D5 admits no exceptions. The reader deliberately returns unhashed files when a cabinet
// did not travel with the package, and that is a reason the artifact cannot be described - not
// a licence to publish a document that looks complete but cannot be verified against anything.
//
// Refusing here also means nothing is written: Write never runs, so no existing sidecar is
// disturbed by an emission that was never going to be valid.
func TestRefusesToEmitWithoutEveryDigest(t *testing.T) {
	pkg := syntheticPackage()
	pkg.Files[1].SHA256 = "" // as the reader leaves it for an unavailable cabinet
	pkg.Media = []msiread.Media{{
		DiskID: 2, Cabinet: "external.cab", LastSequence: 9,
		Unavailable: "cabinet external.cab is external to the package and was not read",
	}}

	_, err := FromPackage(pkg, Options{MsisVersion: "test"})
	if err == nil {
		t.Fatal("a document with an unhashed payload must not be emitted")
	}
	if !strings.Contains(err.Error(), "b.dll") {
		t.Errorf("error = %v, want it to name the component with no digest", err)
	}
	if !strings.Contains(err.Error(), "external.cab") {
		t.Errorf("error = %v, want it to carry the reader's reason", err)
	}
}

// Percent-encoding, not substitution: two different destinations must not collapse onto one
// ref. Replacing spaces with hyphens merged "a b.dll" and "a-b.dll", and within one MSI
// component the discriminator cannot separate them either.
func TestRefEncodingDoesNotMergeDistinctTargets(t *testing.T) {
	const ns, guid = "msis/abc", "{GUID-A}"
	spaced := fileRef(ns, fileOf("a b.dll", `[INSTALLDIR]a b.dll`, "C_SAME", "F1", 1), guid)
	hyphen := fileRef(ns, fileOf("a-b.dll", `[INSTALLDIR]a-b.dll`, "C_SAME", "F2", 2), guid)
	if spaced == hyphen {
		t.Fatalf("two destinations collapsed onto one ref: %q", spaced)
	}
	if strings.ContainsAny(spaced, " ") {
		t.Errorf("ref %q contains a raw space", spaced)
	}
}

// A purl segment must survive characters that mean something in a purl. "Tool#Pro" became
// "pkg:generic/Tool#Pro@1.0", where the "#" begins a subpath and the identity quietly changed.
func TestPurlEscapingPreservesIdentity(t *testing.T) {
	cases := map[string]string{
		"Tool#Pro":  "Tool%23Pro",
		"a/b":       "a%2fb",
		"100% Pure": "100%25%20Pure",
		"q?x":       "q%3fx",
		"Plain-1.0": "Plain-1.0",
	}
	for in, want := range cases {
		if got := purlEscape(in); got != want {
			t.Errorf("purlEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
