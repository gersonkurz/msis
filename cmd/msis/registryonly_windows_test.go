//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// #56: the minimal templates route through InstallDirDlg only when the package declares an
// INSTALLDIR. A registry-only package goes Welcome -> VerifyReady, with no SetTargetPath on a
// directory that does not exist; a package with files keeps the dialog. Built with the real wix
// so the reduced UI is known to compile, and the retained WXS is checked for the flow.
func TestMinimalTemplatesShowInstallDirDlgOnlyWithAnInstallDir(t *testing.T) {
	requireWix(t)
	for _, tc := range []struct {
		template, platform string
		files, wantDialog  bool
	}{
		{"minimal", "x64", false, false},
		{"minimal-x86", "x86", false, false},
		{"minimal", "x64", true, true},
		{"minimal-x86", "x86", true, true},
	} {
		name := fmt.Sprintf("%s/files=%v", tc.template, tc.files)
		dir := t.TempDir()
		write(t, filepath.Join(dir, "settings.reg"),
			"Windows Registry Editor Version 5.00\n\n[HKEY_LOCAL_MACHINE\\SOFTWARE\\Minimal56]\n\"V\"=\"1\"\n")
		body := strings.Replace(registryOnlyScript, `value="x64"`, `value="`+tc.platform+`"`, 1)
		if tc.files {
			write(t, filepath.Join(dir, "app.txt"), "x")
			body = strings.Replace(body, `<registry file="settings.reg"/>`,
				`<registry file="settings.reg"/><files source="app.txt" target="[INSTALLDIR]"/>`, 1)
		}
		script := scriptFor(t, dir, "minimal.msi", body)
		templates := repoTemplates(t)
		args := &cliArgs{build: true, retainWxs: true, setOverrides: map[string]string{},
			templateFolder: templates, template: filepath.Join(templates, tc.template, "template.wxs")}
		if err := processFile(script, args); err != nil {
			t.Fatalf("%s: must build: %v", name, err)
		}
		wxs, err := filepath.Glob(filepath.Join(dir, "*.wxs"))
		if err != nil || len(wxs) != 1 {
			t.Fatalf("%s: expected one retained .wxs, got %v (%v)", name, wxs, err)
		}
		content, err := os.ReadFile(wxs[0])
		if err != nil {
			t.Fatal(err)
		}
		out := string(content)
		// The whole navigation contract: each marker is present exactly when the dialog is wanted
		// (withDialog) or exactly when it is not.
		for _, m := range []struct {
			marker     string
			withDialog bool
		}{
			{`<DialogRef Id="InstallDirDlg" />`, true},
			{`<Property Id="WIXUI_INSTALLDIR" Value="INSTALLDIR" />`, true},
			{`Event="SetTargetPath"`, true},
			{`<Publish Dialog="VerifyReadyDlg" Control="Back" Event="NewDialog" Value="InstallDirDlg" />`, true},
			{`<Publish Dialog="WelcomeDlg" Control="Next" Event="NewDialog" Value="VerifyReadyDlg" />`, false},
			{`<Publish Dialog="VerifyReadyDlg" Control="Back" Event="NewDialog" Value="WelcomeDlg" />`, false},
		} {
			want := m.withDialog == tc.wantDialog
			if got := strings.Contains(out, m.marker); got != want {
				t.Errorf("%s: %s present = %v, want %v", name, m.marker, got, want)
			}
		}
	}
}
