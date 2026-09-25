package main

import (
	"errors"
	"os"
	"path/filepath"
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
		{cliArgs{scanDir: "reports", sbom: true, files: []string{"app.msi"}}, "add /SCAN"},
		{cliArgs{scan: true, scanDir: "reports", files: []string{"app.msi.cdx.json"}}, ""},
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

// #70's review: two different documents that would be reported to the same file - one
// /SCAN-DIR for x64\app.msi.cdx.json and x86\app.msi.cdx.json, or names differing only in case
// - are refused before anything is scanned, so no Scan: line can name another document's report.
func TestReportsThatWouldCollideAreRefused(t *testing.T) {
	dir := t.TempDir()
	x64, x86 := filepath.Join(dir, "x64", "app.msi.cdx.json"), filepath.Join(dir, "x86", "APP.MSI.cdx.json")
	err := scanDocuments([]string{x64, x86}, filepath.Join(dir, "reports"))
	if err == nil || !strings.Contains(err.Error(), "would both be reported to") {
		t.Fatalf("colliding reports: got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "reports")); !os.IsNotExist(statErr) {
		t.Error("something was written before the collision was refused")
	}
	// Beside their own documents they do not collide.
	if err := distinctReports([]string{x64, x86}, ""); err != nil {
		t.Errorf("reports beside their documents: %v", err)
	}
}
