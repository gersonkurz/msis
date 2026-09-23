//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #50 end to end: a <prerequisite source=> with sha256= is verified against the file WiX binds
// before anything is rendered or built, a mismatch refuses the build, and the SBOM says what
// each prerequisite's bytes were checked against.

const suppliedShaBundleScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="SuppliedSha"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{3F9A1C2E-7D45-4B08-9E61-5C2D8A0B4F17}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <bundle>
    <prerequisite type="vcredist" version="2022" source="vcstub.exe"{{SHA}}/>
    <msi source="inner\inner.msi"/>
  </bundle>
</setup>`

func suppliedShaScript(t *testing.T, dir, shaAttr string) string {
	t.Helper()
	return scriptFor(t, dir, filepath.Join("out", "suite.exe"), strings.ReplaceAll(suppliedShaBundleScript, "{{SHA}}", shaAttr))
}

func TestASuppliedPrerequisiteWithAWrongDigestIsRefusedBeforeTheBuild(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	chainableMSI(t, dir)
	write(t, filepath.Join(dir, "vcstub.exe"), "stands in for the redistributable\n")

	script := suppliedShaScript(t, dir, ` sha256="`+strings.Repeat("0", 64)+`"`)
	err := processFile(script, &cliArgs{build: true, setOverrides: map[string]string{}, templateFolder: repoTemplates(t)})
	if err == nil {
		t.Fatal("a supplied prerequisite whose digest does not match was chained")
	}
	for _, want := range []string{"vcredist 2022", "does not match the sha256=", "expected " + strings.Repeat("0", 64)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	// Refused before rendering: no bundle .wxs and no .exe were written.
	for _, p := range []string{filepath.Join(dir, "out", "suite-bundle.wxs"), filepath.Join(dir, "out", "suite.exe")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s exists although the build was refused", p)
		}
	}
}

func TestASuppliedPrerequisiteWithAMatchingDigestBuildsAndIsRecordedAsScriptVerified(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	chainableMSI(t, dir)
	body := "stands in for the redistributable\n"
	write(t, filepath.Join(dir, "vcstub.exe"), body)

	script := suppliedShaScript(t, dir, ` sha256="`+strings.ToUpper(sha256Hex([]byte(body)))+`"`)
	buildWithSBOM(t, script, &cliArgs{})

	doc, _ := readDoc(t, filepath.Join(dir, "out", "suite.exe"))
	verification := ""
	for _, c := range doc.Components {
		if compProp(c, "msis:build.source") == "vcstub.exe" {
			verification = compProp(c, "msis:prerequisite.verification")
		}
	}
	if verification != "script-digest" {
		t.Errorf("msis:prerequisite.verification = %q, want script-digest. Unresolved: %v",
			verification, allMetaProps(doc, "msis:build.unresolved"))
	}
}

func TestASuppliedPrerequisiteWithoutADigestIsRecordedAsUnverified(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	chainableMSI(t, dir)
	write(t, filepath.Join(dir, "vcstub.exe"), "stands in for the redistributable\n")

	script := suppliedShaScript(t, dir, "")
	buildWithSBOM(t, script, &cliArgs{})

	doc, _ := readDoc(t, filepath.Join(dir, "out", "suite.exe"))
	verification := ""
	for _, c := range doc.Components {
		if compProp(c, "msis:build.source") == "vcstub.exe" {
			verification = compProp(c, "msis:prerequisite.verification")
		}
	}
	if verification != "unverified" {
		t.Errorf("msis:prerequisite.verification = %q, want unverified", verification)
	}
}
