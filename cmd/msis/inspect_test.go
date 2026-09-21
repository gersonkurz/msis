package main

import (
	"strings"
	"testing"
)

// TestInspectablePath: /INSPECT reads a built artifact, and the commonest mistake will be
// pointing it at the script instead. The error has to name that rather than surface MSI's
// return code, which says only "1620".
func TestInspectablePath(t *testing.T) {
	for _, ok := range []string{`C:\dist\App.msi`, "app.MSI", "a.b.c-1.0.0.msi"} {
		if err := inspectablePath(ok); err != nil {
			t.Errorf("inspectablePath(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"setup.msis", "setup.exe", "setup", `C:\dist\App.msi.cdx.json`} {
		err := inspectablePath(bad)
		if err == nil {
			t.Errorf("inspectablePath(%q) = nil, want an error", bad)
			continue
		}
		if !strings.Contains(err.Error(), "built .msi") {
			t.Errorf("inspectablePath(%q) error = %v, want it to say what /INSPECT reads", bad, err)
		}
	}
	// The .msis case is the one worth a hint, and the hint must name the way out.
	if err := inspectablePath("setup.msis"); !strings.Contains(err.Error(), "/BUILD") {
		t.Errorf("error for a .msis = %v, want a hint pointing at /BUILD", err)
	}
}

// registryRootName: -1 is not an unknown root, it is "HKCU or HKLM depending on ALLUSERS", and
// rendering it as root-1 would misinform.
func TestRegistryRootName(t *testing.T) {
	cases := map[int]string{
		-1: "HKCU-or-HKLM",
		0:  "HKCR",
		1:  "HKCU",
		2:  "HKLM",
		3:  "HKU",
		9:  "root9",
	}
	for root, want := range cases {
		if got := registryRootName(root); got != want {
			t.Errorf("registryRootName(%d) = %q, want %q", root, got, want)
		}
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 kB"},
		{200_500, "195.8 kB"},
		{1 << 20, "1.0 MB"},
		{9_500_000, "9.1 MB"},
	}
	for _, c := range cases {
		if got := humanSize(c.in); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
