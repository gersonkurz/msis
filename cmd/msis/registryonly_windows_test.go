//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

const registryOnlyScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="RegistryOnly"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{6B2E8F41-9D35-4A0C-B4F7-2C3D5E6F9A54}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <feature name="Main">
    <registry file="settings.reg"/>
  </feature>
</setup>`

// #54 end to end: a package whose only content is a <registry> item builds with the real wix.
// It failed with "WIX0094: The identifier 'Directory:INSTALLDIR' could not be found" because
// the root feature named a ConfigurableDirectory the package never declared.
func TestARegistryOnlyPackageBuilds(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "settings.reg"),
		"Windows Registry Editor Version 5.00\n\n[HKEY_LOCAL_MACHINE\\SOFTWARE\\RegistryOnly54]\n\"V\"=\"1\"\n")
	script := scriptFor(t, dir, "regonly.msi", registryOnlyScript)

	if err := processFile(script, &cliArgs{build: true, setOverrides: map[string]string{}, templateFolder: repoTemplates(t)}); err != nil {
		t.Fatalf("a registry-only package must build: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "regonly.msi")); err != nil {
		t.Fatalf("no MSI was written: %v", err)
	}
}
