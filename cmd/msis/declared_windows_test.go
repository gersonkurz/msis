//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// #64 end to end, with the real wix: a <component> declaration's facts land on the file's OWN
// component (D16) - where BSI TR-03183-2 looks - with each field's provenance stated.
func TestADeclaredComponentReachesTheSBOM(t *testing.T) {
	dir, script := declaredFixture(t, `<component for="[INSTALLDIR]notes.txt" name="libnotes" version="2.3.1"
    creator="notes@foo.example" license="Apache-2.0 OR MIT" purl="pkg:generic/libnotes@2.3.1"
    source="https://github.com/foo/libnotes/tree/v2.3.1"/>`)
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
			ExternalReferences []struct {
				Type string `json:"type"`
				URL  string `json:"url"`
			} `json:"externalReferences"`
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
	found := 0
	for _, c := range doc.Components {
		prop := map[string]string{}
		for _, p := range c.Properties {
			prop[p.Name] = p.Value
		}
		if prop["bsi:component:filename"] != "notes.txt" {
			continue
		}
		found++
		// The file component itself: its name is the declared one, its filename its own.
		if c.Name != "libnotes" || c.Version != "2.3.1" || c.PURL != "pkg:generic/libnotes@2.3.1" {
			t.Errorf("name/version/purl %q/%q/%q", c.Name, c.Version, c.PURL)
		}
		if len(c.Licenses) != 1 || c.Licenses[0].Expression != "Apache-2.0 OR MIT" || c.Licenses[0].Acknowledgement != "concluded" {
			t.Errorf("licences %+v, want the declared expression as the distribution licence", c.Licenses)
		}
		if len(c.Manufacturer.Contact) != 1 || c.Manufacturer.Contact[0].Email != "notes@foo.example" {
			t.Errorf("creator %+v, want the declared email", c.Manufacturer)
		}
		if prop["msis:declared.by"] != `setup.msis <component for="[INSTALLDIR]notes.txt">` {
			t.Errorf("msis:declared.by = %q, want it to name the script's <component>", prop["msis:declared.by"])
		}
		if len(c.ExternalReferences) != 1 || c.ExternalReferences[0].Type != "source-distribution" ||
			c.ExternalReferences[0].URL != "https://github.com/foo/libnotes/tree/v2.3.1" {
			t.Errorf("source %+v, want the declared source code URI (#68)", c.ExternalReferences)
		}
		if prop["msis:declared.fields"] != "creator,license,name,purl,source,version" {
			t.Errorf("msis:declared.fields = %q", prop["msis:declared.fields"])
		}
		if prop["msis:installTarget"] == "" || prop["msis:msi.fileKey"] == "" {
			t.Error("the declaration replaced the file component instead of adding to it")
		}
	}
	if found != 1 {
		t.Fatalf("%d components for notes.txt, want exactly its own", found)
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

// #63 end to end: BSI's version fallback is a build fact. Under /BUILD /SBOM a payload without a
// version of its own takes its source file's modification date; the versioned DLL keeps the
// version the package records; and /SBOM on the same MSI afterwards - with no build, so no
// source - leaves the text file without one.
func TestTheVersionFallbackIsTheSourcesModificationDate(t *testing.T) {
	dir, script := declaredFixture(t, "")
	when := time.Date(2025, 1, 15, 8, 30, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(dir, "notes.txt"), when, when); err != nil {
		t.Fatal(err)
	}
	buildWithSBOM(t, script, &cliArgs{})
	msi := filepath.Join(dir, "declared.msi")

	versions := func() map[string]string {
		doc, _ := readDoc(t, msi)
		out := map[string]string{}
		for _, c := range doc.Components {
			out[c.Name] = c.Version
		}
		return out
	}
	built := versions()
	if built["notes.txt"] != "2025-01-15T08:30:00Z" {
		t.Errorf("notes.txt version %q, want its source's modification date", built["notes.txt"])
	}
	pkg, err := msiread.Read(msi)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range pkg.Files {
		if strings.EqualFold(f.Name, "version.dll") && (f.Version == "" || built["version.dll"] != f.Version) {
			t.Errorf("version.dll version %q, want the recorded %q", built["version.dll"], f.Version)
		}
	}

	if err := runSBOM(msi, false); err != nil {
		t.Fatal(err)
	}
	if v := versions()["notes.txt"]; v != "" {
		t.Errorf("an artifact read without its build gave notes.txt version %q", v)
	}
}
