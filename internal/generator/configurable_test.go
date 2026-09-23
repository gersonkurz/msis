package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// #54: the root feature names INSTALLDIR as its ConfigurableDirectory only when the package
// declares an INSTALLDIR directory. A registry-only package declared none, the attribute
// dangled, and WiX failed with WIX0094.

func writeProbeReg(t *testing.T, dir string) string {
	t.Helper()
	const content = "Windows Registry Editor Version 5.00\n\n" +
		"[HKEY_LOCAL_MACHINE\\SOFTWARE\\Probe54]\n\"V\"=\"1\"\n"
	if err := os.WriteFile(filepath.Join(dir, "probe.reg"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return "probe.reg"
}

func generate54(t *testing.T, workDir string, setup *ir.Setup, vars variables.Dictionary) *GeneratedOutput {
	t.Helper()
	if vars == nil {
		vars = variables.New()
	}
	vars["PRODUCT_NAME"] = "Probe54"
	vars["UPGRADE_CODE"] = "{5A1D7E30-8C24-4F9B-A3E6-1B2C4D5E8F54}"
	out, err := NewContext(setup, vars, workDir).Generate()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRegistryOnlyFeatureIsNotConfigurable(t *testing.T) {
	dir := t.TempDir()
	setup := &ir.Setup{Features: []ir.Feature{{
		Name:  "Main",
		Items: []ir.Item{ir.Registry{File: writeProbeReg(t, dir)}},
	}}}
	out := generate54(t, dir, setup, nil)

	if strings.Contains(out.FeatureXML, "ConfigurableDirectory") {
		t.Errorf("a feature with nothing under INSTALLDIR names it as ConfigurableDirectory:\n%s", out.FeatureXML)
	}
	if strings.Contains(out.DirectoryXML, "INSTALLDIR") {
		t.Errorf("no INSTALLDIR directory was expected, got:\n%s", out.DirectoryXML)
	}
	if !strings.Contains(out.FeatureXML, "<ComponentRef Id='REG_CID_") {
		t.Errorf("the registry component must still belong to the feature:\n%s", out.FeatureXML)
	}
}

// The attribute is kept whenever INSTALLDIR is declared - files, or an item like set-env that
// places a component there without a file - and only on the root feature. The sub-feature is
// EMPTY so that it cannot create INSTALLDIR itself and mask what the root's items do (review
// of #54), and the root and child elements are asserted separately.
func TestFeatureWithInstallDirContentIsConfigurable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		items []ir.Item
	}{
		{"files", []ir.Item{ir.Files{Source: "app.txt", Target: "[INSTALLDIR]"}}},
		{"set-env only", []ir.Item{ir.SetEnv{Name: "V", Value: "x"}}},
		{"registry plus files", []ir.Item{ir.Registry{File: writeProbeReg(t, dir)}, ir.Files{Source: "app.txt", Target: "[INSTALLDIR]"}}},
	} {
		setup := &ir.Setup{Features: []ir.Feature{{
			Name:        "Main",
			Items:       tc.items,
			SubFeatures: []ir.Feature{{Name: "Sub"}},
		}}}
		out := generate54(t, dir, setup, variables.Dictionary{"INSTALLDIR": "Probe54"})
		root, child := featureTag(t, out.FeatureXML, "Main"), featureTag(t, out.FeatureXML, "Sub")
		if !strings.Contains(root, "ConfigurableDirectory='INSTALLDIR'") {
			t.Errorf("%s: the root feature is not configurable: %s", tc.name, root)
		}
		if strings.Contains(child, "ConfigurableDirectory") {
			t.Errorf("%s: the sub-feature must not be configurable: %s", tc.name, child)
		}
	}
}

// featureTag returns the opening <Feature …> tag for the feature with the given title.
func featureTag(t *testing.T, xml, title string) string {
	t.Helper()
	marker := "Title='" + title + "'"
	for _, line := range strings.Split(xml, "\n") {
		if strings.Contains(line, "<Feature ") && strings.Contains(line, marker) {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("no <Feature> titled %q in:\n%s", title, xml)
	return ""
}

// INSTALL_DIR_DIALOG on a package with nothing under INSTALLDIR is reported at build time.
func TestInstallDirDialogWithoutInstallDirWarns(t *testing.T) {
	dir := t.TempDir()
	setup := &ir.Setup{Features: []ir.Feature{{
		Name:  "Main",
		Items: []ir.Item{ir.Registry{File: writeProbeReg(t, dir)}},
	}}}
	out := generate54(t, dir, setup, variables.Dictionary{"INSTALL_DIR_DIALOG": "true"})
	joined := strings.Join(out.Warnings, "\n")
	if !strings.Contains(joined, "INSTALL_DIR_DIALOG") || !strings.Contains(joined, "nothing in this package installs under INSTALLDIR") {
		t.Errorf("want the dialog warning, got: %q", joined)
	}

	// With content under INSTALLDIR the dialog is meaningful and there is no warning.
	if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	setup.Features[0].Items = append(setup.Features[0].Items, ir.Files{Source: "app.txt", Target: "[INSTALLDIR]"})
	out = generate54(t, dir, setup, variables.Dictionary{"INSTALL_DIR_DIALOG": "true", "INSTALLDIR": "Probe54"})
	for _, w := range out.Warnings {
		if strings.Contains(w, "INSTALL_DIR_DIALOG") {
			t.Errorf("unexpected dialog warning with INSTALLDIR content: %s", w)
		}
	}
}
