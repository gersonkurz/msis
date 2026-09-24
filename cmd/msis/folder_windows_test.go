//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const folderScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="Folder"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{9E4D3A86-5F7C-4B03-9D29-4A8C6E0F3B86}"/>
  <set name="PLATFORM" value="x64"/>
  <set name="BUILD_TARGET" value="{{TARGET}}"/>
  <feature name="Main">
    <files source="lib" target="[INSTALLDIR]lib"/>
    <files source="other.txt" target="[INSTALLDIR]"/>
  </feature>
  {{COMPONENTS}}
</setup>`

// folderFixture installs lib\a.txt, lib\sub\b.txt and other.txt.
func folderFixture(t *testing.T, components string) (dir, script string) {
	t.Helper()
	requireWix(t)
	dir = t.TempDir()
	for _, f := range []string{filepath.Join("lib", "a.txt"), filepath.Join("lib", "sub", "b.txt"), "other.txt"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, f), "content of "+f+"\n")
	}
	return dir, scriptFor(t, dir, "folder.msi", strings.Replace(folderScript, "{{COMPONENTS}}", components, 1))
}

// declaredFiles is the files the document marks as described by a <component>, and checks that
// each carries the folder's facts on its own component.
func declaredFiles(t *testing.T, dir string) map[string]bool {
	t.Helper()
	doc, _ := readDoc(t, filepath.Join(dir, "folder.msi"))
	out := map[string]bool{}
	for _, c := range doc.Components {
		by := ""
		for _, p := range c.Properties {
			if p.Name == "msis:declared.by" {
				by = p.Value
			}
		}
		if by == "" {
			continue
		}
		out[c.Name] = true
		if len(c.Licenses) != 2 || c.Licenses[0].License == nil || c.Licenses[0].License.ID != "MIT" {
			t.Errorf("%s: licences %+v, want the folder's MIT pair", c.Name, c.Licenses)
		}
	}
	return out
}

// #65 with the real wix: a folder declaration covers every file under it - recursively unless
// recursive="no" - each on its own component, and nothing outside it.
func TestAFolderDeclarationCoversItsFiles(t *testing.T) {
	dir, script := folderFixture(t, `<component for="[INSTALLDIR]lib\" license="MIT" creator="https://foo.example"/>`)
	buildWithSBOM(t, script, &cliArgs{})
	if got := declaredFiles(t, dir); !got["a.txt"] || !got["b.txt"] || got["other.txt"] || len(got) != 2 {
		t.Errorf("recursive folder declaration covers %v, want a.txt and b.txt only", got)
	}

	dir, script = folderFixture(t, `<component for="[INSTALLDIR]lib\" license="MIT" recursive="no"/>`)
	buildWithSBOM(t, script, &cliArgs{})
	if got := declaredFiles(t, dir); !got["a.txt"] || len(got) != 1 {
		t.Errorf("non-recursive folder declaration covers %v, want a.txt only", got)
	}
}

// The product owner's decision: a file covered by a folder declaration AND named by its own is
// described twice, and refused on every run. A folder that covers nothing is an error.
func TestAFolderDeclarationOverlapIsRefused(t *testing.T) {
	_, script := folderFixture(t, `<component for="[INSTALLDIR]lib\" license="MIT"/>
  <component for="[INSTALLDIR]lib\sub\b.txt" version="2.0"/>`)
	err := processFile(script, &cliArgs{setOverrides: map[string]string{}, templateFolder: repoTemplates(t)})
	if err == nil || !strings.Contains(err.Error(), "both ") {
		t.Errorf("an overlapping declaration: want a refusal, got %v", err)
	}

	_, script = folderFixture(t, `<component for="[INSTALLDIR]nothing-here\" license="MIT"/>`)
	err = processFile(script, &cliArgs{setOverrides: map[string]string{}, templateFolder: repoTemplates(t)})
	if err == nil || !strings.Contains(err.Error(), "no file is installed under that folder") {
		t.Errorf("an empty folder declaration: want a refusal, got %v", err)
	}

	// "[INSTALLDIR]li\" is not a prefix of "[INSTALLDIR]lib\..." - the boundary is the separator.
	_, script = folderFixture(t, `<component for="[INSTALLDIR]li\" license="MIT"/>`)
	err = processFile(script, &cliArgs{setOverrides: map[string]string{}, templateFolder: repoTemplates(t)})
	if err == nil || !strings.Contains(err.Error(), "no file is installed under that folder") {
		t.Errorf("a partial folder name matched: %v", err)
	}
}
