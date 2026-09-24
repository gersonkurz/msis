package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/ir"
	"github.com/gersonkurz/msis/internal/variables"
)

// #55: an unnamed root directory is the standard folder it sits in - INSTALLDIR with no name
// is C:\Program Files, APPDATADIR with no name is C:\ProgramData - and msis must never put a
// permission component on it. With INSTALLDIR unset msis granted Users full control of
// C:\Program Files; Windows refused (Error 25521) and the install rolled back (seen in T8).

func payload55(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func generate55(t *testing.T, dir string, items []ir.Item, vars variables.Dictionary) *GeneratedOutput {
	t.Helper()
	setup := &ir.Setup{Features: []ir.Feature{{Name: "Main", Enabled: true, Items: items}}}
	out, err := NewContext(setup, vars, dir).Generate()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUnnamedInstallDirGetsNoPermissionComponent(t *testing.T) {
	dir := payload55(t)
	out := generate55(t, dir, []ir.Item{ir.Files{Source: "app.txt", Target: "[INSTALLDIR]"}}, variables.New())

	if !strings.Contains(out.DirectoryXML, "<Directory Id='INSTALLDIR'>") {
		t.Fatalf("expected an unnamed INSTALLDIR (the Program Files folder itself), got:\n%s", out.DirectoryXML)
	}
	if strings.Contains(out.DirectoryXML, "PermissionEx") || strings.Contains(out.DirectoryXML, "CreateFolder") {
		t.Errorf("an unnamed INSTALLDIR is C:\\Program Files; it must get no permission component:\n%s", out.DirectoryXML)
	}
	if !strings.Contains(out.DirectoryXML, "app.txt") {
		t.Errorf("the payload must still be emitted:\n%s", out.DirectoryXML)
	}
}

// The same for an app-data root that resolves to the standard folder: APPDATADIR falls back to
// INSTALLDIR's name, so with neither set it is C:\ProgramData.
func TestUnnamedAppDataDirGetsNoPermissionComponent(t *testing.T) {
	dir := payload55(t)
	out := generate55(t, dir, []ir.Item{ir.Files{Source: "app.txt", Target: "[APPDATADIR]"}}, variables.New())
	if strings.Contains(out.AppDataDirXML, "PermissionEx") {
		t.Errorf("an unnamed APPDATADIR is C:\\ProgramData; it must get no permission component:\n%s", out.AppDataDirXML)
	}
}

// A named root and its named subdirectories keep their permission components, and a named
// subdirectory under an unnamed root still gets one - it is the product's own folder.
func TestNamedDirectoriesKeepTheirPermissionComponents(t *testing.T) {
	dir := payload55(t)

	named := generate55(t, dir, []ir.Item{ir.Files{Source: "app.txt", Target: "[INSTALLDIR]sub"}},
		variables.Dictionary{"INSTALLDIR": "Probe55"})
	if n := strings.Count(named.DirectoryXML, "PermissionEx"); n != 2 {
		t.Errorf("named INSTALLDIR plus a subdirectory: %d permission components, want 2:\n%s", n, named.DirectoryXML)
	}

	unnamedRoot := generate55(t, dir, []ir.Item{ir.Files{Source: "app.txt", Target: "[INSTALLDIR]Vendor"}}, variables.New())
	if n := strings.Count(unnamedRoot.DirectoryXML, "PermissionEx"); n != 1 {
		t.Errorf("unnamed INSTALLDIR with a named subdirectory: %d permission components, want 1 (the subdirectory):\n%s",
			n, unnamedRoot.DirectoryXML)
	}
}
