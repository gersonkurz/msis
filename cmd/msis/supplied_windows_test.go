//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/sbom"
	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

// A real build with a supplied component SBOM (#36). The unit tests drive the merge directly;
// this one exists because the three things that have to meet - the script, the tree the
// generator built, and the artifact the document is derived from - only meet in a real build.

const suppliedBuildScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="Composed"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{6F3A8D25-91CB-4E07-B4A2-7C15D9E03B88}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <sbom source="app.cdx.json" for="[INSTALLDIR]app.exe"/>
  <feature name="Main">
    <files source="app.exe" target="[INSTALLDIR]"/>
  </feature>
</setup>`

// componentDocument is what a build system hands msis: a document about one binary, with the
// digest of the bytes it produced.
func componentDocument(digest, compositions string) string {
	return `{
  "bomFormat": "CycloneDX",
  "specVersion": "1.6",
  "serialNumber": "urn:uuid:3b9e7c10-2222-4ddd-8eee-ffffffffffff",
  "version": 1,
  "metadata": {
    "component": {
      "type": "application",
      "bom-ref": "app",
      "name": "app.exe",
      "version": "1.0.0",
      "hashes": [{"alg": "SHA-256", "content": "` + digest + `"}]
    }
  },
  "components": [
    {
      "type": "library",
      "bom-ref": "zlib",
      "name": "zlib",
      "version": "1.3.1",
      "purl": "pkg:generic/zlib@1.3.1",
      "licenses": [{"license": {"id": "Zlib"}}]
    }
  ],
  "dependencies": [
    {"ref": "app", "dependsOn": ["zlib"]},
    {"ref": "zlib", "dependsOn": []}
  ]` + compositions + `
}`
}

func buildWithSuppliedSBOM(t *testing.T, compositions string) (*sbom.Document, []byte, string) {
	t.Helper()
	requireWix(t)
	dir := t.TempDir()

	payload := "the application binary, in spirit\n"
	write(t, filepath.Join(dir, "app.exe"), payload)
	write(t, filepath.Join(dir, "app.cdx.json"),
		componentDocument(sha256Hex([]byte(payload)), compositions))

	script := scriptFor(t, dir, "app.msi", suppliedBuildScript)
	buildWithSBOM(t, script, &cliArgs{})

	doc, raw := readDoc(t, filepath.Join(dir, "app.msi"))
	return doc, raw, dir
}

// The whole feature, end to end: a document supplied for one payload file comes out inside the
// installer's own, keeping its identity, its licences and its coverage statement, and the
// merged document still obeys every rule #29 fixed.
func TestASuppliedDocumentIsComposedIntoTheInstallersOwn(t *testing.T) {
	doc, raw, _ := buildWithSuppliedSBOM(t,
		`,"compositions": [{"aggregate": "complete", "dependencies": ["app", "zlib"]}]`)

	var zlib *sbom.Component
	var appRef string
	for i := range doc.Components {
		switch doc.Components[i].Name {
		case "zlib":
			zlib = &doc.Components[i]
		case "app.exe":
			if strings.Contains(doc.Components[i].BOMRef, "/supplied/") {
				appRef = doc.Components[i].BOMRef
			}
		}
	}
	if zlib == nil {
		t.Fatal("the supplied component is not in the installer's document")
	}
	if appRef == "" {
		t.Fatal("the supplied document's own subject was not imported")
	}
	if !strings.Contains(zlib.BOMRef, "/supplied/app.cdx.json/") {
		t.Errorf("ref %q is not namespaced by the document it came from", zlib.BOMRef)
	}

	// The supplier's identity is theirs, and it survives the move untouched.
	if got := compProp(*zlib, "msis:supplied.from"); got != "app.cdx.json" {
		t.Errorf("supplied.from = %q", got)
	}
	if !strings.Contains(string(raw), `"pkg:generic/zlib@1.3.1"`) {
		t.Error("the supplied purl did not survive")
	}
	if !strings.Contains(string(raw), `"Zlib"`) {
		t.Error("the supplied licence did not survive; msis does not model licences, so an " +
			"imported component has to be emitted as its author wrote it")
	}

	// And the whole document still conforms. The supplied names come from the supplied
	// document, not from the output, so a merge that dropped or invented one fails here.
	identified := []string{}
	for _, c := range doc.Components {
		if c.Name == "zlib" {
			identified = append(identified, c.BOMRef)
		}
	}
	for _, p := range conformance.Check(raw, conformance.Expected{
		PayloadNames:           []string{"app.exe"},
		IdentifiedComponents:   identified,
		SuppliedComponentNames: []string{"app.exe", "zlib"},
	}) {
		t.Errorf("conformance: %v", p)
	}
}

// What the merge must not imply. A reader seeing a dependency graph under msis's name would
// reasonably assume msis checked it; the document has to say, in itself, that it did not.
func TestTheMergedDocumentClaimsNothingAboutWhatItMerged(t *testing.T) {
	doc, _, _ := buildWithSuppliedSBOM(t, "")

	var note string
	for _, p := range doc.Metadata.Properties {
		if p.Name == "msis:supplied.document" {
			note = p.Value
		}
	}
	if note == "" {
		t.Fatal("the document does not record that anything was merged")
	}
	for _, want := range []string{"app.cdx.json", "app.exe", "establishes nothing"} {
		if !strings.Contains(note, want) {
			t.Errorf("%q does not mention %q", note, want)
		}
	}
	// It carried a digest and the build checked it, which is the ONE thing msis can say.
	if !strings.Contains(note, "matches the packaged bytes") {
		t.Errorf("%q does not say that the digest was checked", note)
	}
}

// A document about a different build of the same file describes different bytes, and attaching
// it would put a dependency graph on the wrong content. The build fails rather than publishes.
func TestADocumentForOtherBytesFailsTheBuild(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()

	write(t, filepath.Join(dir, "app.exe"), "the application binary, in spirit\n")
	write(t, filepath.Join(dir, "app.cdx.json"),
		componentDocument(strings.Repeat("9", 64), ""))

	script := scriptFor(t, dir, "app.msi", suppliedBuildScript)
	err := processFile(script, &cliArgs{
		build: true, sbom: true, setOverrides: map[string]string{},
		templateFolder: repoTemplates(t)})
	if err == nil {
		t.Fatal("a document describing different bytes was merged into the installer's own")
	}
	if !strings.Contains(err.Error(), "different build") {
		t.Errorf("%q does not say what is wrong", err)
	}
	// And nothing was written: a failed merge must not leave a half-true document behind.
	if _, statErr := os.Stat(sbom.SidecarPath(filepath.Join(dir, "app.msi"))); statErr == nil {
		t.Error("a document was written for a build that failed its digest check")
	}
}

// A `for` that names no file is a mistake in the script, and it is caught on EVERY build rather
// than only when /SBOM was asked for - finding it at the next release helps nobody.
func TestABrokenTargetFailsABuildWithoutSBOM(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()

	write(t, filepath.Join(dir, "app.exe"), "the application\n")
	write(t, filepath.Join(dir, "app.cdx.json"), componentDocument(strings.Repeat("0", 64), ""))
	broken := strings.Replace(suppliedBuildScript,
		`for="[INSTALLDIR]app.exe"`, `for="[INSTALLDIR]typo.exe"`, 1)

	script := scriptFor(t, dir, "app.msi", broken)
	err := processFile(script, &cliArgs{
		build: true, setOverrides: map[string]string{}, templateFolder: repoTemplates(t)})
	if err == nil {
		t.Fatal("a target naming no file passed a build")
	}
	if !strings.Contains(err.Error(), "typo.exe") {
		t.Errorf("%q does not name the target", err)
	}
}

const suppliedAutoBundleScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="ComposedBundle"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{0C41E7B9-3A62-4D18-8E55-B72F9A104D3C}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <requires type="vcredist" version="2022" source="stub-vcredist.exe"/>
  <sbom source="app.cdx.json" for="[INSTALLDIR]app.exe"/>
  <feature name="Main">
    <files source="app.exe" target="[INSTALLDIR]"/>
  </feature>
</setup>`

// An auto-bundle produces two documents, and a supplied one describes an installed FILE - which
// only the MSI has. The wrapper carries the MSI and links to its document (#33); repeating a
// file's contents there would say the bundle contains them directly.
func TestOnlyTheMSIsDocumentCarriesTheMerge(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()

	payload := "the application binary, in spirit\n"
	write(t, filepath.Join(dir, "app.exe"), payload)
	write(t, filepath.Join(dir, "stub-vcredist.exe"), "stands in for the VC++ redistributable\n")
	write(t, filepath.Join(dir, "app.cdx.json"), componentDocument(sha256Hex([]byte(payload)), ""))

	script := scriptFor(t, dir, "app.msi", suppliedAutoBundleScript)
	buildWithSBOM(t, script, &cliArgs{})

	msiDoc, _ := readDoc(t, filepath.Join(dir, "app.msi"))
	bundleDoc, _ := readDoc(t, filepath.Join(dir, "app.exe"))

	if got := suppliedNoteOf(msiDoc); got == "" {
		t.Error("the MSI's document does not carry the merge")
	}
	if got := suppliedNoteOf(bundleDoc); got != "" {
		t.Errorf("the bundle's document claims a file's contents directly: %q", got)
	}
	for _, c := range bundleDoc.Components {
		if compProp(c, "msis:supplied.from") != "" {
			t.Errorf("%s was imported into the wrapper's document", c.BOMRef)
		}
	}
}

func suppliedNoteOf(doc *sbom.Document) string {
	for _, p := range doc.Metadata.Properties {
		if p.Name == "msis:supplied.document" {
			return p.Value
		}
	}
	return ""
}

const bundleWithSBOMScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="BundleWithSBOM"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{5E8B21D7-64F0-4A39-9C82-1B37E6D5A094}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <sbom source="app.cdx.json" for="[INSTALLDIR]app.exe"/>
  <bundle>
    <msi source="inner\inner.msi"/>
  </bundle>
</setup>`

// A bundle installs nothing of its own, so `for` can name nothing in it. Saying that where the
// script is read beats failing later on a lookup that could not have succeeded.
func TestASBOMInABundleScriptIsRefused(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "app.cdx.json"), componentDocument(strings.Repeat("0", 64), ""))

	script := scriptFor(t, dir, "suite.exe", bundleWithSBOMScript)
	err := processFile(script, &cliArgs{setOverrides: map[string]string{}, templateFolder: repoTemplates(t)})
	if err == nil {
		t.Fatal("<sbom> was accepted in a script that installs no files")
	}
	for _, want := range []string{"[INSTALLDIR]app.exe", "installs no"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not mention %q", err, want)
		}
	}
}
