package wix

import (
	"strings"
	"testing"
)

func TestParseDotnetToolVersion(t *testing.T) {
	const listing = `Package Id      Version      Commands
--------------- ------------ --------
wix             6.0.2        wix
dotnet-ef       8.0.0        dotnet-ef
`
	if got := parseDotnetToolVersion(listing, "wix"); got != "6.0.2" {
		t.Errorf("parseDotnetToolVersion wix = %q, want %q", got, "6.0.2")
	}
	if got := parseDotnetToolVersion(listing, "WIX"); got != "6.0.2" {
		t.Errorf("parseDotnetToolVersion is case-insensitive: got %q", got)
	}
	if got := parseDotnetToolVersion(listing, "dotnet-ef"); got != "8.0.0" {
		t.Errorf("parseDotnetToolVersion dotnet-ef = %q, want %q", got, "8.0.0")
	}
	if got := parseDotnetToolVersion(listing, "missing"); got != "" {
		t.Errorf("parseDotnetToolVersion missing = %q, want empty", got)
	}
	if got := parseDotnetToolVersion("", "wix"); got != "" {
		t.Errorf("parseDotnetToolVersion empty input = %q, want empty", got)
	}
}

func TestExtArgs(t *testing.T) {
	got := extArgs([]string{"A", "B"})
	want := []string{"-ext", "A", "-ext", "B"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("extArgs = %v, want %v", got, want)
	}
	if got := extArgs(nil); len(got) != 0 {
		t.Errorf("extArgs(nil) = %v, want empty", got)
	}
}

// TestAllExtensionsCoversBuildSets guards the single-source-of-truth invariant: every
// extension the build path loads must be installable by EnsureWix.
func TestAllExtensionsCoversBuildSets(t *testing.T) {
	all := make(map[string]bool, len(AllExtensions))
	for _, e := range AllExtensions {
		all[e] = true
	}
	for _, set := range [][]string{msiExtensions, bundleExtensions} {
		for _, e := range set {
			if !all[e] {
				t.Errorf("extension %q used by a build but missing from AllExtensions", e)
			}
		}
	}
}
