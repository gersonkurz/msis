//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Round-1 review of #50: EnsurePrerequisites ran only when the prerequisite cache could be
// created, so a cache failure - LOCALAPPDATA pointing at a file, say - skipped verification of
// supplied sources and the no-digest warning, and the record then said "script-digest" merely
// because the attribute existed. The step now runs whether or not there is a cache, and the
// record is told what was verified. These tests force the cache to fail and check both paths.

// breakCache points LOCALAPPDATA at a regular file, so prereqcache.NewCache's MkdirAll fails.
func breakCache(t *testing.T, dir string) {
	t.Helper()
	blocker := filepath.Join(dir, "not-a-directory")
	write(t, blocker, "LOCALAPPDATA is a file, so no cache directory can be created under it\n")
	t.Setenv("LOCALAPPDATA", blocker)
}

func TestWithoutACacheASuppliedPrerequisiteWithAWrongDigestIsStillRefused(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	chainableMSI(t, dir)
	write(t, filepath.Join(dir, "vcstub.exe"), "stands in for the redistributable\n")
	breakCache(t, dir)

	script := suppliedShaScript(t, dir, ` sha256="`+strings.Repeat("0", 64)+`"`)
	err := processFile(script, &cliArgs{build: true, setOverrides: map[string]string{}, templateFolder: repoTemplates(t)})
	if err == nil {
		t.Fatal("with the cache unavailable, a mismatching supplied prerequisite was chained")
	}
	if !strings.Contains(err.Error(), "does not match the sha256=") {
		t.Errorf("error = %v, want the mismatch refused", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "suite.exe")); err == nil {
		t.Error("the bundle was built although verification failed")
	}
}

func TestWithoutACacheASuppliedPrerequisiteWithoutADigestIsRecordedAsUnverified(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	chainableMSI(t, dir)
	write(t, filepath.Join(dir, "vcstub.exe"), "stands in for the redistributable\n")
	breakCache(t, dir)

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

// The auto-bundle path: a <requires source= sha256=> whose file does not match, with no cache.
const suppliedShaRequiresScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="SuppliedShaRequires"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{8D2E4F61-0B7A-4C93-A5D8-6E1F3B9C2A74}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <requires type="vcredist" version="2022" source="vcstub.exe" sha256="{{SHA}}"/>
  <feature name="Main">
    <files source="app.txt" target="[INSTALLDIR]"/>
  </feature>
</setup>`

func TestWithoutACacheAnAutoBundlesSuppliedRequirementWithAWrongDigestIsRefused(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "app.txt"), "the application\n")
	write(t, filepath.Join(dir, "vcstub.exe"), "stands in for the redistributable\n")
	breakCache(t, dir)

	body := strings.ReplaceAll(suppliedShaRequiresScript, "{{SHA}}", strings.Repeat("0", 64))
	script := scriptFor(t, dir, "app.msi", body)
	err := processFile(script, &cliArgs{build: true, setOverrides: map[string]string{}, templateFolder: repoTemplates(t)})
	if err == nil {
		t.Fatal("with the cache unavailable, the auto-bundle chained a mismatching supplied requirement")
	}
	if !strings.Contains(err.Error(), "does not match the sha256=") {
		t.Errorf("error = %v, want the mismatch refused", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "app.exe")); err == nil {
		t.Error("the auto-bundle was built although verification failed")
	}
}
