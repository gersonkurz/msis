package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/variables"
	"github.com/gersonkurz/msis/internal/wix"
)

// TestBundleWxsRoundTrip pins the coupling between the two halves of the auto-bundle naming
// (issue #27): main writes the wrapper as "<base>-bundle.wxs", and wix.NewBundleBuilder strips
// that suffix again to name the .exe. If either side drifts, the bundle stops landing beside the
// MSI under the name the user expects - which is how it ended up in the process's working
// directory, reported at a third path that did not exist.
func TestBundleWxsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	// A product version in the name, because filepath.Ext reads ".0" as an extension.
	msiPath := filepath.Join(dir, "X86 Bundle Probe-1.0.0.msi")

	wxsFile := bundleWxsPath(strings.TrimSuffix(msiPath, filepath.Ext(msiPath)))
	if want := filepath.Join(dir, "X86 Bundle Probe-1.0.0-bundle.wxs"); wxsFile != want {
		t.Fatalf("wxs = %q, want %q", wxsFile, want)
	}

	b := wix.NewBundleBuilder(variables.New(), wxsFile, "", "", dir, false)
	want := filepath.Join(dir, "X86 Bundle Probe-1.0.0.exe")
	if b.OutputFile != want {
		t.Errorf("OutputFile = %q, want %q", b.OutputFile, want)
	}
	if !filepath.IsAbs(b.OutputFile) {
		t.Errorf("OutputFile = %q, want an absolute path beside the MSI", b.OutputFile)
	}
}

// TestBundleFileWxsDefaultsBesideSource covers the explicit <bundle> path, which had the same two
// defects: without BUILD_TARGET it named its artifacts PRODUCT_NAME-PRODUCT_VERSION - a relative
// path resolved against the working directory - and filepath.Ext then ate the patch version.
func TestBundleFileWxsDefaultsBesideSource(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "setup.msis")

	vars := variables.New()
	vars["PRODUCT_NAME"] = "Probe"
	vars["PRODUCT_VERSION"] = "1.0.0"

	b := wix.NewBundleBuilder(vars, bundleWxsPath(bundleBaseName(filename, vars)), "", "", dir, false)
	if want := filepath.Join(dir, "setup.exe"); b.OutputFile != want {
		t.Errorf("OutputFile = %q, want %q", b.OutputFile, want)
	}

	// An explicit BUILD_TARGET still places the artifact itself, patch version intact.
	vars["BUILD_TARGET"] = `dist\probe-1.0.0-setup.exe`
	if got, want := bundleBaseName(filename, vars), `dist\probe-1.0.0-setup`; got != want {
		t.Errorf("bundleBaseName with BUILD_TARGET = %q, want %q", got, want)
	}
	b = wix.NewBundleBuilder(vars, bundleWxsPath(bundleBaseName(filename, vars)), "", "", dir, false)
	if want := `dist\probe-1.0.0-setup.exe`; b.OutputFile != want {
		t.Errorf("OutputFile = %q, want %q", b.OutputFile, want)
	}

	// A BUILD_TARGET naming a .msi keeps producing "<same name>.exe" beside it, as before.
	vars["BUILD_TARGET"] = `dist\probe-1.0.0.msi`
	if got, want := bundleBaseName(filename, vars), `dist\probe-1.0.0`; got != want {
		t.Errorf("bundleBaseName with a .msi BUILD_TARGET = %q, want %q", got, want)
	}
	b = wix.NewBundleBuilder(vars, bundleWxsPath(bundleBaseName(filename, vars)), "", "", dir, false)
	if want := `dist\probe-1.0.0.exe`; b.OutputFile != want {
		t.Errorf("OutputFile = %q, want %q", b.OutputFile, want)
	}
}
