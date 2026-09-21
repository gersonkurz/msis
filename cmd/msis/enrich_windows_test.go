//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/msiread"
	"github.com/gersonkurz/msis/internal/prereqcache"
	"github.com/gersonkurz/msis/internal/sbom"
	"github.com/gersonkurz/msis/internal/sbom/conformance"
	"github.com/gersonkurz/msis/internal/wix"
)

// #34 across all four build paths. Each is a real build - `wix` runs - because the thing under
// test is what the build resolved, and a build that did not happen resolved nothing.

func requireWix(t *testing.T) {
	t.Helper()
	if !wix.IsWixAvailable() {
		t.Skip("wix is not available; these build real packages")
	}
}

func repoTemplates(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "templates"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// scriptFor writes a .msis with an ABSOLUTE BUILD_TARGET. A relative one resolves against the
// process working directory rather than the script, which would drop the artifacts in the
// package directory during a test run.
func scriptFor(t *testing.T, dir, name, body string) string {
	t.Helper()
	target := filepath.Join(dir, name)
	script := strings.ReplaceAll(body, "{{TARGET}}", strings.ReplaceAll(target, `\`, `\\`))
	path := filepath.Join(dir, "setup.msis")
	write(t, path, script)
	return path
}

func buildWithSBOM(t *testing.T, script string, args *cliArgs) {
	t.Helper()
	args.build = true
	args.sbom = true
	if args.setOverrides == nil {
		args.setOverrides = map[string]string{}
	}
	if args.templateFolder == "" {
		args.templateFolder = repoTemplates(t)
	}
	if err := processFile(script, args); err != nil {
		t.Fatalf("building %s: %v", filepath.Base(script), err)
	}
}

// readDoc loads the document msis wrote beside an artifact.
func readDoc(t *testing.T, artifact string) (*sbom.Document, []byte) {
	t.Helper()
	data, err := os.ReadFile(sbom.SidecarPath(artifact))
	if err != nil {
		t.Fatalf("no document beside %s: %v", filepath.Base(artifact), err)
	}
	var doc sbom.Document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return &doc, data
}

func metaProp(doc *sbom.Document, name string) string {
	for _, p := range doc.Metadata.Properties {
		if p.Name == name {
			return p.Value
		}
	}
	return ""
}

func compProp(c sbom.Component, name string) string {
	for _, p := range c.Properties {
		if p.Name == name {
			return p.Value
		}
	}
	return ""
}

const payloadScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="EnrichMe"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{4C7E1A92-8D3F-4B60-9E25-1A7C3F0D5B84}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <feature name="Main">
    <files source="app.txt" target="[INSTALLDIR]"/>
    <files source="lib" target="[INSTALLDIR]lib"/>
  </feature>
</setup>`

func payloadDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "app.txt"), "the application\n")
	write(t, filepath.Join(dir, "lib", "one.txt"), "library one\n")
	write(t, filepath.Join(dir, "lib", "deep", "two.txt"), "library two, nested\n")
	return dir
}

// --- path 1: a plain MSI -------------------------------------------------------------------

// The MSI path knows where every payload file came from. The artifact does not, and cannot:
// it holds the bytes and the install target, and nothing about the script that produced them.
func TestMSIPathRecordsWhereEachPayloadCameFrom(t *testing.T) {
	requireWix(t)
	dir := payloadDir(t)
	script := scriptFor(t, dir, "app.msi", payloadScript)
	buildWithSBOM(t, script, &cliArgs{})

	doc, _ := readDoc(t, filepath.Join(dir, "app.msi"))

	if got := metaProp(doc, "msis:build.path"); got != "msi" {
		t.Errorf("build path = %q, want msi", got)
	}
	if got := metaProp(doc, "msis:build.script"); got != "setup.msis" {
		t.Errorf("build script = %q, want the .msis by name", got)
	}
	if metaProp(doc, "msis:build.tool.msis") == "" {
		t.Error("the document does not say which msis built the artifact")
	}
	if metaProp(doc, "msis:build.tool.wix") == "" {
		t.Error("the document does not say which WiX built the artifact")
	}

	// Every payload file carries the source it was built from, at the path the script
	// spelled - relative to the .msis, and slash-separated whatever the platform.
	want := map[string]string{
		"app.txt": "app.txt",
		"one.txt": "lib/one.txt",
		"two.txt": "lib/deep/two.txt",
	}
	seen := map[string]string{}
	for _, c := range doc.Components {
		if compProp(c, "msis:role") != "payload" {
			continue
		}
		seen[c.Name] = compProp(c, "msis:build.source")
	}
	for name, source := range want {
		if seen[name] != source {
			t.Errorf("%s: build source = %q, want %q (all: %v)", name, seen[name], source, seen)
		}
	}
}

// Nothing absolute leaves the build. A machine path in a published document leaks the build
// machine's layout and tells the reader nothing they can act on.
func TestNoAbsolutePathsReachTheDocument(t *testing.T) {
	requireWix(t)
	dir := payloadDir(t)
	script := scriptFor(t, dir, "app.msi", payloadScript)
	buildWithSBOM(t, script, &cliArgs{})

	_, raw := readDoc(t, filepath.Join(dir, "app.msi"))
	text := string(raw)

	// The temp directory, the user profile and the template folder are all absolute paths
	// this build genuinely touched, so finding any of them is a real leak.
	for _, leak := range []string{
		strings.ReplaceAll(dir, `\`, `\\`),
		strings.ReplaceAll(repoTemplates(t), `\`, `\\`),
		strings.ReplaceAll(os.Getenv("LOCALAPPDATA"), `\`, `\\`),
	} {
		if leak == "" {
			continue
		}
		if strings.Contains(text, leak) {
			t.Errorf("the document contains the absolute path %q", leak)
		}
	}
	// ...and a drive letter anywhere is the general form of the same mistake.
	if strings.Contains(text, `C:\\`) || strings.Contains(text, "C:/") {
		t.Error("the document contains an absolute Windows path")
	}
}

// The acceptance criterion: enrichment ADDS facts. An enriched document and an artifact-only
// document of the same package describe the same bytes, so every payload hash agrees - which
// is what makes the two interchangeable as evidence.
func TestEnrichedAndArtifactOnlyDocumentsAgreeOnEveryHash(t *testing.T) {
	requireWix(t)
	dir := payloadDir(t)
	script := scriptFor(t, dir, "app.msi", payloadScript)
	buildWithSBOM(t, script, &cliArgs{})

	artifact := filepath.Join(dir, "app.msi")
	enriched, _ := readDoc(t, artifact)

	// The artifact-only document, from the same file, with no build record at all.
	pkg, err := msiread.Read(artifact)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := sbom.FromPackage(pkg, sbom.Options{MsisVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}

	hashes := func(d *sbom.Document) map[string]string {
		out := map[string]string{}
		for _, c := range d.Components {
			for _, h := range c.Hashes {
				if strings.EqualFold(h.Alg, "SHA-256") {
					out[c.BOMRef] = strings.ToLower(h.Content)
				}
			}
		}
		return out
	}
	a, b := hashes(enriched), hashes(plain)

	for ref, want := range b {
		got, ok := a[ref]
		if !ok {
			t.Errorf("%s is in the artifact-only document but not the enriched one", ref)
			continue
		}
		if got != want {
			t.Errorf("%s: enriched %s, artifact-only %s - enrichment changed a hash", ref, got, want)
		}
	}
	if len(b) == 0 {
		t.Fatal("the artifact-only document has no hashes, so this compares nothing")
	}

	// The enriched one has MORE components (the build contributes some), never fewer, and
	// the subject digest is identical.
	if len(a) < len(b) {
		t.Errorf("the enriched document has %d hashed components, the artifact-only one %d",
			len(a), len(b))
	}
	if enriched.Metadata.Component.Hashes[0].Content != plain.Metadata.Component.Hashes[0].Content {
		t.Error("the two documents disagree about the subject artifact's own digest")
	}
}

// Both documents conform. D is not a special case of the profile.
func TestAnEnrichedDocumentConforms(t *testing.T) {
	requireWix(t)
	dir := payloadDir(t)
	script := scriptFor(t, dir, "app.msi", payloadScript)
	buildWithSBOM(t, script, &cliArgs{})

	doc, raw := readDoc(t, filepath.Join(dir, "app.msi"))

	// Only the product's identity was read rather than guessed, as in A2; enrichment adds
	// provenance, not identity, so it may not add a purl to anything.
	want := conformance.Expected{
		IdentifiedComponents: []string{doc.Metadata.Component.BOMRef},
	}
	for _, p := range conformance.Check(raw, want) {
		t.Errorf("conformance: %v", p)
	}
}

// --- path 2: /STANDALONE -------------------------------------------------------------------

const standaloneScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="StandaloneMe"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{2F8B6D41-5C09-4E73-A1B8-7D4E2C905F36}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <requires type="vcredist" version="2022"/>
  <feature name="Main">
    <files source="app.txt" target="[INSTALLDIR]"/>
  </feature>
</setup>`

// /STANDALONE resolves no chain at all: the prerequisites become launch conditions, so what
// the build arranged is DETECTION, not distribution. Listing them like bundled payload would
// claim the installer ships something it does not - which is the distinction #34 asks for.
func TestStandalonePrerequisitesAreExternallyRequiredRuntimes(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "app.txt"), "the application\n")
	script := scriptFor(t, dir, "app.msi", standaloneScript)
	buildWithSBOM(t, script, &cliArgs{standalone: true})

	doc, _ := readDoc(t, filepath.Join(dir, "app.msi"))

	if got := metaProp(doc, "msis:build.path"); got != "standalone" {
		t.Errorf("build path = %q, want standalone", got)
	}

	var runtime *sbom.Component
	for i := range doc.Components {
		if compProp(doc.Components[i], "msis:role") == "required-runtime" {
			runtime = &doc.Components[i]
		}
	}
	if runtime == nil {
		t.Fatal("the /STANDALONE build's prerequisite is not in the document at all")
	}
	if runtime.Name != "vcredist" {
		t.Errorf("name = %q, want vcredist", runtime.Name)
	}
	// Not carried, no digest, and it says why - because nothing was distributed.
	if got := compProp(*runtime, "msis:payload.carried"); got != "false" {
		t.Errorf("carried = %q, want false: /STANDALONE ships nothing", got)
	}
	if len(runtime.Hashes) != 0 {
		t.Errorf("a runtime nobody shipped has a digest: %+v", runtime.Hashes)
	}
	if compProp(*runtime, "msis:payload.unavailable") == "" {
		t.Error("a component with no digest must say why")
	}
	// The launch condition is the evidence that detection is what was arranged.
	if compProp(*runtime, "msis:launchCondition") == "" {
		t.Error("the launch condition the build generated is not recorded")
	}

	// And it must NOT be described as a prerequisite the installer carries.
	for _, c := range doc.Components {
		if compProp(c, "msis:role") == "prerequisite" {
			t.Errorf("a /STANDALONE build produced a carried prerequisite: %s", c.Name)
		}
	}
}

// --- path 4: a /CUSTOMTEMPLATES overlay ------------------------------------------------------

// The hook DLL that executes during installation is resolved through WiX's ORDERED bind paths,
// and a /CUSTOMTEMPLATES overlay comes before the template folder. On a machine carrying both,
// naming the obvious copy records a digest for bytes that never ran - which is exactly what was
// true on the machine this was developed on, where three different copies existed.
//
// The check is a digest match against the artifact's own Binary stream: the template declares
// the DLL as Binary id "binary.dll" whatever the file is called, so a name join would prove
// nothing.
func TestTheOverlayCopyOfTheHookDLLIsTheOneRecorded(t *testing.T) {
	requireWix(t)
	dir := payloadDir(t)

	// An overlay carrying a DIFFERENT x64/msi-simplica.dll from the template folder's. Its
	// contents are not a real DLL - nothing loads it here - but WiX packages whatever the
	// bind path resolves, which is the whole point.
	overlay := filepath.Join(dir, "overlay")
	write(t, filepath.Join(overlay, "x64", "msi-simplica.dll"),
		"this is the overlay's copy, and it is the one WiX must consume\n")

	script := scriptFor(t, dir, "app.msi", strings.ReplaceAll(payloadScript,
		`<set name="PLATFORM" value="x64"/>`,
		`<set name="PLATFORM" value="x64"/>
  <set name="USE_INSTALLER_HOOKS" value="True"/>
  <set name="DLL_ENTRY" value="msi-simplica.dll"/>`))
	buildWithSBOM(t, script, &cliArgs{customTemplates: overlay})

	artifact := filepath.Join(dir, "app.msi")
	doc, _ := readDoc(t, artifact)

	// Find the Binary stream the build attributed to a source.
	var attributed *sbom.Component
	for i := range doc.Components {
		c := doc.Components[i]
		if compProp(c, "msis:role") == "binary-stream" && compProp(c, "msis:build.source") != "" {
			attributed = &doc.Components[i]
		}
	}
	if attributed == nil {
		t.Fatal("no Binary stream was attributed to a build source; the hook DLL was not recorded")
	}
	if got := compProp(*attributed, "msis:build.sourceRoot"); got != "custom-templates" {
		t.Errorf("source root = %q, want custom-templates - the overlay comes first in the "+
			"bind path order and is what WiX consumed", got)
	}
	if got := compProp(*attributed, "msis:build.source"); got != "x64/msi-simplica.dll" {
		t.Errorf("source = %q, want x64/msi-simplica.dll", got)
	}

	// The attribution is only trustworthy because it was matched on CONTENT. Prove the
	// bytes really are the overlay's, independently.
	overlayBytes, err := os.ReadFile(filepath.Join(overlay, "x64", "msi-simplica.dll"))
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := msiread.Read(artifact)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, b := range pkg.Binaries {
		if b.SHA256 == sha256Hex(overlayBytes) {
			found = true
		}
	}
	if !found {
		t.Error("the artifact does not contain the overlay's copy, so this test is not " +
			"measuring the bind path order at all")
	}
	if attributed.Hashes[0].Content != sha256Hex(overlayBytes) {
		t.Errorf("the attributed stream's digest is %s, the overlay's file is %s",
			attributed.Hashes[0].Content[:16], sha256Hex(overlayBytes)[:16])
	}
}

// --- path 3: an auto-bundle ------------------------------------------------------------------

const autoBundleScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="AutoBundled"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{6D3A9B57-2E41-4C88-B0F6-9A25E7D14C03}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <requires type="vcredist" version="2022" source="stub-vcredist.exe"/>
  <feature name="Main">
    <files source="app.txt" target="[INSTALLDIR]"/>
  </feature>
</setup>`

// The auto-bundle path resolves its prerequisites AFTER the MSI it wraps has been built, which
// is why the record is contributed to rather than derived: at generation time none of this
// exists yet.
//
// A custom source= is used so the build is hermetic - the point under test is what the record
// says about a prerequisite the installer CARRIES, not whether a download works.
func TestAutoBundleRecordsItsPrerequisitesAndLinksTheMSI(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "app.txt"), "the application\n")
	write(t, filepath.Join(dir, "stub-vcredist.exe"), "stands in for the VC++ redistributable\n")
	script := scriptFor(t, dir, "app.msi", autoBundleScript)
	buildWithSBOM(t, script, &cliArgs{})

	msi := filepath.Join(dir, "app.msi")
	exe := filepath.Join(dir, "app.exe")
	for _, p := range []string{msi, exe} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected artifact %s: %v", filepath.Base(p), err)
		}
	}

	// BOTH artifacts get a document. An auto-bundle produces two files, and describing only
	// one of them would leave the other undescribed in the release directory.
	msiDoc, _ := readDoc(t, msi)
	bundleDoc, _ := readDoc(t, exe)

	for _, d := range []*sbom.Document{msiDoc, bundleDoc} {
		if got := metaProp(d, "msis:build.path"); got != "auto-bundle" {
			t.Errorf("build path = %q, want auto-bundle", got)
		}
	}

	// The prerequisite's provenance is attached to the component the BUNDLE already has for
	// it, matched by digest - so the claim is checked against what WiX packaged rather than
	// asserted beside it.
	var prereq *sbom.Component
	for i := range bundleDoc.Components {
		if compProp(bundleDoc.Components[i], "msis:build.source") == "stub-vcredist.exe" {
			prereq = &bundleDoc.Components[i]
		}
	}
	if prereq == nil {
		t.Fatal("no component of the bundle was attributed to the prerequisite's source; " +
			"either it was not recorded, or its bytes do not match what the bundle carries")
	}
	if got := compProp(*prereq, "msis:prerequisite.type"); got != "vcredist" {
		t.Errorf("prerequisite type = %q", got)
	}

	// A prerequisite the SCRIPT supplied was not downloaded, so no download provenance is
	// claimed for it. Reporting a Microsoft URL for locally authored bytes would be an
	// invented provenance - the same class of mistake as an invented purl, and worse, because
	// a URL looks like evidence.
	if got := compProp(*prereq, "msis:prerequisite.cache"); got != "" {
		t.Errorf("a locally sourced prerequisite claims a cache entry: %q", got)
	}
	for _, r := range prereq.ExternalReferences {
		if r.Type == "distribution" {
			t.Errorf("a locally sourced prerequisite claims it was downloaded from %q", r.URL)
		}
	}

	// The MSI carries neither the prerequisite nor itself. Those bytes belong to the wrapper,
	// and handing one record to both documents made the MSI claim both.
	for _, c := range msiDoc.Components {
		if compProp(c, "msis:prerequisite.type") != "" {
			t.Errorf("the MSI's document claims a prerequisite: %s", c.BOMRef)
		}
		if compProp(c, "msis:build.source") == "stub-vcredist.exe" {
			t.Errorf("the MSI's document claims the wrapper's prerequisite: %s", c.BOMRef)
		}
	}
	// Nor may it MENTION them. Attaching provenance by digest already stops the MSI from
	// acquiring a component for the wrapper's prerequisite - nothing in it carries those
	// bytes - but an unscoped record would still leave the MSI's document saying it could not
	// account for a prerequisite that was never its to account for.
	for _, p := range msiDoc.Metadata.Properties {
		if p.Name != "msis:build.unresolved" {
			continue
		}
		for _, theirs := range []string{"vcredist", "stub-vcredist.exe", "chained"} {
			if strings.Contains(p.Value, theirs) {
				t.Errorf("the MSI's document discusses the wrapper's contents: %q", p.Value)
			}
		}
	}

	// The MSI's document is written first, so the bundle's can link to it - which is what
	// turns two documents into a linked pair rather than two unrelated files.
	var linked string
	for _, c := range bundleDoc.Components {
		if l, _ := sbom.BOMLink(c); l != "" {
			linked = l
		}
	}
	if linked == "" {
		t.Fatal("the bundle's document links to nothing; the MSI's document was not written " +
			"first, or its subject digest did not match")
	}
	serial, _, err := sbom.ParseBOMLink(linked)
	if err != nil {
		t.Fatal(err)
	}
	if serial != msiDoc.SerialNumber {
		t.Errorf("the bundle links to %s, but the MSI's document is %s", serial, msiDoc.SerialNumber)
	}
}

// --- path 4: an explicit <bundle> --------------------------------------------------------------

// An explicit <bundle> has no payload of its own: what it knows is which installers it was
// built FROM. The artifact holds the packaged bytes and nothing about where they came from, so
// this association exists only in the build.
func TestExplicitBundleRecordsTheSourcesItWasBuiltFrom(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()

	// A real MSI to chain, built first by the MSI path, and a stand-in prerequisite. An
	// explicit <bundle> that carries a prerequisite is the case that could not emit a document
	// at all while prerequisites were recorded without resolving them: the entry had no digest,
	// so the emitter refused to write anything after a build that had succeeded.
	inner := filepath.Dir(chainableMSI(t, dir))
	write(t, filepath.Join(dir, "stub-vcredist.exe"), "stands in for the VC++ redistributable\n")

	bundleScript := `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="ExplicitBundle"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{8E5F2C10-4A76-4D39-95B2-C08D1E6A7F42}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <bundle>
    <prerequisite type="vcredist" version="2022" source="stub-vcredist.exe"/>
    <msi source="inner\inner.msi"/>
  </bundle>
</setup>`
	script := scriptFor(t, dir, "suite.exe", bundleScript)
	buildWithSBOM(t, script, &cliArgs{})

	doc, _ := readDoc(t, filepath.Join(dir, "suite.exe"))

	if got := metaProp(doc, "msis:build.path"); got != "bundle" {
		t.Errorf("build path = %q, want bundle", got)
	}

	attributed := map[string]*sbom.Component{}
	for i := range doc.Components {
		if src := compProp(doc.Components[i], "msis:build.source"); src != "" {
			attributed[src] = &doc.Components[i]
		}
	}

	// The chained MSI, attributed to the file the build read - matched by digest against what
	// the bundle actually carries, not asserted from the record alone.
	chained := attributed["inner/inner.msi"]
	if chained == nil {
		t.Fatalf("the bundle's document does not say which installer it was built from: %v",
			keysOf(attributed))
	}
	data, err := os.ReadFile(filepath.Join(inner, "inner.msi"))
	if err != nil {
		t.Fatal(err)
	}
	if len(chained.Hashes) == 0 || chained.Hashes[0].Content != sha256Hex(data) {
		t.Errorf("the attributed component's digest is not that of the MSI on disk, so the "+
			"attribution was not verified against the artifact: %+v", chained.Hashes)
	}

	prereq := attributed["stub-vcredist.exe"]
	if prereq == nil {
		t.Fatalf("the bundle's prerequisite was not attributed: %v", keysOf(attributed))
	}
	if got := compProp(*prereq, "msis:prerequisite.type"); got != "vcredist" {
		t.Errorf("prerequisite type = %q", got)
	}
	for _, r := range prereq.ExternalReferences {
		if r.Type == "distribution" {
			t.Errorf("a locally sourced prerequisite claims it was downloaded from %q", r.URL)
		}
	}
}

func keysOf(m map[string]*sbom.Component) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

const competingPayloadScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="Competing"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{3D6A0F28-7B14-4E95-A2C3-58F19D4B06E7}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <feature name="Main">
    <files source="app.txt" target="[INSTALLDIR]"/>
  </feature>
</setup>`

const payloadScriptOneFile = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="Inner"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{1B4D7E39-6C25-4A80-8F17-3E9A5C204D68}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <feature name="Main">
    <files source="app.txt" target="[INSTALLDIR]"/>
  </feature>
</setup>`

// The two SBOM operations are chosen by one predicate, and getting it backwards would make
// `msis /BUILD /SBOM setup.msis` fall through to the artifact-only path - where a .msis is not
// a readable artifact, so it would fail with a message about the wrong thing.
func TestTheSBOMModeIsChosenCorrectly(t *testing.T) {
	cases := []struct {
		name         string
		args         cliArgs
		artifactOnly bool
	}{
		{"/SBOM on an artifact", cliArgs{sbom: true}, true},
		{"/BUILD /SBOM on a script", cliArgs{sbom: true, build: true}, false},
		{"/BUILD alone", cliArgs{build: true}, false},
		{"neither", cliArgs{}, false},
	}
	for _, tc := range cases {
		args := tc.args
		if got := artifactOnlySBOM(&args); got != tc.artifactOnly {
			t.Errorf("%s: artifactOnlySBOM = %v, want %v", tc.name, got, tc.artifactOnly)
		}
	}
}

// WiX resolves a relative payload through its bind paths IN ORDER, and the generated WXS
// directory comes before the script's. When BUILD_TARGET puts the output somewhere else and
// that somewhere else happens to hold a file of the same relative name, WiX packages THAT copy.
//
// Hashing the script directory's copy instead would describe different bytes from the ones
// shipped - and because enrichment refuses to contradict the artifact, it would fail a build
// that WiX completed perfectly well.
func TestPayloadIsHashedWhereWiXResolvesIt(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()

	// The output, and therefore the .wxs, lands in a subdirectory that carries its own
	// app.txt. That copy is first in the bind order, so it is the one WiX packages.
	out := filepath.Join(dir, "out")
	write(t, filepath.Join(dir, "app.txt"), "the SCRIPT directory's copy\n")
	write(t, filepath.Join(out, "app.txt"), "the WXS directory's copy, which WiX finds first\n")

	script := scriptFor(t, dir, filepath.Join("out", "app.msi"), competingPayloadScript)
	buildWithSBOM(t, script, &cliArgs{})

	artifact := filepath.Join(out, "app.msi")
	doc, _ := readDoc(t, artifact)

	// The document exists at all, which it would not if the build record had disagreed with
	// the artifact - the refusal is fatal by design.
	var payload *sbom.Component
	for i := range doc.Components {
		if compProp(doc.Components[i], "msis:role") == "payload" {
			payload = &doc.Components[i]
		}
	}
	if payload == nil {
		t.Fatal("no payload component")
	}

	// And the bytes really are the WXS directory's, independently confirmed.
	wanted, err := os.ReadFile(filepath.Join(out, "app.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if payload.Hashes[0].Content != sha256Hex(wanted) {
		t.Fatalf("the package contains %s..., but the WXS directory's copy is %s... - this "+
			"test is not measuring the bind path order",
			payload.Hashes[0].Content[:16], sha256Hex(wanted)[:16])
	}
	if got := compProp(*payload, "msis:build.source"); got != "app.txt" {
		t.Errorf("build source = %q, want app.txt", got)
	}
	if got := compProp(*payload, "msis:build.sourceRoot"); got != "wxs" {
		t.Errorf("source root = %q, want wxs - that is where WiX found it", got)
	}
}

// /STANDALONE documents have to pass the same profile as every other document. They are the
// one shape that carries a component with NO digest of any kind - nothing was distributed for
// a runtime the installer merely detects - so the profile has to admit exactly that and no more.
func TestAStandaloneDocumentConforms(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "app.txt"), "the application\n")
	script := scriptFor(t, dir, "app.msi", standaloneScript)
	buildWithSBOM(t, script, &cliArgs{standalone: true})

	doc, raw := readDoc(t, filepath.Join(dir, "app.msi"))

	// Derived from the SCRIPT, not from the document: it declares one <requires> and the
	// build was /STANDALONE, so exactly one detected runtime is expected.
	var detected []string
	for _, c := range doc.Components {
		if compProp(c, "msis:role") == "required-runtime" {
			detected = append(detected, c.BOMRef)
		}
	}
	if len(detected) != 1 {
		t.Fatalf("%d detected runtimes, want the one the script requires", len(detected))
	}

	want := conformance.Expected{
		IdentifiedComponents: []string{doc.Metadata.Component.BOMRef},
		DetectedComponents:   detected,
	}
	for _, p := range conformance.Check(raw, want) {
		t.Errorf("conformance: %v", p)
	}
}

// --- the explicit bundle, twice more ---------------------------------------------------------

const cachedPrereqBundleScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="CachedPrereqBundle"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{1D4F7A20-6B18-4C55-9A31-2F80B6E4D913}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <bundle>
    <prerequisite type="vcredist" version="2022"/>
    <msi source="inner\inner.msi"/>
  </bundle>
</setup>`

// chainableMSI builds a real MSI for a bundle to chain: a <bundle> requires one, and a
// stand-in file will not do - WiX reads the package.
func chainableMSI(t *testing.T, dir string) string {
	t.Helper()
	inner := filepath.Join(dir, "inner")
	write(t, filepath.Join(inner, "app.txt"), "the inner application\n")
	script := scriptFor(t, inner, "inner.msi", payloadScriptOneFile)
	if err := processFile(script, &cliArgs{
		build: true, setOverrides: map[string]string{}, templateFolder: repoTemplates(t)}); err != nil {
		t.Fatalf("building the chained MSI: %v", err)
	}
	return filepath.Join(inner, "inner.msi")
}

// seedCache points the prerequisite cache at a directory of this test's own and fills it, so
// the build resolves every architecture it may ask for without going near the network.
func seedCache(t *testing.T, dir, typ, version string) map[string]string {
	t.Helper()
	t.Setenv("LOCALAPPDATA", dir)
	seeded := map[string]string{}
	for _, arch := range []string{"x64", "x86", "arm64"} {
		u := prereqcache.LookupDownloadURL(typ, version, arch)
		if u == nil {
			continue
		}
		path := filepath.Join(dir, "msis", "prerequisites", typ, version, u.FileName)
		write(t, path, "stands in for "+u.FileName+", "+arch+"\n")
		seeded[arch] = path
	}
	if len(seeded) == 0 {
		t.Fatalf("msis knows no download for %s %s, so nothing can be seeded", typ, version)
	}
	return seeded
}

// An explicit <bundle> resolves its prerequisites LATER than it constructs its generator:
// CachedPaths is empty until EnsurePrerequisites has run. Recording before that point left
// every downloaded prerequisite with no architecture, no cache entry and no download URL -
// WiX packaged the cached file perfectly well, and the document simply could not say where
// those bytes came from.
//
// It takes a real build to see: a unit test handed an already-populated map is testing the
// recording, not the order the build does things in.
func TestACachedPrerequisiteKeepsItsProvenanceThroughTheBuild(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	chainableMSI(t, dir)
	seeded := seedCache(t, filepath.Join(dir, "appdata"), "vcredist", "2022")

	script := scriptFor(t, dir, "suite.exe", cachedPrereqBundleScript)
	buildWithSBOM(t, script, &cliArgs{})

	doc, _ := readDoc(t, filepath.Join(dir, "suite.exe"))

	// Every architecture the bundle actually carries is attributed, and each to ITS OWN
	// cached file - the digests differ, so a mislabelled architecture cannot pass.
	found := map[string]sbom.Component{}
	for _, c := range doc.Components {
		if compProp(c, "msis:prerequisite.type") == "vcredist" {
			found[compProp(c, "msis:prerequisite.arch")] = c
		}
	}
	if len(found) == 0 {
		t.Fatalf("no prerequisite was attributed; the record lost its provenance. Unresolved: %v",
			allMetaProps(doc, "msis:build.unresolved"))
	}
	for arch, c := range found {
		path, ok := seeded[arch]
		if !ok {
			t.Errorf("the document claims architecture %q, which was never seeded", arch)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Hashes) == 0 || c.Hashes[0].Content != sha256Hex(data) {
			t.Errorf("%s: the component's digest is not that of the cached %s file", arch, arch)
		}
		// The cache entry it came from, symbolically - not the machine path.
		wantCache := "prerequisite-cache:vcredist/2022/" + filepath.Base(path)
		if got := compProp(c, "msis:prerequisite.cache"); got != wantCache {
			t.Errorf("%s: cache = %q, want %q", arch, got, wantCache)
		}
		// And the URL it would have been fetched from. This one was seeded rather than
		// downloaded, but the entry IS the cache's, so the download provenance is real.
		var distributed bool
		for _, r := range c.ExternalReferences {
			if r.Type == "distribution" {
				distributed = true
			}
		}
		if !distributed {
			t.Errorf("%s: no download provenance", arch)
		}
	}
}

const competingBundleScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="CompetingBundle"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{7B2C9E14-55A8-4F03-8D6E-3A91C4F70B26}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <bundle>
    <prerequisite type="vcredist" version="2022" source="vcstub.exe"/>
    <exe id="Tool" source="tool.exe" detect="HKLM\SOFTWARE\msis-tests\Tool" args="/quiet"/>
    <msi source="inner\inner.msi"/>
  </bundle>
</setup>`

// A bundle's own sources go through WiX's bind paths exactly like payload does: the generated
// WXS writes SourceFile='tool.exe' verbatim, and WiX resolves it - first match wins, and the
// WXS directory comes before the script's. Hashing the script directory's copy would leave the
// installer that actually shipped unattributed while publishing a source path for bytes nobody
// packaged.
//
// The published root is the other half: without it "tool.exe" reads as relative to the .msis,
// which here is a different file.
func TestBundleSourcesResolveThroughBindPathsAndSayWhere(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	chainableMSI(t, dir)

	// Two copies of each, and the one WiX finds first is NOT the one beside the .msis.
	write(t, filepath.Join(dir, "tool.exe"), "the SCRIPT directory's tool\n")
	write(t, filepath.Join(out, "tool.exe"), "the WXS directory's tool, which WiX finds first\n")
	write(t, filepath.Join(dir, "vcstub.exe"), "the SCRIPT directory's prerequisite\n")
	write(t, filepath.Join(out, "vcstub.exe"), "the WXS directory's prerequisite\n")

	script := scriptFor(t, dir, filepath.Join("out", "suite.exe"), competingBundleScript)
	buildWithSBOM(t, script, &cliArgs{})

	doc, _ := readDoc(t, filepath.Join(out, "suite.exe"))

	for _, tc := range []struct{ source, wantProp string }{
		{"tool.exe", ""},
		{"vcstub.exe", "msis:prerequisite.type"},
	} {
		var c *sbom.Component
		for i := range doc.Components {
			if compProp(doc.Components[i], "msis:build.source") == tc.source {
				c = &doc.Components[i]
			}
		}
		if c == nil {
			t.Errorf("%s was not attributed to any component of the bundle. Unresolved: %v",
				tc.source, allMetaProps(doc, "msis:build.unresolved"))
			continue
		}
		data, err := os.ReadFile(filepath.Join(out, tc.source))
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Hashes) == 0 || c.Hashes[0].Content != sha256Hex(data) {
			t.Errorf("%s: the attributed component does not carry the WXS directory's copy, "+
				"so the record resolved somewhere WiX did not", tc.source)
		}
		if got := compProp(*c, "msis:build.sourceRoot"); got != "wxs" {
			t.Errorf("%s: source root = %q, want wxs", tc.source, got)
		}
		if tc.wantProp != "" && compProp(*c, tc.wantProp) == "" {
			t.Errorf("%s: %s is missing", tc.source, tc.wantProp)
		}
	}
}

func allMetaProps(doc *sbom.Document, name string) []string {
	var out []string
	for _, p := range doc.Metadata.Properties {
		if p.Name == name {
			out = append(out, p.Value)
		}
	}
	return out
}
