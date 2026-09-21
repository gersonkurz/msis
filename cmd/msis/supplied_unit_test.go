package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/generator"
	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/parser"
	"github.com/gersonkurz/msis/internal/variables"
)

// Matching `<sbom for=>` against the tree the build just produced (#36).
//
// No wix here: what is under test is which file a target names, which the generator settles
// before anything is compiled.

// generated parses a script, generates, and hands back the context and the script's path.
func generated(t *testing.T, dir, body string, files map[string]string) (*generator.Context, string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(dir, "setup.msis")
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	setup, err := parser.Parse(script)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	vars := variables.New()
	for _, s := range setup.Sets {
		vars[s.Name] = s.Value
	}
	if err := vars.ResolveAll(); err != nil {
		t.Fatal(err)
	}
	ctx := generator.NewContext(setup, vars, dir)
	if _, err := ctx.Generate(); err != nil {
		t.Fatalf("generating: %v", err)
	}
	return ctx, script
}

const suppliedScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="Supplied"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{2A6D91C4-0F7B-4E58-9C13-5D80B2E7A461}"/>
  <set name="INSTALLDIR" value="Acme\Widget"/>
  <feature name="Main">
    <files source="app.exe" target="[INSTALLDIR]"/>
    <files source="lib" target="[INSTALLDIR]lib"/>
  </feature>
</setup>`

var suppliedFiles = map[string]string{
	"app.exe":     "the application\n",
	"lib/one.txt": "library one\n",
}

// The everyday case, plus the two spellings a user is free to write: Windows paths do not care
// about case, and a .msis may use either separator.
func TestAnInstallTargetNamesItsFile(t *testing.T) {
	dir := t.TempDir()
	ctx, script := generated(t, dir, suppliedScript, suppliedFiles)
	if err := os.WriteFile(filepath.Join(dir, "app.cdx.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{
		`[INSTALLDIR]app.exe`,
		`[installdir]APP.EXE`,
		`[INSTALLDIR]/app.exe`,
	} {
		got, err := resolveSuppliedSBOMs(
			[]ir.SuppliedSBOM{{Source: "app.cdx.json", For: target}}, ctx, script)
		if err != nil {
			t.Fatalf("for=%q: %v", target, err)
		}
		if len(got) != 1 || got[0].FileID == "" {
			t.Fatalf("for=%q resolved to %+v", target, got)
		}
		if got[0].Target != target {
			t.Errorf("the target is not carried through as authored: %q", got[0].Target)
		}
	}

	// A file below the install directory is addressed the way the script placed it. INSTALLDIR
	// is "Acme\Widget" here, which the generator turns into a chain of directories with the
	// key on the last one - so [INSTALLDIR] has to mean that one, not the top of the chain.
	if _, err := resolveSuppliedSBOMs(
		[]ir.SuppliedSBOM{{Source: "app.cdx.json", For: `[INSTALLDIR]lib\one.txt`}}, ctx, script); err != nil {
		t.Errorf("a nested target did not resolve: %v", err)
	}
}

// A `for` that names nothing is a mistake in the script, and the error has to be actionable:
// the whole point of failing is that the author can see what they meant to write.
func TestATargetThatNamesNothingIsRefused(t *testing.T) {
	dir := t.TempDir()
	ctx, script := generated(t, dir, suppliedScript, suppliedFiles)

	_, err := resolveSuppliedSBOMs(
		[]ir.SuppliedSBOM{{Source: "app.cdx.json", For: `[INSTALLDIR]missing.exe`}}, ctx, script)
	if err == nil {
		t.Fatal("a target naming no file was accepted")
	}
	for _, want := range []string{"missing.exe", "app.exe", "lib\\one.txt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not mention %q", err, want)
		}
	}
}

const duplicateTargetScript = `<?xml version="1.0" encoding="utf-8"?>
<setup>
  <set name="PRODUCT_NAME" value="Supplied"/>
  <set name="PRODUCT_VERSION" value="1.0.0"/>
  <set name="MANUFACTURER" value="msis tests"/>
  <set name="UPGRADE_CODE" value="{2A6D91C4-0F7B-4E58-9C13-5D80B2E7A461}"/>
  <feature name="A">
    <files source="a\config.xml" target="[INSTALLDIR]"/>
  </feature>
  <feature name="B">
    <files source="b\config.xml" target="[INSTALLDIR]"/>
  </feature>
</setup>`

// msis already packages two DIFFERENT files to one destination - that is what short names exist
// for - so a target can genuinely match more than one. Picking whichever came first would
// attach someone's dependency graph to a file by coin toss and present it as a fact.
func TestATargetMatchingSeveralFilesIsRefused(t *testing.T) {
	dir := t.TempDir()
	ctx, script := generated(t, dir, duplicateTargetScript, map[string]string{
		"a/config.xml": "<a/>\n",
		"b/config.xml": "<b/>\n",
	})

	_, err := resolveSuppliedSBOMs(
		[]ir.SuppliedSBOM{{Source: "app.cdx.json", For: `[INSTALLDIR]config.xml`}}, ctx, script)
	if err == nil {
		t.Fatal("a target matching two files was resolved to one of them")
	}
	if !strings.Contains(err.Error(), "2 files") {
		t.Errorf("%q does not say how many files are there", err)
	}
	// Both candidates are named, so the author can see what they have to separate.
	if strings.Count(err.Error(), "FILE_ID") < 2 {
		t.Errorf("%q does not name the candidates", err)
	}
}

// The document is read by msis and never packaged, so it is resolved against the .msis - and a
// missing one is an error now rather than a document that silently describes less.
func TestAMissingSuppliedDocumentIsRefused(t *testing.T) {
	dir := t.TempDir()
	ctx, script := generated(t, dir, suppliedScript, suppliedFiles)

	_, err := resolveSuppliedSBOMs(
		[]ir.SuppliedSBOM{{Source: "absent.cdx.json", For: `[INSTALLDIR]app.exe`}}, ctx, script)
	if err == nil {
		t.Fatal("a missing document was accepted")
	}
	if !strings.Contains(err.Error(), "absent.cdx.json") {
		t.Errorf("%q does not name the document", err)
	}
}
