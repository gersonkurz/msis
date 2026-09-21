//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/sbom"
)

// A real build with a VEX document (#37). The unit tests drive the evaluation directly; this one
// exists because the release being assessed, the inventory it is checked against and the file on
// disk only meet in a build.

const vexBuildScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="Assessed"/>
  <set name="PRODUCT_VERSION" value="{{VERSION}}"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{9A1F4C77-2E36-4B80-9D51-6C08E3B4A712}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <vex source="app.vex.json"/>
  <feature name="Main">
    <files source="lib.dll" target="[INSTALLDIR]"/>
  </feature>
</setup>`

// vexFor writes the team's VEX file: one assessment, recorded against a release.
func vexFor(assessed, appliesTo string) string {
	props := `{"name": "msis:vex.assessedProductVersion", "value": "` + assessed + `"}`
	if appliesTo != "" {
		props += `, {"name": "msis:vex.appliesToProductVersions", "value": "` + appliesTo + `"}`
	}
	return `{
  "bomFormat": "CycloneDX",
  "specVersion": "1.6",
  "version": 1,
  "vulnerabilities": [
    {
      "id": "CVE-2024-1234",
      "analysis": {"state": "not_affected", "justification": "code_not_reachable",
                   "detail": "the vulnerable entry point is never called"},
      "affects": [{"ref": "REF"}],
      "properties": [` + props + `]
    }
  ]
}`
}

// buildAssessed builds one release and returns the two documents beside the MSI.
func buildAssessed(t *testing.T, dir, version, assessed, appliesTo string) (bom, vexDoc *sbom.Document) {
	t.Helper()
	requireWix(t)

	write(t, filepath.Join(dir, "lib.dll"), "the library, unchanged across releases\n")

	// The ref a statement names is the inventory's, so the first build is made without a
	// usable VEX, the ref read out of the document it produced, and the file rewritten. That
	// is exactly what a team does once, by hand, when they start assessing.
	script := scriptFor(t, dir, "app.msi",
		strings.ReplaceAll(vexBuildScript, "{{VERSION}}", version))
	write(t, filepath.Join(dir, "app.vex.json"),
		strings.Replace(vexFor(assessed, appliesTo), "REF", libRefOf(t, dir, script), 1))

	buildWithSBOM(t, script, &cliArgs{})

	msi := filepath.Join(dir, "app.msi")
	bom, _ = readDoc(t, msi)
	vexDoc = readVEX(t, msi)
	return bom, vexDoc
}

// libRefOf builds once with a throwaway VEX to learn the ref of lib.dll in the inventory.
func libRefOf(t *testing.T, dir, script string) string {
	t.Helper()
	write(t, filepath.Join(dir, "app.vex.json"), vexFor("0.0.0", "*"))
	if err := processFile(script, &cliArgs{
		build: true, sbom: true, setOverrides: map[string]string{},
		templateFolder: repoTemplates(t)}); err != nil {
		t.Fatalf("the first build: %v", err)
	}
	doc, _ := readDoc(t, filepath.Join(dir, "app.msi"))
	for _, c := range doc.Components {
		if c.Name == "lib.dll" {
			return c.BOMRef
		}
	}
	t.Fatal("the inventory has no component for lib.dll")
	return ""
}

func readVEX(t *testing.T, artifact string) *sbom.Document {
	t.Helper()
	data, err := os.ReadFile(sbom.VEXSidecarPath(artifact))
	if err != nil {
		t.Fatalf("no VEX document beside %s: %v", filepath.Base(artifact), err)
	}
	var doc sbom.Document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return &doc
}

// The acceptance criterion of #37, end to end. The library is byte-identical between the two
// releases; the assessment was made for the first. It must not carry to the second on its own.
func TestAnAssessmentDoesNotCarryToTheNextRelease(t *testing.T) {
	requireWix(t)

	_, first := buildAssessed(t, t.TempDir(), "4.1", "4.1", "")
	if len(first.Vulnerabilities) != 1 {
		t.Fatalf("%d statements in the sidecar", len(first.Vulnerabilities))
	}
	if got := first.Vulnerabilities[0].Property(sbom.PropApplicability); got != sbom.ApplicabilityApplies {
		t.Fatalf("at 4.1 the assessment is %q, want it to apply", got)
	}

	_, second := buildAssessed(t, t.TempDir(), "4.2", "4.1", "")
	v := second.Vulnerabilities[0]
	if got := v.Property(sbom.PropApplicability); got != sbom.ApplicabilityNeedsReview {
		t.Fatalf("at 4.2 the assessment is %q; an unchanged library carried it forward", got)
	}
	if v.AnalysisState() == "not_affected" {
		t.Error("at 4.2 the document still says not_affected about a release nobody assessed")
	}
	if got := v.Property(sbom.PropPreviousState); got != "not_affected" {
		t.Errorf("what the assessment used to say was lost: %q", got)
	}

	// Widened deliberately, it does carry - that is the assessor's call, and it is visible.
	_, widened := buildAssessed(t, t.TempDir(), "4.2", "4.1", "4.2")
	if got := widened.Vulnerabilities[0].Property(sbom.PropApplicability); got != sbom.ApplicabilityApplies {
		t.Errorf("an explicitly widened assessment did not carry: %q", got)
	}
}

// The SBOM is what was shipped. Assessing it must not change it - otherwise the inventory and
// the assessment disagree about the same build, and only one of them was derived from bytes.
func TestTheSBOMIsUntouchedByTheVEXSidecar(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()

	bom, vexDoc := buildAssessed(t, dir, "4.1", "4.1", "")

	if len(bom.Vulnerabilities) != 0 {
		t.Error("the inventory carries vulnerabilities; assessments belong in the sidecar")
	}
	_, raw := readDoc(t, filepath.Join(dir, "app.msi"))
	if strings.Contains(string(raw), "msis:vex.") {
		t.Error("the inventory carries msis:vex vocabulary")
	}

	// Two documents, two serials: a BOM-Link addresses one of them.
	if vexDoc.SerialNumber == bom.SerialNumber {
		t.Error("the two documents share a serial")
	}
	// And the sidecar says which inventory it is about.
	want := "urn:cdx:" + strings.TrimPrefix(bom.SerialNumber, "urn:uuid:") + "/1"
	var linked bool
	for _, r := range vexDoc.Metadata.Component.ExternalReferences {
		if r.Type == "bom" && r.URL == want {
			linked = true
		}
	}
	if !linked {
		t.Errorf("the sidecar does not link to %s", want)
	}
}

// A path that does not exist is a mistake in the script, and it is reported on every build -
// finding it at release time, when the assessments were the point, helps nobody.
func TestAMissingVEXDocumentFailsTheBuild(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "lib.dll"), "the library\n")

	script := scriptFor(t, dir, "app.msi", strings.ReplaceAll(vexBuildScript, "{{VERSION}}", "1.0.0"))
	err := processFile(script, &cliArgs{
		build: true, setOverrides: map[string]string{}, templateFolder: repoTemplates(t)})
	if err == nil {
		t.Fatal("a <vex> naming no file passed a build")
	}
	if !strings.Contains(err.Error(), "app.vex.json") {
		t.Errorf("%q does not name the document", err)
	}
}

const vexAutoBundleScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="AssessedBundle"/>
  <set name="PRODUCT_VERSION" value="4.1"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{3B7D50E1-8C24-4F96-A0D3-51E9B26C7F48}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <requires type="vcredist" version="2022" source="stub-vcredist.exe"/>
  <vex source="app.vex.json"/>
  <feature name="Main">
    <files source="lib.dll" target="[INSTALLDIR]"/>
  </feature>
</setup>`

// An auto-bundle writes two documents. The statements are about installed components, which the
// MSI inventories and the wrapper does not - the wrapper carries the MSI itself and links to its
// document (#33). Annotating the wrapper would put an assessment beside an inventory that does
// not contain the thing assessed.
func TestTheVEXAnnotatesTheInventoryNotTheWrapper(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()

	write(t, filepath.Join(dir, "lib.dll"), "the library\n")
	write(t, filepath.Join(dir, "stub-vcredist.exe"), "stands in for the VC++ redistributable\n")
	script := scriptFor(t, dir, "app.msi", vexAutoBundleScript)
	write(t, filepath.Join(dir, "app.vex.json"), vexFor("4.1", ""))

	// The ref has to come from the MSI's inventory, so build once to learn it, then again.
	buildWithSBOM(t, script, &cliArgs{})
	msiDoc, _ := readDoc(t, filepath.Join(dir, "app.msi"))
	var ref string
	for _, c := range msiDoc.Components {
		if c.Name == "lib.dll" {
			ref = c.BOMRef
		}
	}
	if ref == "" {
		t.Fatal("the MSI's inventory has no component for lib.dll")
	}
	write(t, filepath.Join(dir, "app.vex.json"), strings.Replace(vexFor("4.1", ""), "REF", ref, 1))
	buildWithSBOM(t, script, &cliArgs{})

	// Beside the MSI, and evaluated against ITS inventory.
	v := readVEX(t, filepath.Join(dir, "app.msi"))
	if len(v.Vulnerabilities) != 1 {
		t.Fatalf("%d statements beside the MSI", len(v.Vulnerabilities))
	}
	if got := v.Vulnerabilities[0].Property(sbom.PropApplicability); got != sbom.ApplicabilityApplies {
		t.Errorf("the statement is %q; it was evaluated against a document that does not "+
			"contain what it assesses", got)
	}

	// And NOT beside the wrapper.
	if _, err := os.Stat(sbom.VEXSidecarPath(filepath.Join(dir, "app.exe"))); err == nil {
		t.Error("a VEX document was written beside the bundle, whose inventory is installers " +
			"rather than the files a statement is about")
	}
}
