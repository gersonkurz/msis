package main

import (
	"debug/buildinfo"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

// TestComponentDocsDescribeTheBytesTheyNameAndValidate builds a real msis binary, so its module
// graph is genuine, and checks the documents setup.msis composes: each names its file by the SHA-256
// msis will compare against the packaged file, lists what build info says is linked in (the Go
// modules and the toolchain's stdlib; the hook DLL's NuGet libraries), states its coverage, and is
// valid CycloneDX 1.6 against the vendored schema.
func TestComponentDocsDescribeTheBytesTheyNameAndValidate(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	binDir, dist, templates := filepath.Join(dir, "bin"), filepath.Join(dir, "dist"), filepath.Join(dir, "templates")
	exe := filepath.Join(binDir, "msis-x64.exe")
	cmd := exec.Command("go", "build", "-o", exe, "github.com/gersonkurz/msis/cmd/msis")
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building msis: %v\n%s", err, out)
	}
	for _, arch := range arches {
		if err := os.MkdirAll(filepath.Join(templates, arch), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(templates, arch, "msi-simplica.dll"), []byte("hook "+arch), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := writeComponentDocs("9.9.9", dist, binDir, templates, []string{"x64"}); err != nil {
		t.Fatalf("writeComponentDocs: %v", err)
	}

	read := func(name string) (componentDoc, []byte) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(componentsDir(dist), name))
		if err != nil {
			t.Fatal(err)
		}
		if err := conformance.ValidateSchema(data); err != nil {
			t.Errorf("%s is not valid CycloneDX 1.6: %v", name, err)
		}
		var doc componentDoc
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		return doc, data
	}
	wantHash := func(name, path string, got component) {
		t.Helper()
		sum, err := sha256File(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Hashes) != 1 || got.Hashes[0].Content != sum {
			t.Errorf("%s: subject hash %v, want the file's SHA-256 %s", name, got.Hashes, sum)
		}
	}
	coverage := func(name string, doc componentDoc, assemblies string) {
		t.Helper()
		ref := doc.Metadata.Component.BOMRef
		want := []composition{
			{Aggregate: assemblies, Assemblies: []string{ref}},
			{Aggregate: "unknown", Dependencies: []string{ref}},
		}
		got, _ := json.Marshal(doc.Compositions)
		exp, _ := json.Marshal(want)
		if string(got) != string(exp) {
			t.Errorf("%s: compositions %s, want %s", name, got, exp)
		}
	}

	// The msis binary: every module build info records, plus stdlib at the toolchain's version.
	doc, _ := read(binaryDocName("x64"))
	root := doc.Metadata.Component
	wantHash("msis-x64", exe, root)
	coverage("msis-x64", doc, "complete")
	info, err := buildinfo.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, c := range root.Components {
		if c.BOMRef != c.PURL || c.PURL == "" {
			t.Errorf("msis-x64: %s has bom-ref %q and purl %q; both must be the purl", c.Name, c.BOMRef, c.PURL)
		}
		have[c.PURL] = true
	}
	for _, dep := range info.Deps {
		if dep.Replace != nil {
			dep = dep.Replace
		}
		if p := "pkg:golang/" + dep.Path + "@" + dep.Version; !have[p] {
			t.Errorf("msis-x64: module %s is linked in but not listed", p)
		}
	}
	if p := "pkg:golang/stdlib@" + strings.TrimPrefix(info.GoVersion, "go"); !have[p] {
		t.Errorf("msis-x64: %s is not listed", p)
	}
	if len(root.Components) != len(info.Deps)+1 {
		t.Errorf("msis-x64: %d components, want %d modules + stdlib", len(root.Components), len(info.Deps))
	}

	// #62: every linked module and the stdlib carry the licence concluded from their own text,
	// and the document says when it was obtained.
	// lic is the licence id when a component carries exactly BSI's pair (decisions D12): the
	// original licence, declared, and the distribution licence, concluded; "" otherwise.
	lic := func(c component) string {
		if len(c.Licenses) != 2 || c.Licenses[0].License.Acknowledgement != "declared" ||
			c.Licenses[1].License.Acknowledgement != "concluded" ||
			c.Licenses[0].License.ID != c.Licenses[1].License.ID {
			return ""
		}
		return c.Licenses[0].License.ID
	}
	if got := lic(root); got != "MIT" {
		t.Errorf("msis-x64: msis's own licence %q, want the MIT pair (the repository's LICENSE)", got)
	}
	for _, c := range root.Components {
		if lic(c) == "" {
			t.Errorf("msis-x64: %s carries licence %v, want the declared/concluded pair", c.Name, c.Licenses)
		}
	}
	for name, want := range map[string]string{
		"stdlib": "BSD-3-Clause",
		"github.com/santhosh-tekuri/jsonschema/v6": "Apache-2.0",
		"github.com/aymerick/raymond":              "MIT",
		"golang.org/x/sys":                         "BSD-3-Clause",
	} {
		for _, c := range root.Components {
			if c.Name == name && lic(c) != want {
				t.Errorf("msis-x64: %s licence %q, want %q", name, lic(c), want)
			}
		}
	}
	if len(doc.Metadata.Licenses) != 1 || doc.Metadata.Licenses[0].Expression != "CC0-1.0" {
		t.Errorf("msis-x64: data licence %+v, want CC0-1.0 (msis's own documents, #62)", doc.Metadata.Licenses)
	}
	if len(doc.Metadata.Lifecycles) != 1 || doc.Metadata.Lifecycles[0].Phase != "post-build" {
		t.Errorf("msis-x64: lifecycles %v, want [post-build]", doc.Metadata.Lifecycles)
	}

	// Only the requested binaries are described; every hook DLL always is.
	if _, err := os.Stat(filepath.Join(componentsDir(dist), binaryDocName("x86"))); !os.IsNotExist(err) {
		t.Errorf("a document for msis-x86.exe was written although only x64 was asked for (err %v)", err)
	}
	for _, arch := range arches {
		name := hookDocName(arch)
		doc, _ := read(name)
		wantHash(name, filepath.Join(templates, arch, "msi-simplica.dll"), doc.Metadata.Component)
		coverage(name, doc, "incomplete")
		if got := len(doc.Metadata.Component.Components); got != len(nativeComponents) {
			t.Errorf("%s: %d libraries, want the %d NuGet packages", name, got, len(nativeComponents))
		}
		if got := lic(doc.Metadata.Component); got != "MIT" {
			t.Errorf("%s: msi-simplica licence %q, want the MIT pair", name, got)
		}
		for _, c := range doc.Metadata.Component.Components {
			if got := lic(c); got != "MS-RL" {
				t.Errorf("%s: %s licence %q, want the MS-RL pair (declared by its .nuspec)", name, c.Name, got)
			}
		}
	}
}

// A missing hook DLL is an error naming the step that stages it, not a document with no hash.
func TestComponentDocsRequireTheHookDlls(t *testing.T) {
	dir := t.TempDir()
	err := writeComponentDocs("9.9.9", filepath.Join(dir, "dist"), dir, filepath.Join(dir, "templates"), nil)
	if err == nil || !strings.Contains(err.Error(), "build-hooks") {
		t.Fatalf("want an error pointing at `just build-hooks`, got %v", err)
	}
}
