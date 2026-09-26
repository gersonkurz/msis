package sbom

import (
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

func analyzedFixture() *Analyzed {
	return &Analyzed{
		Tool: Component{Type: "application", Name: "syft", Version: "1.52.0",
			Hashes: []Hash{{Alg: "SHA-256", Content: strings.Repeat("d", 64)}}},
		Packages: []AnalyzedPackage{
			{FileID: "F1", Name: "pillow", Version: "10.4.0", PURL: "pkg:pypi/pillow@10.4.0",
				Cataloger: "python-installed-package-cataloger", Basis: "dist-info METADATA"},
			{FileID: "F2", Name: "logback-core", Version: "1.3.14", PURL: "pkg:maven/ch.qos.logback/logback-core@1.3.14",
				Cataloger: "java-archive-cataloger", Basis: "pom.properties"},
			{FileID: "F2", Name: "jetty-http", Version: "9.4.53", PURL: "pkg:maven/org.eclipse.jetty/jetty-http@9.4.53",
				Cataloger: "java-archive-cataloger", Basis: "pom.properties", Within: "lib/jetty-http.jar"},
		},
		Skipped: map[string]int{"from PE version resources": 3},
	}
}

// #82 (D25): each analyzed package becomes a component the file it was found in depends on,
// marked with the analyzer and the declaration it rests on, and the document passes the
// conformance rules - including the two-sided check that nothing was invented or dropped.
func TestAnalyzedPackagesAttachToTheirFiles(t *testing.T) {
	counts := AnalyzedCounts{}
	doc, err := FromPackage(syntheticPackage(), Options{MsisVersion: "test", Analyzed: analyzedFixture(),
		AnalyzedCounts: &counts})
	if err != nil {
		t.Fatal(err)
	}
	if counts != (AnalyzedCounts{Imported: 3, Files: 2}) {
		t.Errorf("counts = %+v", counts)
	}
	fileRef := map[string]string{}
	for _, c := range doc.Components {
		if id := propertyValueOf(c.Properties, propFileKey); id != "" {
			fileRef[id] = c.BOMRef
		}
	}
	dependsOn := map[string][]string{}
	for _, d := range doc.Dependencies {
		dependsOn[d.Ref] = d.DependsOn
	}
	for _, want := range []struct{ file, purl, basis string }{
		{"F1", "pkg:pypi/pillow@10.4.0", "dist-info METADATA"},
		{"F2", "pkg:maven/ch.qos.logback/logback-core@1.3.14", "pom.properties"},
		{"F2", "pkg:maven/org.eclipse.jetty/jetty-http@9.4.53", "pom.properties"},
	} {
		var found *Component
		for i := range doc.Components {
			if doc.Components[i].PURL == want.purl {
				found = &doc.Components[i]
			}
		}
		if found == nil {
			t.Fatalf("no component carries %s", want.purl)
		}
		if got := propertyValueOf(found.Properties, propAnalyzerBasis); got != want.basis {
			t.Errorf("%s rests on %q, want %q", want.purl, got, want.basis)
		}
		if got := propertyValueOf(found.Properties, propAnalyzedBy); got != "syft 1.52.0" {
			t.Errorf("%s analyzed by %q", want.purl, got)
		}
		if !strings.Contains(strings.Join(dependsOn[fileRef[want.file]], " "), found.BOMRef) {
			t.Errorf("file %s does not depend on %s (dependsOn %v)", want.file, found.BOMRef, dependsOn[fileRef[want.file]])
		}
	}
	if len(doc.Metadata.Tools.Components) != 2 || doc.Metadata.Tools.Components[1].Name != "syft" {
		t.Errorf("tools = %+v", doc.Metadata.Tools.Components)
	}
	if note := propertyValueOf(doc.Metadata.Properties, propAnalyzerDocument); !strings.Contains(note, "3 from PE version resources") {
		t.Errorf("the document's note does not say what was left out: %q", note)
	}

	data, err := Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	want := conformance.Expected{
		IdentifiedComponents:   []string{doc.Metadata.Component.BOMRef},
		AnalyzedComponentNames: []string{"pillow", "logback-core", "jetty-http"},
	}
	for _, p := range conformance.Check(data, want) {
		t.Errorf("conformance: %v", p)
	}
	// Two-sided: an analyzer finding the document does not carry, or one it carries unasked, fails.
	want.AnalyzedComponentNames = []string{"pillow", "logback-core"}
	if len(conformance.Check(data, want)) == 0 {
		t.Error("conformance accepted an analyzed component nobody reported")
	}
}

// A supplied SBOM is authoritative for its file (#36): the analyzer's packages for that file are
// dropped and counted, and the other file still gets its own.
func TestASuppliedSBOMKeepsItsFile(t *testing.T) {
	counts := AnalyzedCounts{}
	doc, err := FromPackage(syntheticPackage(), Options{MsisVersion: "test", Analyzed: analyzedFixture(),
		AnalyzedCounts: &counts, Supplied: []Supplied{forF1(suppliedDoc("", ""))}})
	if err != nil {
		t.Fatal(err)
	}
	if counts != (AnalyzedCounts{Imported: 2, Files: 1, Dropped: 1}) {
		t.Errorf("counts = %+v", counts)
	}
	for _, c := range doc.Components {
		if c.PURL == "pkg:pypi/pillow@10.4.0" {
			t.Error("the analyzer's package for a file a supplied SBOM describes was imported")
		}
	}
}

// The guard behind D25: an identity resting on anything but a package's own declaration is
// refused, not published - whatever the analyzer package decided.
func TestAnInferredIdentityIsRefused(t *testing.T) {
	a := analyzedFixture()
	a.Packages[0].Basis = "PE version resource"
	if _, err := FromPackage(syntheticPackage(), Options{MsisVersion: "test", Analyzed: a}); err == nil ||
		!strings.Contains(err.Error(), "D25") {
		t.Errorf("an inferred identity was accepted: %v", err)
	}
}
