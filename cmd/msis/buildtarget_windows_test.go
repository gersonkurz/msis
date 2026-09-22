//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// #42: a BUILD_TARGET whose directory does not exist yet. msis-2.x created it
// (BuildContext.CreateReleaseFolder); msis 3 failed on the first write with the OS's own
// message. The .wxs is the first artifact written and shares its directory with the .msi and
// the .exe, so its directory being created is the output directory being created. Two levels
// deep, so a plain Mkdir would not do. No /BUILD: the .wxs is all that is needed to see it.
func TestBuildTargetDirectoryIsCreatedForAnMSI(t *testing.T) {
	dir := payloadDir(t)
	script := scriptFor(t, dir, filepath.Join("nodir", "deeper", "probe.msi"), payloadScript)

	if err := processFile(script, &cliArgs{setOverrides: map[string]string{}, templateFolder: repoTemplates(t)}); err != nil {
		t.Fatalf("processFile: %v", err)
	}

	wxs := filepath.Join(dir, "nodir", "deeper", "probe.wxs")
	if _, err := os.Stat(wxs); err != nil {
		t.Fatalf("the .wxs was not written into the created directory: %v", err)
	}
}

const bundleInMissingDirScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="MissingDir"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{A1D4C6E8-2B57-4F90-8C13-6E5D2A9B7F42}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <bundle>
    <msi source="inner\inner.msi"/>
  </bundle>
</setup>`

// The explicit-bundle path writes its .wxs through the same helper.
func TestBuildTargetDirectoryIsCreatedForABundle(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "inner", "inner.msi"), "stands in for the chained MSI\n")
	script := scriptFor(t, dir, filepath.Join("out", "release", "suite.exe"), bundleInMissingDirScript)

	if err := processFile(script, &cliArgs{setOverrides: map[string]string{}, templateFolder: repoTemplates(t)}); err != nil {
		t.Fatalf("processFile: %v", err)
	}

	wxs := filepath.Join(dir, "out", "release", "suite-bundle.wxs")
	if _, err := os.Stat(wxs); err != nil {
		t.Fatalf("the bundle .wxs was not written into the created directory: %v", err)
	}
}
