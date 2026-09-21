package main

import (
	"strings"
	"testing"
)

// /SBOM reads a built artifact. Pointing it at the script is the commonest mistake, and the
// error has to name that rather than surface an installer error code. Bundles are a separate
// ticket and the message says so rather than failing obscurely.
func TestSbomablePath(t *testing.T) {
	for _, ok := range []string{`C:\dist\App.msi`, "app.MSI", "a.b-1.0.0.msi"} {
		if err := sbomablePath(ok); err != nil {
			t.Errorf("sbomablePath(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"setup.msis", "setup", `C:\dist\App.msi.cdx.json`} {
		if err := sbomablePath(bad); err == nil {
			t.Errorf("sbomablePath(%q) = nil, want an error", bad)
		}
	}
	// A bundle is not merely unsupported, it is somebody's next ticket; say which.
	err := sbomablePath("setup.exe")
	if err == nil {
		t.Fatal("a bundle must be rejected for now")
	}
	if !strings.Contains(err.Error(), "#33") {
		t.Errorf("error = %v, want it to point at the bundle ticket", err)
	}
}
