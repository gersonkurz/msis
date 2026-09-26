package main

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/buildrecord"
	"github.com/gersonkurz/msis/internal/variables"
)

// #66: every file the WXS names for WiX to package - a payload Source, a Binary or Icon
// SourceFile, and the file-valued WixVariables - and nothing else.
func TestReferencedFiles(t *testing.T) {
	wxs := `<Wix><Package>
  <Binary Id="b" SourceFile="hooks\x64\msi-simplica.dll"/>
  <Icon Id="i" SourceFile="setup.ico"/>
  <WixVariable Id="WixUIBannerBmp" Value="banner.bmp"/>
  <WixVariable Id="WixUILicenseRtf" Value="license.rtf"/>
  <WixVariable Id="SomethingElse" Value="not-a-file"/>
  <Property Id="ARPURLINFOABOUT" Value="https://x.example"/>
  <Component><File Id='F1' Source='bin\app.exe'/><File Id='F2' Source='bin\app.exe'/></Component>
</Package></Wix>`
	got, err := referencedFiles(wxs)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"banner.bmp", `bin\app.exe`, `hooks\x64\msi-simplica.dll`, "license.rtf", "setup.ico"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("referencedFiles = %v, want %v", got, want)
	}
}

// The code is a Windows Installer GUID, upper case in braces, marked name-based (version 8).
func TestGuidOf(t *testing.T) {
	g := guidOf(make([]byte, 32))
	if !regexp.MustCompile(`^\{[0-9A-F]{8}-[0-9A-F]{4}-8[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}\}$`).MatchString(g) {
		t.Errorf("guidOf = %q", g)
	}
}

// A referenced file the bind paths do not hold means an input the code cannot see: no code is
// derived, the reason names the file, and applyProductCode drops the attribute for WiX to fill.
func TestAnUnresolvableReferenceDerivesNoCode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "present.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := buildrecord.New(buildrecord.PathMSI, filepath.Join(dir, "setup.msis"), []buildrecord.BindPath{{Name: "wxs", Dir: dir}})
	vars := variables.Dictionary{"UPGRADE_CODE": "{9E4D3A86-5F7C-4B03-9D29-4A8C6E0F3B86}", "PRODUCT_VERSION": "1.0.0"}
	wxs := `<Wix><Package ProductCode="` + productCodePlaceholder + `"><File Source="present.txt"/><File Source="missing.txt"/></Package></Wix>`
	if code, why := productCode(wxs, vars, rec, dir, nil); code != "" || !strings.Contains(why, "missing.txt") {
		t.Errorf("productCode = %q, %q; want none, naming missing.txt", code, why)
	}
	present := `<Wix><Package ProductCode="` + productCodePlaceholder + `"><File Source="present.txt"/></Package></Wix>`
	a, _ := productCode(present, vars, rec, dir, nil)
	b, _ := productCode(present, vars, rec, dir, nil)
	if a == "" || a != b {
		t.Errorf("the same inputs gave %q and %q", a, b)
	}
	if err := os.WriteFile(filepath.Join(dir, "present.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, _ := productCode(present, vars, rec, dir, nil); c == a {
		t.Error("a changed file kept the ProductCode")
	}
}

// #81: a script with absolute sources, built from another folder, gets the same ProductCode -
// where the sources sit is not an input. What each file contains, and which file goes where,
// still is: swapping two files' contents changes the code.
func TestTheProductCodeIgnoresWhereSourcesSit(t *testing.T) {
	vars := variables.Dictionary{"UPGRADE_CODE": "{9E4D3A86-5F7C-4B03-9D29-4A8C6E0F3B86}", "PRODUCT_VERSION": "1.0.0"}
	codeIn := func(dir, first, second string) string {
		t.Helper()
		for name, content := range map[string]string{"one.txt": first, "two.txt": second} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		rec := buildrecord.New(buildrecord.PathMSI, filepath.Join(dir, "setup.msis"), []buildrecord.BindPath{{Name: "wxs", Dir: dir}})
		wxs := `<Wix><Package ProductCode="` + productCodePlaceholder + `"><File Source="` + filepath.Join(dir, "one.txt") +
			`"/><File Source='` + filepath.Join(dir, "two.txt") + `'/></Package></Wix>`
		code, why := productCode(wxs, vars, rec, dir, nil)
		if code == "" {
			t.Fatalf("no code derived: %s", why)
		}
		return code
	}
	ci := codeIn(filepath.Join(t.TempDir()), "a", "b")
	local := codeIn(filepath.Join(t.TempDir()), "a", "b")
	if ci != local {
		t.Errorf("the same package built from two folders got %s and %s", ci, local)
	}
	if swapped := codeIn(t.TempDir(), "b", "a"); swapped == ci {
		t.Error("swapping two files' contents kept the ProductCode")
	}

	// #81's review: WiX installs a <File> without Name under its Source's name, so the same
	// bytes under another file name are a different package.
	dir := t.TempDir()
	rec := buildrecord.New(buildrecord.PathMSI, filepath.Join(dir, "setup.msis"), []buildrecord.BindPath{{Name: "wxs", Dir: dir}})
	named := func(name string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("same bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
		code, why := productCode(`<Wix><Package ProductCode="`+productCodePlaceholder+`"><File Id="F" Source="`+
			filepath.Join(dir, name)+`"/></Package></Wix>`, vars, rec, dir, nil)
		if code == "" {
			t.Fatalf("no code derived: %s", why)
		}
		return code
	}
	if named("one.txt") == named("two.txt") {
		t.Error("the same bytes installed under another file name kept the ProductCode")
	}
}

// #66's review: WiX's preprocessor can add inputs the hash never sees, so a WXS that uses it
// derives no code - an <?include?>, a <?define?>, a $(env.X). WiX's escaped "$$" is a plain
// dollar, not a preprocessor reference, and the file it names is found with the single "$".
func TestThePreprocessorLeavesTheCodeToWiX(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a$(b).txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := buildrecord.New(buildrecord.PathMSI, filepath.Join(dir, "setup.msis"), []buildrecord.BindPath{{Name: "wxs", Dir: dir}})
	vars := variables.Dictionary{"UPGRADE_CODE": "{9E4D3A86-5F7C-4B03-9D29-4A8C6E0F3B86}", "PRODUCT_VERSION": "1.0.0"}
	for wxs, mustSay := range map[string]string{
		`<Wix><?include extra.wxi?><Package/></Wix>`:                             "preprocessor (<?include",
		`<Wix><?define FLAVOR="pro"?><Package/></Wix>`:                           "preprocessor (<?define",
		`<Wix><Package><Property Id="F" Value="$(env.FLAVOR)"/></Package></Wix>`: "preprocessor variable",
		// WiX preprocesses the decoded document: a character reference is the same variable.
		`<Wix><Package><Property Id="F" Value="&#36;(env.FLAVOR)"/></Package></Wix>`: "preprocessor variable",
		`<Wix><Package><Condition>&#x24;(var.X)</Condition></Package></Wix>`:         "preprocessor variable",
	} {
		if code, why := productCode(wxs, vars, rec, dir, nil); code != "" || !strings.Contains(why, mustSay) {
			t.Errorf("%s: got %q, %q; want no code, saying %q", wxs, code, why, mustSay)
		}
	}
	escaped := `<?xml version="1.0"?><Wix><Package><File Source="a$$(b).txt"/></Package></Wix>`
	if code, why := productCode(escaped, vars, rec, dir, nil); code == "" {
		t.Errorf("an escaped dollar was taken for the preprocessor, or its file was not found: %s", why)
	}
}

// #66's review: when no code is derived the placeholder attribute is removed however the
// template spells it, and a placeholder left anywhere else stops the build.
func TestTheFallbackRemovesThePlaceholderInAnySpelling(t *testing.T) {
	dir := t.TempDir()
	rec := buildrecord.New(buildrecord.PathMSI, filepath.Join(dir, "setup.msis"), []buildrecord.BindPath{{Name: "wxs", Dir: dir}})
	vars := variables.Dictionary{"UPGRADE_CODE": "{9E4D3A86-5F7C-4B03-9D29-4A8C6E0F3B86}", "PRODUCT_VERSION": "1.0.0"}
	for _, attr := range []string{
		` ProductCode="` + productCodePlaceholder + `"`,
		` ProductCode='` + productCodePlaceholder + `'`,
		"\n    ProductCode = \"" + productCodePlaceholder + "\"",
	} {
		wxs := `<Wix><Package Name="P"` + attr + `><File Source="missing.txt"/></Package></Wix>`
		out, said, err := applyProductCode(wxs, vars, rec, dir, dir)
		if err != nil || strings.Contains(out, productCodePlaceholder) || strings.Contains(out, "ProductCode") ||
			!strings.HasPrefix(said, "ProductCode: generated by WiX") {
			t.Errorf("%q: out %q, said %q, err %v", attr, out, said, err)
		}
	}
	elsewhere := `<Wix><Package Name="P" ProductCode="` + productCodePlaceholder + `"><Property Id="X" Value="` +
		productCodePlaceholder + `"/><File Source="missing.txt"/></Package></Wix>`
	if _, _, err := applyProductCode(elsewhere, vars, rec, dir, dir); err == nil || !strings.Contains(err.Error(), "outside <Package") {
		t.Errorf("a placeholder outside <Package>: got %v", err)
	}
}
