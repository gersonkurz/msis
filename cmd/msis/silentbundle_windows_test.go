//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #95 (D31): a bundle built from the shipped templates must START. Until 3.0.7 the silent template
// gave WixStdBA Theme="none", and every silent bundle failed at BA creation with 0x80070490; the
// build tests only compiled bundles, which WiX does without complaint.
//
// /layout <dir> /quiet is the runtime check: Burn creates the BA (where #95 failed), detects,
// plans a Layout and applies it, copying the bundle (its MSI is embedded in it) into dir. It runs
// no package, needs no elevation and registers nothing. The bundles chain only local payloads, so
// nothing downloads.

// layOut runs bundle with /layout into a fresh folder and fails the test unless Burn created the
// BA, detected and planned the chained MSI for a layout, applied it successfully, and laid the
// bundle out byte for byte.
func layOut(t *testing.T, bundle string) {
	t.Helper()
	dir := t.TempDir()
	out, log := filepath.Join(dir, "layout"), filepath.Join(dir, "layout.log")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	err := exec.CommandContext(ctx, bundle, "/layout", out, "/quiet", "/norestart", "/log", log).Run()
	text, _ := os.ReadFile(log)
	if err != nil {
		t.Fatalf("%s /layout: %v\n%s", filepath.Base(bundle), err, text)
	}
	for _, want := range []string{"Detected package: MainPackage", "Planned package: MainPackage", "action: Layout", "Apply complete, result: 0x0"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("%s: the Burn log lacks %q:\n%s", filepath.Base(bundle), want, text)
		}
	}
	if got, want := fileSHA(t, filepath.Join(out, filepath.Base(bundle))), fileSHA(t, bundle); got != want {
		t.Errorf("%s: the laid-out bundle is not the built one (%s, want %s)", filepath.Base(bundle), got, want)
	}
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

const explicitBundleScript = `<?xml version="1.0" encoding="utf-8"?>
<setup{{SILENT}}>
  <set name="PRODUCT_NAME" value="StartsBundle"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{8C4F2A61-5D9B-4E37-A1C8-3F6E0B7D2A95}"/>
  {{LICENSE}}
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <bundle>
    <msi source="inner\inner.msi"/>
  </bundle>
</setup>`

// TestBundlesFromTheShippedTemplatesStart: the silent and the regular explicit bundle.
func TestBundlesFromTheShippedTemplatesStart(t *testing.T) {
	requireWix(t)
	for name, c := range map[string]struct{ silent, license string }{
		"silent":  {` silent="yes"`, ""},
		"regular": {"", `<set name="LICENSE_URL" value="https://example.com/license"/>`},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			buildInnerMSI(t, dir)
			body := strings.NewReplacer("{{SILENT}}", c.silent, "{{LICENSE}}", c.license).Replace(explicitBundleScript)
			script := scriptFor(t, dir, "suite.exe", body)
			if err := processFile(script, &cliArgs{build: true, retainWxs: true, setOverrides: map[string]string{}, templateFolder: repoTemplates(t)}); err != nil {
				t.Fatalf("building the bundle: %v", err)
			}
			wxs, err := os.ReadFile(filepath.Join(dir, "suite-bundle.wxs"))
			if err != nil {
				t.Fatal(err)
			}
			// The attribute, not the word: the silent template's comment mentions it.
			silent, shows := c.silent != "", strings.Contains(string(wxs), "bal:DisplayInternalUICondition='1'")
			if silent == shows {
				t.Errorf("silent=%v, but the MSI's own UI is shown=%v", silent, shows)
			}
			layOut(t, filepath.Join(dir, "suite.exe"))
		})
	}
}

const silentAutoBundleScript = `<?xml version="1.0" encoding="utf-8"?>
<setup silent="yes">
  <set name="PRODUCT_NAME" value="StartsAuto"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{3E7B9D24-6A1C-4F58-8B03-9D2E5C7A1F95}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <requires type="vcredist" version="2022" source="stub-vcredist.exe"/>
  <feature name="Main">
    <files source="app.txt" target="[INSTALLDIR]"/>
  </feature>
</setup>`

// TestASilentAutoBundleStarts: the auto-bundle wrapper around an MSI, silent, with a local stub
// standing in for the VC++ redistributable so the build downloads nothing.
func TestASilentAutoBundleStarts(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "app.txt"), "the application\n")
	standInExe(t, filepath.Join(dir, "stub-vcredist.exe"))
	script := scriptFor(t, dir, "app.msi", silentAutoBundleScript)
	if err := processFile(script, &cliArgs{build: true, setOverrides: map[string]string{}, templateFolder: repoTemplates(t)}); err != nil {
		t.Fatalf("building the auto-bundle: %v", err)
	}
	layOut(t, filepath.Join(dir, "app.exe"))
}

// TestABundleTemplateWithThemeNoneIsRefused: a custom bundle template still carrying 3.0.6's
// Theme="none" fails the build instead of producing a bundle that cannot start.
func TestABundleTemplateWithThemeNoneIsRefused(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "inner", "inner.msi"), "stands in for the chained MSI\n")
	shipped, err := os.ReadFile(filepath.Join(repoTemplates(t), "bundle-silent.wxs"))
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(string(shipped), `Theme="hyperlinkLicense"`, `Theme="none"`, 1)
	if old == string(shipped) {
		t.Fatal("the shipped silent template has no hyperlinkLicense theme to replace")
	}
	custom := filepath.Join(dir, "custom")
	write(t, filepath.Join(custom, "bundle-silent.wxs"), old)
	body := strings.NewReplacer("{{SILENT}}", ` silent="yes"`, "{{LICENSE}}", "").Replace(explicitBundleScript)
	script := scriptFor(t, dir, "suite.exe", body)
	err = processFile(script, &cliArgs{setOverrides: map[string]string{}, templateFolder: repoTemplates(t), customTemplates: custom})
	if err == nil || !strings.Contains(err.Error(), `Theme="none"`) || !strings.Contains(err.Error(), "#95") {
		t.Fatalf("want the Theme=\"none\" refusal, got %v", err)
	}
}
