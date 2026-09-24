//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/msiread"
)

const declaredScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="Declared"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{7C2B1E64-3D5A-4F81-9B07-2E6A4C8D1F64}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <feature name="Main">
    <files source="notes.txt" target="[INSTALLDIR]"/>
    <files source="version.dll" target="[INSTALLDIR]"/>
  </feature>
  {{COMPONENTS}}
</setup>`

// declaredFixture lays out a package with a text file and a real versioned DLL - System32's
// version.dll, whose version resource WiX records in the File table.
func declaredFixture(t *testing.T, components string) (dir, script string) {
	t.Helper()
	requireWix(t)
	dir = t.TempDir()
	write(t, filepath.Join(dir, "notes.txt"), "third-party notes\n")
	data, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "version.dll"))
	if err != nil {
		t.Skip("no System32\\version.dll to use as a versioned payload")
	}
	write(t, filepath.Join(dir, "version.dll"), string(data))
	return dir, scriptFor(t, dir, "declared.msi", strings.Replace(declaredScript, "{{COMPONENTS}}", components, 1))
}

// #64 end to end, with the real wix: a <component> declaration reaches the installer's SBOM as a
// supplied component carrying exactly the declared facts, marked as coming from this script.
func TestADeclaredComponentReachesTheSBOM(t *testing.T) {
	dir, script := declaredFixture(t, `<component for="[INSTALLDIR]notes.txt" name="libnotes" version="2.3.1"
    creator="notes@foo.example" license="Apache-2.0 OR MIT" purl="pkg:generic/libnotes@2.3.1"/>`)
	buildWithSBOM(t, script, &cliArgs{})

	_, raw := readDoc(t, filepath.Join(dir, "declared.msi"))
	var doc struct {
		Components []struct {
			Name     string `json:"name"`
			Version  string `json:"version"`
			PURL     string `json:"purl"`
			Licenses []struct {
				Expression      string `json:"expression"`
				Acknowledgement string `json:"acknowledgement"`
			} `json:"licenses"`
			Manufacturer struct {
				Contact []struct {
					Email string `json:"email"`
				} `json:"contact"`
			} `json:"manufacturer"`
			Properties []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"properties"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range doc.Components {
		if c.Name != "libnotes" {
			continue
		}
		found = true
		if c.Version != "2.3.1" || c.PURL != "pkg:generic/libnotes@2.3.1" {
			t.Errorf("version/purl %q/%q", c.Version, c.PURL)
		}
		if len(c.Licenses) != 1 || c.Licenses[0].Expression != "Apache-2.0 OR MIT" || c.Licenses[0].Acknowledgement != "declared" {
			t.Errorf("licences %+v, want the declared expression", c.Licenses)
		}
		if len(c.Manufacturer.Contact) != 1 || c.Manufacturer.Contact[0].Email != "notes@foo.example" {
			t.Errorf("creator %+v, want the declared email", c.Manufacturer)
		}
		var from string
		for _, p := range c.Properties {
			if p.Name == "msis:supplied.from" {
				from = p.Value
			}
		}
		if from != `setup.msis <component for="[INSTALLDIR]notes.txt">` {
			t.Errorf("msis:supplied.from = %q, want it to name the script's <component>", from)
		}
	}
	if !found {
		t.Fatal("the declared component is not in the document")
	}
}

// The product owner's decision: a declared version that contradicts the version resource the
// package records stops the build, naming both; the recorded version itself is accepted.
func TestADeclaredVersionContradictingTheFileStopsTheBuild(t *testing.T) {
	dir, script := declaredFixture(t, `<component for="[INSTALLDIR]version.dll" version="0.0.1"/>`)
	err := processFile(script, &cliArgs{build: true, sbom: true, setOverrides: map[string]string{},
		templateFolder: repoTemplates(t)})
	if err == nil || !strings.Contains(err.Error(), "declares version 0.0.1") {
		t.Fatalf("want a refusal naming the declared version, got %v", err)
	}

	// The version the package really records, read from the MSI the refused run built.
	pkg, rerr := msiread.Read(filepath.Join(dir, "declared.msi"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	var recorded string
	for _, f := range pkg.Files {
		if strings.EqualFold(f.Name, "version.dll") {
			recorded = f.Version
		}
	}
	if recorded == "" {
		t.Fatal("WiX recorded no version for version.dll, so the conflict check was never exercised")
	}
	if !strings.Contains(err.Error(), "records version "+recorded) {
		t.Errorf("the refusal does not name the recorded version %s: %v", recorded, err)
	}

	dir2, script2 := declaredFixture(t, `<component for="[INSTALLDIR]version.dll" version="`+recorded+`"/>`)
	buildWithSBOM(t, script2, &cliArgs{})
	if _, err := os.Stat(filepath.Join(dir2, "declared.msi.cdx.json")); err != nil {
		t.Errorf("a declaration matching the recorded version did not produce a document: %v", err)
	}
}

// A bundle installs no files of its own, so a declaration there has nothing to describe.
func TestADeclarationInABundleScriptIsRefused(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "bundle.msis")
	write(t, script, `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="B"/><set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="m"/><set name="UPGRADE_CODE" value="{8D3C2F75-4E6B-4A92-8C18-3F7B5D9E2A75}"/>
  <component for="[INSTALLDIR]app.exe" license="MIT"/>
  <bundle><msi source="app.msi"/></bundle>
</setup>`)
	err := processFile(script, &cliArgs{setOverrides: map[string]string{}, templateFolder: repoTemplates(t)})
	if err == nil || !strings.Contains(err.Error(), "builds a bundle") {
		t.Fatalf("want a refusal, got %v", err)
	}
}

// #64's review: the refusal does not depend on /SBOM. /BUILD alone must not ship an installer
// whose script contradicts it.
func TestAContradictionStopsABuildWithoutSBOM(t *testing.T) {
	dir, script := declaredFixture(t, `<component for="[INSTALLDIR]version.dll" version="0.0.1"/>`)
	err := processFile(script, &cliArgs{build: true, setOverrides: map[string]string{},
		templateFolder: repoTemplates(t)})
	if err == nil || !strings.Contains(err.Error(), "declares version 0.0.1") {
		t.Fatalf("want a refusal without /SBOM, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "declared.msi.cdx.json")); err == nil {
		t.Error("a document was written although /SBOM was not asked for")
	}
}

// #64's review: two descriptions of one file are refused on every run - with neither /SBOM nor
// /BUILD, when only the WXS is generated - and whichever way the file was described.
func TestADoubleDeclarationStopsEveryRun(t *testing.T) {
	for name, components := range map[string]string{
		"two <component>s": `<component for="[INSTALLDIR]notes.txt" license="MIT"/>
  <component for="[INSTALLDIR]notes.txt" version="1.0"/>`,
		"a <component> and an <sbom>": `<component for="[INSTALLDIR]notes.txt" license="MIT"/>
  <sbom source="notes.cdx.json" for="[INSTALLDIR]notes.txt"/>`,
	} {
		dir, script := declaredFixture(t, components)
		write(t, filepath.Join(dir, "notes.cdx.json"), `{"bomFormat":"CycloneDX","specVersion":"1.6","version":1,`+
			`"metadata":{"component":{"type":"library","bom-ref":"n","name":"notes"}}}`)
		err := processFile(script, &cliArgs{setOverrides: map[string]string{}, templateFolder: repoTemplates(t)})
		if err == nil || !strings.Contains(err.Error(), "both ") {
			t.Errorf("%s: want a refusal on a generate-only run, got %v", name, err)
		}
	}
}
