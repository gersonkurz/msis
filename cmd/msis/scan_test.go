package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// What /SCAN can be pointed at (#69).
func TestScanArguments(t *testing.T) {
	for _, tc := range []struct {
		args    cliArgs
		mustSay string
	}{
		{cliArgs{scan: true, build: true, files: []string{"setup.msis"}}, "add /SBOM"},
		{cliArgs{scan: true, files: []string{"app.msi"}}, "not one"},
		{cliArgs{scan: true, files: []string{"app.msi.vex.cdx.json"}}, "VEX document"},
		{cliArgs{scan: true, files: []string{"app.msi.cdx.json"}}, ""},
		{cliArgs{scan: true, sbom: true, files: []string{"app.msi"}}, ""},
	} {
		err := scanArgsValid(&tc.args)
		if tc.mustSay == "" && err != nil || tc.mustSay != "" && (err == nil || !strings.Contains(err.Error(), tc.mustSay)) {
			t.Errorf("%+v: got %v, want %q", tc.args, err, tc.mustSay)
		}
	}
}

// A bundle's BOM-Link to an MSI document scanned in the same run is covered; any other is named,
// because grype does not follow links. The #fragment addresses a component, not a document.
func TestABOMLinkScannedAlongsideIsCovered(t *testing.T) {
	scanned := map[string]bool{"urn:cdx:aaaa/1": true}
	got := unfollowed([]string{"urn:cdx:aaaa/1", "urn:cdx:aaaa/1#comp", "urn:cdx:aaaa/2", "urn:cdx:bbbb/1"}, scanned)
	if want := []string{"urn:cdx:aaaa/2", "urn:cdx:bbbb/1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("unfollowed %v, want %v", got, want)
	}
}

// #69's review: a link is covered only by a document whose scan SUCCEEDED. A bundle scanned
// before its MSI, whose scan then fails, still names the link.
func TestALinkIsCoveredOnlyByASuccessfulScan(t *testing.T) {
	results := []scanResult{
		{doc: "setup.exe.cdx.json", link: "urn:cdx:bbbb/1"},
		{doc: "app.msi.cdx.json", link: "urn:cdx:aaaa/1", err: errors.New("grype failed")},
	}
	if got := unfollowed([]string{"urn:cdx:aaaa/1"}, coveredLinks(results)); len(got) != 1 {
		t.Errorf("a link to a document whose scan failed counted as covered: %v", got)
	}
	results[1].err = nil
	if got := unfollowed([]string{"urn:cdx:aaaa/1"}, coveredLinks(results)); len(got) != 0 {
		t.Errorf("a link to a document scanned alongside was not covered: %v", got)
	}
}
