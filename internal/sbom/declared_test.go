package sbom

import (
	"strings"
	"testing"
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

func declaredFor(version string) Supplied {
	data := `{"bomFormat":"CycloneDX","specVersion":"1.6","version":1,"metadata":{"component":` +
		`{"type":"library","bom-ref":"declared","name":"a.dll","version":"` + version + `"}}}`
	return Supplied{Source: `setup.msis <component for="[INSTALLDIR]a.dll">`, Target: "[INSTALLDIR]a.dll",
		FileID: "F1", Data: []byte(data), Declared: true}
}

// #64, the product owner's decision: a declared version that contradicts the version resource
// the package records for the file refuses the document, naming both. A matching one - allowing
// trailing ".0" groups - merges.
func TestADeclaredVersionMustAgreeWithTheFile(t *testing.T) {
	pkg := syntheticPackage()
	pkg.Files[0].Version = "2.4.0.0"

	_, err := FromPackage(pkg, Options{MsisVersion: "test", Supplied: []Supplied{declaredFor("2.3.1")}})
	if err == nil || !strings.Contains(err.Error(), "declares version 2.3.1") ||
		!strings.Contains(err.Error(), "records version 2.4.0.0") {
		t.Fatalf("want a refusal naming both versions, got %v", err)
	}

	doc, err := FromPackage(pkg, Options{MsisVersion: "test", Supplied: []Supplied{declaredFor("2.4")}})
	if err != nil {
		t.Fatalf("a matching declaration was refused: %v", err)
	}
	if refWithName(doc, "a.dll") == "" {
		t.Fatal("the file is missing")
	}

	// A supplied document (<sbom>) is not held to the file's version: its author describes what
	// the file contains, whose version need not be the file's own.
	supplied := declaredFor("2.3.1")
	supplied.Declared = false
	if _, err := FromPackage(pkg, Options{MsisVersion: "test", Supplied: []Supplied{supplied}}); err != nil {
		t.Errorf("a supplied document was held to the file's version: %v", err)
	}
}
