package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gersonkurz/msis/internal/wix"
)

// TestNugetVersionsMatchVcxproj is the guard on the one part of this SBOM that is copied by hand.
// Go cannot read an MSBuild project, so the hook DLL's libraries are written out in main.go; if
// someone bumps WcaUtil in the .vcxproj the SBOM would quietly keep describing the old one, which
// is precisely the failure an SBOM exists to prevent.
func TestNugetVersionsMatchVcxproj(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "native", "msi-simplica", "msi-simplica.vcxproj"))
	if err != nil {
		t.Fatalf("reading the hook project: %v", err)
	}

	re := regexp.MustCompile(`<PackageReference Include="([^"]+)" Version="([^"]+)"`)
	inProject := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(string(data), -1) {
		inProject[m[1]] = m[2]
	}
	if len(inProject) == 0 {
		t.Fatal("no <PackageReference> found; the project layout changed and this guard is now blind")
	}

	inSBOM := map[string]string{}
	for _, c := range nativeComponents {
		inSBOM[c.Name] = c.Version
	}

	for name, version := range inProject {
		got, ok := inSBOM[name]
		if !ok {
			t.Errorf("%s %s is referenced by the hook DLL but missing from the SBOM", name, version)
			continue
		}
		if got != version {
			t.Errorf("%s: SBOM says %s, the .vcxproj says %s", name, got, version)
		}
		if want := "pkg:nuget/" + name + "@" + version; !hasPURL(want) {
			t.Errorf("missing or wrong purl, want %q", want)
		}
	}
	for name := range inSBOM {
		if _, ok := inProject[name]; !ok {
			t.Errorf("SBOM lists %s, which the hook DLL no longer references", name)
		}
	}
}

func hasPURL(want string) bool {
	for _, c := range nativeComponents {
		if c.PURL == want {
			return true
		}
	}
	return false
}

// TestBuildProducesAValidDocument exercises the whole assembly against real binaries: three are
// compiled here for the three shipped architectures, and four stand-in release files are hashed.
// It is the only way to know that debug/buildinfo actually yields the module graph - reading the
// code cannot tell you that, and the graph is the substance of the SBOM.
func TestBuildProducesAValidDocument(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	dist := filepath.Join(dir, "dist")
	for _, d := range []string{binDir, dist} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// The real release builds windows/{amd64,386,arm64}; cross-compiling the actual command is
	// what puts a genuine module graph (raymond, go-regis3, x/term ...) into the test binaries.
	for arch, goarch := range map[string]string{"x64": "amd64", "x86": "386", "arm64": "arm64"} {
		out := filepath.Join(binDir, "msis-"+arch+".exe")
		cmd := exec.Command("go", "build", "-o", out, "github.com/gersonkurz/msis/cmd/msis")
		cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+goarch)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("building %s: %v\n%s", arch, err, out)
		}
	}

	const version = "9.9.9"
	for _, name := range []string{
		"msis-" + version + "-x64.msi",
		"msis-" + version + "-x86.msi",
		"msis-" + version + "-arm64.msi",
		"msis-" + version + "-setup.exe",
	} {
		if err := os.WriteFile(filepath.Join(dist, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Walk the release pipeline the justfile drives: record the binaries and toolchain before
	// packaging, seal once the artifacts exist, then generate.
	if err := capture(version, dist, binDir, "7.0.0+observed"); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := seal(version, dist, binDir); err != nil {
		t.Fatalf("seal: %v", err)
	}
	doc, err := build(version, dist, binDir)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Round-trip through JSON: the file is the deliverable, not the struct.
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"bomFormat", "specVersion", "version", "metadata", "components"} {
		if _, ok := got[key]; !ok {
			t.Errorf("CycloneDX requires %q", key)
		}
	}
	if got["bomFormat"] != "CycloneDX" || got["specVersion"] != "1.6" {
		t.Errorf("bomFormat/specVersion = %v/%v", got["bomFormat"], got["specVersion"])
	}

	byName := map[string]component{}
	for _, c := range doc.Components {
		byName[c.Name] = c
	}

	// Every direct dependency of the module, read back out of a binary that was really built.
	for _, want := range []string{
		"github.com/aymerick/raymond",
		"github.com/gersonkurz/go-regis3",
		"golang.org/x/term",
	} {
		c, ok := byName[want]
		if !ok {
			t.Errorf("Go dependency %s missing from the SBOM", want)
			continue
		}
		if c.Version == "" {
			t.Errorf("%s has no version", want)
		}
		if want := "pkg:golang/" + c.Name + "@" + c.Version; c.PURL != want {
			t.Errorf("%s purl = %q, want %q", c.Name, c.PURL, want)
		}
		if !strings.HasPrefix(c.Description, "go.sum h1:") {
			t.Errorf("%s: want the go.sum dirhash recorded, got %q", c.Name, c.Description)
		}
	}

	// The non-Go half, which is the reason this tool exists rather than a one-line go-mod dump.
	for _, want := range []string{"msi-simplica", "WixToolset.WcaUtil", "WixToolset.DUtil"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("%s missing from the SBOM", want)
		}
	}

	// Released files, each tied to its bytes.
	for _, name := range []string{
		"msis-" + version + "-x64.msi",
		"msis-" + version + "-setup.exe",
	} {
		c, ok := byName[name]
		if !ok {
			t.Fatalf("release artifact %s missing", name)
		}
		if len(c.Hashes) != 1 || c.Hashes[0].Alg != "SHA-256" || len(c.Hashes[0].Content) != 64 {
			t.Errorf("%s: want one SHA-256 digest, got %+v", name, c.Hashes)
		}
	}

	// CycloneDX reserves metadata.tools for what produced the DOCUMENT.
	if len(doc.Metadata.Tools) != 1 || !strings.Contains(doc.Metadata.Tools[0].Name, "sbom") {
		t.Errorf("metadata.tools = %+v, want just this generator", doc.Metadata.Tools)
	}

	// The build toolchain is a property, and the WiX version is the OBSERVED one - never
	// wix.DefaultVersion, which is only what /SETUP-WIX installs by default.
	props := map[string]string{}
	for _, p := range doc.Metadata.Properties {
		props[p.Name] = p.Value
	}
	if got := props["msis:wix.version.observed"]; got != "7.0.0+observed" {
		t.Errorf("observed wix version = %q, want the value passed in", got)
	}
	if got := props["msis:wix.extensions.loaded"]; got != strings.Join(wix.AllExtensions, ",") {
		t.Errorf("extensions = %q, want %q", got, strings.Join(wix.AllExtensions, ","))
	}

	// Provenance: whatever the binaries say, the SBOM has to repeat it.
	var haveRevision bool
	for _, p := range doc.Metadata.Properties {
		if p.Name == "msis:vcs.revision" {
			haveRevision = true
		}
	}
	if !haveRevision && !testing.Short() {
		t.Log("no vcs.revision in the test binaries (expected when building outside a git checkout)")
	}
}

// TestDescribeDiffCoversProvenanceNotJustVersions: comparing only dependency names and versions
// let an older x86 binary through whenever its dependencies happened to be unchanged, and the
// document then attributed x64's commit to the whole release. The comparison now covers the main
// module, the commit, the Go toolchain and each dependency's go.sum dirhash.
func TestDescribeDiffCoversProvenanceNotJustVersions(t *testing.T) {
	base := map[string]string{
		"go version":            "go1.27.1",
		"main module":           "github.com/gersonkurz/msis@v1.0.0",
		"module count":          "1",
		"vcs.revision":          "aaaa",
		"vcs.modified":          "false",
		"module github.com/x/y": "v1.0.0 go.sum h1:AAA=",
	}

	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"a different commit", "vcs.revision", "bbbb"},
		{"one arch built from a dirty tree", "vcs.modified", "true"},
		{"a different Go toolchain", "go version", "go1.26.0"},
		{"a different main module version", "main module", "github.com/gersonkurz/msis@v1.0.1"},
		{"same version, different dirhash", "module github.com/x/y", "v1.0.0 go.sum h1:BBB="},
		{"a different dependency version", "module github.com/x/y", "v1.0.1 go.sum h1:AAA="},
	}
	for _, c := range cases {
		other := map[string]string{}
		for k, v := range base {
			other[k] = v
		}
		other[c.key] = c.value

		diff := describeDiff(base, other)
		if diff == "" {
			t.Errorf("%s: not reported", c.name)
			continue
		}
		if !strings.Contains(diff, c.key) {
			t.Errorf("%s: diff %q does not name %q", c.name, diff, c.key)
		}
	}

	// A module present in one and absent from the other.
	missing := map[string]string{}
	for k, v := range base {
		missing[k] = v
	}
	delete(missing, "module github.com/x/y")
	if diff := describeDiff(base, missing); !strings.Contains(diff, "absent") {
		t.Errorf("a dropped module must be reported as absent, got %q", diff)
	}

	if diff := describeDiff(base, base); diff != "" {
		t.Errorf("identical facts reported as different: %q", diff)
	}
}

// stageRelease lays out a release run: binaries and artifacts as plain files, captured and
// sealed. Content is what the tests mutate; timestamps are set to a fixed past instant so that
// nothing here can pass by looking recent.
func stageRelease(t *testing.T, version string) (dist, binDir string) {
	t.Helper()

	dir := t.TempDir()
	binDir = filepath.Join(dir, "bin")
	dist = filepath.Join(dir, "dist")
	for _, d := range []string{binDir, dist} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(dir, name, content string) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range binaryNames() {
		write(binDir, name, "binary "+name)
	}
	if err := capture(version, dist, binDir, "6.0.2+captured"); err != nil {
		t.Fatalf("capture: %v", err)
	}
	for _, name := range artifactNames(version) {
		write(dist, name, "artifact "+name)
	}
	if err := seal(version, dist, binDir); err != nil {
		t.Fatalf("seal: %v", err)
	}
	return dist, binDir
}

// TestSubstitutedFilesAreRejected is the regression for the binding between the inventory and the
// installers. Timestamps cannot carry it: copying an older release's assets in, or refreshing an
// mtime, restores the appearance of a matched pair. Every case here keeps the file's modification
// time in the past, so only the recorded hashes can be doing the work.
func TestSubstitutedFilesAreRejected(t *testing.T) {
	const version = "9.9.9"

	swap := func(t *testing.T, dir, name, content string) {
		t.Helper()
		p := filepath.Join(dir, name)
		before, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		// Put the timestamp back exactly as it was: the check must not depend on it.
		if err := os.Chtimes(p, before.ModTime(), before.ModTime()); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("a binary replaced after packaging", func(t *testing.T) {
		dist, binDir := stageRelease(t, version)
		swap(t, binDir, "msis-x86.exe", "a different binary")

		_, err := build(version, dist, binDir)
		if err == nil {
			t.Fatal("a substituted binary must be rejected")
		}
		if !strings.Contains(err.Error(), "msis-x86.exe") || !strings.Contains(err.Error(), "packaging recorded") {
			t.Errorf("error = %v, want it to name the file and the mismatch", err)
		}
	})

	t.Run("an installer replaced after packaging", func(t *testing.T) {
		dist, binDir := stageRelease(t, version)
		swap(t, dist, "msis-"+version+"-arm64.msi", "someone else's installer")

		_, err := build(version, dist, binDir)
		if err == nil {
			t.Fatal("a substituted installer must be rejected")
		}
		if !strings.Contains(err.Error(), "arm64.msi") {
			t.Errorf("error = %v, want it to name the file", err)
		}
	})

	t.Run("an installer whose timestamp is refreshed", func(t *testing.T) {
		dist, binDir := stageRelease(t, version)
		swap(t, dist, "msis-"+version+"-x64.msi", "stale content, fresh mtime")
		now := time.Now()
		if err := os.Chtimes(filepath.Join(dist, "msis-"+version+"-x64.msi"), now, now); err != nil {
			t.Fatal(err)
		}

		if _, err := build(version, dist, binDir); err == nil {
			t.Fatal("refreshing a timestamp must not make a substituted installer acceptable")
		}
	})

	t.Run("no manifest at all", func(t *testing.T) {
		dist, binDir := stageRelease(t, version)
		if err := os.Remove(manifestPath(dist)); err != nil {
			t.Fatal(err)
		}

		_, err := build(version, dist, binDir)
		if err == nil {
			t.Fatal("generating without packaging evidence must be refused")
		}
		if !strings.Contains(err.Error(), "release-all") {
			t.Errorf("error = %v, want it to say how to get one", err)
		}
	})

	t.Run("packaging never finished", func(t *testing.T) {
		dist, binDir := stageRelease(t, version)
		m, err := readManifest(dist)
		if err != nil {
			t.Fatal(err)
		}
		m.Artifacts = nil
		if err := writeManifest(dist, m); err != nil {
			t.Fatal(err)
		}

		if _, err := build(version, dist, binDir); err == nil || !strings.Contains(err.Error(), "never sealed") {
			t.Errorf("an unsealed manifest must be refused, got %v", err)
		}
	})

	t.Run("seal refuses binaries that changed during packaging", func(t *testing.T) {
		dir := t.TempDir()
		binDir := filepath.Join(dir, "bin")
		dist := filepath.Join(dir, "dist")
		for _, d := range []string{binDir, dist} {
			if err := os.MkdirAll(d, 0755); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range binaryNames() {
			if err := os.WriteFile(filepath.Join(binDir, name), []byte("binary "+name), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if err := capture(version, dist, binDir, ""); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(binDir, "msis-x64.exe"), []byte("rebuilt mid-run"), 0644); err != nil {
			t.Fatal(err)
		}
		for _, name := range artifactNames(version) {
			if err := os.WriteFile(filepath.Join(dist, name), []byte("artifact "+name), 0644); err != nil {
				t.Fatal(err)
			}
		}

		if err := seal(version, dist, binDir); err == nil {
			t.Fatal("seal must reject binaries that changed between capture and packaging")
		}
	})
}

// TestWixVersionComesFromPackagingNotTheCurrentEnvironment: build with WiX 6, upgrade to WiX 7,
// regenerate — the finished release must not acquire WiX 7 provenance. The version is observed
// once, at capture time, and read back from the manifest thereafter.
func TestWixVersionComesFromPackagingNotTheCurrentEnvironment(t *testing.T) {
	const version = "9.9.9"
	dist, binDir := stageRelease(t, version) // captured with "6.0.2+captured"

	m, err := readManifest(dist)
	if err != nil {
		t.Fatal(err)
	}
	if m.WixVersion != "6.0.2+captured" {
		t.Fatalf("manifest WiX version = %q, want the one observed at capture", m.WixVersion)
	}

	// build() takes no WiX argument at all, so there is no path by which today's toolchain can
	// reach the document: whatever is installed now, the recorded value is what gets written.
	_, err = build(version, dist, binDir)
	if err == nil {
		t.Fatal("expected the stand-in binaries to fail buildinfo parsing")
	}
	if strings.Contains(err.Error(), "wix") {
		t.Errorf("generation must not consult WiX: %v", err)
	}

	// And the provenance the document would carry is the captured string.
	props := toolchainProvenance(m.WixVersion)
	var found string
	for _, p := range props {
		if p.Name == "msis:wix.version.observed" {
			found = p.Value
		}
	}
	if found != "6.0.2+captured" {
		t.Errorf("recorded WiX version = %q, want %q", found, "6.0.2+captured")
	}
}

// TestMissingArtifactIsAnError: an SBOM naming a file that was not built would be a lie about the
// release, so a missing artifact fails instead of being skipped.
func TestMissingArtifactIsAnError(t *testing.T) {
	_, err := statFiles(t.TempDir(), artifactNames("9.9.9"))
	if err == nil {
		t.Fatal("a missing release artifact must fail the SBOM")
	}
	if !strings.Contains(err.Error(), "release file missing") || !strings.Contains(err.Error(), "release-all") {
		t.Errorf("error = %v, want it to say what is missing and what to run", err)
	}
}

// TestWixVersionIsObservedNotAssumed: msis builds with whichever of WiX 6 or 7 is installed, so
// claiming wix.DefaultVersion would report a WiX 6 build as 7.0.0. When WiX cannot be queried the
// property is omitted rather than guessed.
func TestWixVersionIsObservedNotAssumed(t *testing.T) {
	has := func(props []property, name string) (string, bool) {
		for _, p := range props {
			if p.Name == name {
				return p.Value, true
			}
		}
		return "", false
	}

	if v, ok := has(toolchainProvenance("6.0.2+b3f3403"), "msis:wix.version.observed"); !ok || v != "6.0.2+b3f3403" {
		t.Errorf("observed WiX 6 recorded as %q (present=%v), want it verbatim", v, ok)
	}
	for _, unavailable := range []string{"", "  ", "(unavailable)"} {
		if _, ok := has(toolchainProvenance(unavailable), "msis:wix.version.observed"); ok {
			t.Errorf("%q: an unknown WiX version must be omitted, not recorded", unavailable)
		}
	}
	if v, ok := has(toolchainProvenance(""), "msis:wix.extensions.loaded"); !ok || v != strings.Join(wix.AllExtensions, ",") {
		t.Errorf("extensions = %q (present=%v)", v, ok)
	}
}

func TestSha256FileMatchesKnownDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("abc"), 0644); err != nil {
		t.Fatal(err)
	}
	// The SHA-256 of "abc", a published test vector.
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	got, err := sha256File(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("sha256(%q) = %s, want %s", "abc", got, want)
	}
}
