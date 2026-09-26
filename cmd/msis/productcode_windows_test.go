//go:build windows

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/msiread"
)

// productCodeOf builds the folder fixture into dir and returns the built MSI's ProductCode.
func productCodeOf(t *testing.T, dir, script string, overrides map[string]string) string {
	t.Helper()
	msi := filepath.Join(dir, "folder.msi")
	os.Remove(msi)
	if err := processFile(script, &cliArgs{build: true, templateFolder: repoTemplates(t), setOverrides: overrides}); err != nil {
		t.Fatal(err)
	}
	pkg, err := msiread.Read(msi)
	if err != nil {
		t.Fatal(err)
	}
	return pkg.Properties["ProductCode"]
}

// #66 with the real wix: the ProductCode is derived from everything the package is built from.
// Two builds of one script get the same code; a changed payload file or a new version gets a
// new one - a rebuild still major-upgrades - and a script's own PRODUCT_CODE is used as given.
func TestTheProductCodeFollowsThePackagesInputs(t *testing.T) {
	dir, script := folderFixture(t, "")
	first := productCodeOf(t, dir, script, nil)
	if first == "" || strings.Contains(first, productCodePlaceholder) {
		t.Fatalf("ProductCode %q", first)
	}
	if again := productCodeOf(t, dir, script, nil); again != first {
		t.Errorf("a rebuild of the same inputs got %s, not %s", again, first)
	}
	if v := productCodeOf(t, dir, script, map[string]string{"PRODUCT_VERSION": "1.0.1"}); v == first {
		t.Error("a new version kept the ProductCode")
	}
	write(t, filepath.Join(dir, "lib", "a.txt"), "changed content\n")
	if changed := productCodeOf(t, dir, script, nil); changed == first {
		t.Error("a changed payload file kept the ProductCode")
	}
	const own = "{5D2E1C3B-7A9F-4E80-B1C2-D3E4F5A6B7C8}"
	if got := productCodeOf(t, dir, script, map[string]string{"PRODUCT_CODE": own}); got != own {
		t.Errorf("a script's PRODUCT_CODE became %s", got)
	}
}

// #81 with the real wix: one script, its sources named by absolute path - NG1's CI shape - built
// from two folders. The folder used to reach every component GUID, the component ids and, through
// the WXS, the ProductCode; the two packages must now agree on all three.
func TestTheBuildFolderDoesNotReachComponentIdentity(t *testing.T) {
	build := func(root string) *msiread.Package {
		t.Helper()
		dir := filepath.Join(root, "ng1-2.4.0-banking")
		write(t, filepath.Join(dir, "out", "app.exe"), "never executed\n")
		write(t, filepath.Join(dir, "out", "conf", "fastcgi.conf"), "conf\n")
		script := scriptFor(t, dir, "folder81.msi", `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="Folder81"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{8C2B4E61-3D7A-4F95-B0E2-6A1D9C3F5B72}"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <feature name="Main">
    <files source="`+filepath.Join(dir, "out", "app.exe")+`" target="[INSTALLDIR]"/>
    <files source="`+filepath.Join(dir, "out", "conf")+`" target="[INSTALLDIR]conf"/>
  </feature>
</setup>`)
		if err := processFile(script, &cliArgs{build: true, templateFolder: repoTemplates(t), setOverrides: map[string]string{}}); err != nil {
			t.Fatal(err)
		}
		pkg, err := msiread.Read(filepath.Join(dir, "folder81.msi"))
		if err != nil {
			t.Fatal(err)
		}
		return pkg
	}
	ci, local := build(t.TempDir()), build(filepath.Join(t.TempDir(), "Downloads"))
	if len(ci.Components) < 2 || !reflect.DeepEqual(ci.Components, local.Components) {
		t.Errorf("the build folder changed the components (ids, GUIDs):\n%+v\nvs\n%+v", ci.Components, local.Components)
	}
	if code := ci.Properties["ProductCode"]; code == "" || code != local.Properties["ProductCode"] {
		t.Errorf("the build folder changed the ProductCode: %s vs %s", code, local.Properties["ProductCode"])
	}
}

// #66's review, with the real wix: a custom template whose Property is a character-referenced
// preprocessor variable, &#36;(env.FLAVOR). WiX expands the decoded value - the two builds'
// Property differs with the environment - so msis must not derive a code for it: both builds
// take the explicit fallback and leave the ProductCode to WiX.
func TestAPreprocessorVariableTakesTheFallback(t *testing.T) {
	dir, script := folderFixture(t, "")
	tpl, err := os.ReadFile(filepath.Join(repoTemplates(t), "minimal", "template.wxs"))
	if err != nil {
		t.Fatal(err)
	}
	custom := strings.Replace(string(tpl), "<MajorUpgrade", `<Property Id="FLAVOR" Value="&#36;(env.FLAVOR)" />
    <MajorUpgrade`, 1)
	if custom == string(tpl) {
		t.Fatal("the minimal template has no <MajorUpgrade to insert the Property before")
	}
	templatePath := filepath.Join(dir, "custom.wxs")
	write(t, templatePath, custom)

	flavorOf := func(flavor string) (string, string) {
		t.Setenv("FLAVOR", flavor)
		os.Remove(filepath.Join(dir, "folder.msi"))
		out := capture(t, func() error {
			return processFile(script, &cliArgs{build: true, templateFolder: repoTemplates(t), template: templatePath, setOverrides: map[string]string{}})
		})
		if !strings.Contains(out, "ProductCode: generated by WiX - the WXS uses a WiX preprocessor variable") {
			t.Errorf("FLAVOR=%s: the build did not take the explicit fallback:\n%s", flavor, out)
		}
		pkg, err := msiread.Read(filepath.Join(dir, "folder.msi"))
		if err != nil {
			t.Fatal(err)
		}
		return pkg.Properties["FLAVOR"], pkg.Properties["ProductCode"]
	}
	a, codeA := flavorOf("standard")
	b, codeB := flavorOf("pro")
	if a != "standard" || b != "pro" {
		t.Fatalf("WiX expanded FLAVOR to %q and %q; the fixture does not exercise the preprocessor", a, b)
	}
	if codeA == codeB {
		t.Errorf("two packages differing in FLAVOR share the ProductCode %s", codeA)
	}
}
