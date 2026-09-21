package main

import (
	"strings"
	"testing"
)

// /SBOM reads a built artifact. Pointing it at the script is the commonest mistake, and the
// error has to name that rather than surface an installer error code.
func TestSbomablePath(t *testing.T) {
	// Both artifacts msis produces are readable since #33: the .msi and the bundle .exe.
	for _, ok := range []string{
		`C:\dist\App.msi`, "app.MSI", "a.b-1.0.0.msi",
		`C:\dist\App-setup.exe`, "setup.EXE",
	} {
		if err := sbomablePath(ok); err != nil {
			t.Errorf("sbomablePath(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"setup.msis", "setup", `C:\dist\App.msi.cdx.json`} {
		err := sbomablePath(bad)
		if err == nil {
			t.Errorf("sbomablePath(%q) = nil, want an error", bad)
			continue
		}
		if !strings.Contains(err.Error(), "/BUILD") {
			t.Errorf("sbomablePath(%q) error = %v, want a hint pointing at /BUILD", bad, err)
		}
	}
	// A .cdx.json is the thing /SBOM writes, not a thing it reads; pointing at it must not
	// be mistaken for the .msi beside it.
	if err := sbomablePath(`C:\dist\App.msi.cdx.json`); err == nil {
		t.Error("a document is not an artifact")
	}
}
