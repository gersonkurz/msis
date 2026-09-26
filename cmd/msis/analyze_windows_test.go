//go:build windows

package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/analyze"
	"github.com/gersonkurz/msis/internal/sbom"
	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

// writeJar writes a jar holding the given entries.
func writeJar(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, content := range entries {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// #82 (D25) end to end, with the real wix and the real syft: /SBOM /ANALYZE on a built package
// imports the Python distribution and the jar that declare themselves, attached to the files
// their declarations are in, and leaves out the jar syft could only name from its file. The
// document passes the conformance rules, which check that each analyzed identity rests on a
// declaration and that nothing was invented or dropped.
func TestAnalyzeImportsDeclaredPackages(t *testing.T) {
	requireWix(t)
	if _, err := exec.LookPath(analyze.Analyzer); err != nil {
		t.Skip("syft is not on PATH; /ANALYZE needs it")
	}
	dir := t.TempDir()
	msi := buildAnalyzeFixture(t, dir, &cliArgs{build: true, templateFolder: repoTemplates(t), setOverrides: map[string]string{}})
	if err := runSBOM(msi, true); err != nil {
		t.Fatal(err)
	}
	data := checkAnalyzedDocument(t, msi)

	// Deterministic (D6): a second run differs only in the serial number and the timestamp.
	if err := runSBOM(msi, true); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(sbom.SidecarPath(msi))
	if err != nil {
		t.Fatal(err)
	}
	if canonical(t, data) != canonical(t, again) {
		t.Error("two /SBOM /ANALYZE runs over one package produced different documents")
	}
}

// The same through /BUILD /SBOM /ANALYZE, where the build writes the document (emitBuildSBOM).
func TestBuildSBOMAnalyzeImportsDeclaredPackages(t *testing.T) {
	requireWix(t)
	if _, err := exec.LookPath(analyze.Analyzer); err != nil {
		t.Skip("syft is not on PATH; /ANALYZE needs it")
	}
	dir := t.TempDir()
	msi := buildAnalyzeFixture(t, dir, &cliArgs{build: true, sbom: true, analyze: true,
		templateFolder: repoTemplates(t), setOverrides: map[string]string{}})
	checkAnalyzedDocument(t, msi)
}

// buildAnalyzeFixture builds a package holding a Python distribution and a jar that declare
// themselves, and a jar syft can only name from its file.
func buildAnalyzeFixture(t *testing.T, dir string, args *cliArgs) string {
	t.Helper()
	write(t, filepath.Join(dir, "payload", "lib", "demo-1.2.3.dist-info", "METADATA"),
		"Metadata-Version: 2.1\nName: demo\nVersion: 1.2.3\nSummary: a test distribution\n")
	write(t, filepath.Join(dir, "payload", "lib", "demo-1.2.3.dist-info", "RECORD"),
		"demo/__init__.py,,\ndemo-1.2.3.dist-info/METADATA,,\n")
	write(t, filepath.Join(dir, "payload", "lib", "demo", "__init__.py"), "")
	writeJar(t, filepath.Join(dir, "payload", "java", "demo-lib-4.5.6.jar"), map[string]string{
		"META-INF/MANIFEST.MF":                               "Manifest-Version: 1.0\r\nImplementation-Title: demo-lib\r\nImplementation-Version: 4.5.6\r\n",
		"META-INF/maven/org.example/demo-lib/pom.properties": "groupId=org.example\nartifactId=demo-lib\nversion=4.5.6\n",
		"org/example/Demo.class":                             "not a class",
	})
	// A .NET application's .deps.json: one NuGet package (kept) and the application's own project
	// (skipped - it is not a package). syft wants each package's DLL present; these are
	// placeholders, so its PE cataloger finds nothing in them.
	write(t, filepath.Join(dir, "payload", "app", "App.deps.json"), depsJSON)
	write(t, filepath.Join(dir, "payload", "app", "App.dll"), "not a dll")
	// A real PE: syft identifies it from its version resource, which is not a declaration.
	where, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "where.exe"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "payload", "bin", "where.exe"), string(where))
	write(t, filepath.Join(dir, "payload", "app", "Newtonsoft.Json.dll"), "not a dll")
	writeJar(t, filepath.Join(dir, "payload", "java", "nameless-1.0.jar"), map[string]string{
		"META-INF/MANIFEST.MF":    "Manifest-Version: 1.0\r\n",
		"org/example/Other.class": "not a class",
	})
	script := scriptFor(t, dir, "analyze.msi", `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="Analyze82"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{4E9C2A71-6B3D-4F80-9A15-2C7D8E1B3F64}"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <feature name="Main"><files source="payload" target="[INSTALLDIR]"/></feature>
</setup>`)
	if err := processFile(script, args); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "analyze.msi")
}

// checkAnalyzedDocument checks the sidecar of msi: the two declared packages, attached to their
// files, nothing else identified, and the conformance rules.
func checkAnalyzedDocument(t *testing.T, msi string) []byte {
	t.Helper()
	data, err := os.ReadFile(sbom.SidecarPath(msi))
	if err != nil {
		t.Fatal(err)
	}
	var doc sbom.Document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	evidence := map[string]string{} // purl -> the file whose dependsOn names it
	names := map[string]string{}
	for _, c := range doc.Components {
		names[c.BOMRef] = c.Name
	}
	purlOf := map[string]string{}
	for _, c := range doc.Components {
		if c.PURL != "" {
			purlOf[c.BOMRef] = c.PURL
		}
	}
	for _, d := range doc.Dependencies {
		for _, on := range d.DependsOn {
			if p := purlOf[on]; p != "" {
				evidence[p] = names[d.Ref]
			}
		}
	}
	for purl, file := range map[string]string{
		"pkg:pypi/demo@1.2.3":                  "METADATA",
		"pkg:maven/org.example/demo-lib@4.5.6": "demo-lib-4.5.6.jar",
		"pkg:nuget/Newtonsoft.Json@13.0.3":     "App.deps.json",
	} {
		if evidence[purl] != file {
			t.Errorf("%s is attached to %q, want %q (all: %v)", purl, evidence[purl], file, evidence)
		}
	}
	if len(purlOf) != 3 {
		t.Errorf("analyzed identities %v, want exactly the three declared ones", purlOf)
	}
	for _, p := range conformance.Check(data, conformance.Expected{
		IdentifiedComponents:   []string{doc.Metadata.Component.BOMRef},
		AnalyzedComponentNames: []string{"demo", "demo-lib", "Newtonsoft.Json"},
	}) {
		t.Errorf("conformance: %v", p)
	}
	// The PE's identification is counted, not imported.
	if run := propertyOf(doc.Metadata.Properties, "msis:analyzer.run"); !strings.Contains(run, "from PE version resources") {
		t.Errorf("the PE identification is not reported as left out: %q", run)
	}
	// The tool's digest is the program's, not a launcher's: for a Scoop shim, the program the
	// .shim names.
	var tool *sbom.Component
	for i := range doc.Metadata.Tools.Components {
		if doc.Metadata.Tools.Components[i].Name == "syft" {
			tool = &doc.Metadata.Tools.Components[i]
		}
	}
	if tool == nil {
		t.Fatal("syft is not in metadata.tools")
	}
	exe, _ := exec.LookPath(analyze.Analyzer)
	if root := os.Getenv("ChocolateyInstall"); root != "" && strings.EqualFold(filepath.Dir(exe), filepath.Join(root, "bin")) {
		// A Chocolatey shim names no program msis can read, so no digest is recorded (D25).
		if len(tool.Hashes) != 0 {
			t.Errorf("syft behind a Chocolatey shim got a digest %v; it can only be the launcher's", tool.Hashes)
		}
		return data
	}
	program := exe
	if shim, err := os.ReadFile(strings.TrimSuffix(exe, filepath.Ext(exe)) + ".shim"); err == nil {
		_, after, _ := strings.Cut(string(shim), "path = ")
		program = strings.Trim(strings.TrimSpace(strings.SplitN(after, "\n", 2)[0]), `"`)
	}
	if want := sha256Of(t, program); len(tool.Hashes) != 1 || tool.Hashes[0].Content != want {
		t.Errorf("syft's digest %v is not that of %s (%s)", tool.Hashes, program, want)
	}
	return data
}

// canonical is a document without the two fields that legitimately vary between runs.
func canonical(t *testing.T, data []byte) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "serialNumber")
	if meta, ok := m["metadata"].(map[string]any); ok {
		delete(meta, "timestamp")
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// /ANALYZE adds to an SBOM; without /SBOM there is none to add to.
func TestAnalyzeNeedsSBOM(t *testing.T) {
	if err := scanArgsValid(&cliArgs{analyze: true, files: []string{"x.msi"}}); err == nil ||
		!strings.Contains(err.Error(), "/SBOM") {
		t.Errorf("/ANALYZE without /SBOM: %v", err)
	}
}

// depsJSON is a .NET 8 application's .deps.json, as dotnet publish writes one.
const depsJSON = `{
  "runtimeTarget": {"name": ".NETCoreApp,Version=v8.0", "signature": ""},
  "compilationOptions": {},
  "targets": {
    ".NETCoreApp,Version=v8.0": {
      "App/1.0.0": {"dependencies": {"Newtonsoft.Json": "13.0.3"}, "runtime": {"App.dll": {}}},
      "Newtonsoft.Json/13.0.3": {"runtime": {"lib/net6.0/Newtonsoft.Json.dll": {"assemblyVersion": "13.0.0.0", "fileVersion": "13.0.3.27908"}}}
    }
  },
  "libraries": {
    "App/1.0.0": {"type": "project", "serviceable": false, "sha512": ""},
    "Newtonsoft.Json/13.0.3": {"type": "package", "serviceable": true, "sha512": "sha512-HrC5BXdl00IP9zeV+0Z848QWPAoCr9P3bDEZguI+gkLcBKAOxix/tLEAAHC+UvDNPv4a2d18lOReHMOagPa+zQ==", "path": "newtonsoft.json/13.0.3", "hashPath": "newtonsoft.json.13.0.3.nupkg.sha512"}
  }
}`

func propertyOf(props []sbom.Property, name string) string {
	for _, p := range props {
		if p.Name == name {
			return p.Value
		}
	}
	return ""
}

func sha256Of(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
