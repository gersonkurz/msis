//go:build windows

package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/msiread"
	"github.com/gersonkurz/msis/internal/sbom"
)

// #64 end to end, with the real wix: MANUFACTURER_URL and MANUFACTURER_EMAIL go INTO the
// installers - the MSI as ARPURLINFOABOUT/ARPCONTACT, the bundle as AboutUrl - and each document
// reads them back out of its artifact as the product creator. SBOM_CREATOR names the documents'
// creator. That the bundle's value comes back at all proves the Burn manifest attribute is the
// one burnread reads.
func TestContactsGoIntoTheInstallersAndComeBackOutOfThem(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "app.txt"), "the application\n")
	write(t, filepath.Join(dir, "stub-vcredist.exe"), "stands in for the VC++ redistributable\n")
	script := scriptFor(t, dir, "app.msi", autoBundleScript)
	buildWithSBOM(t, script, &cliArgs{setOverrides: map[string]string{
		"MANUFACTURER_URL":   "https://acme.example",
		"MANUFACTURER_EMAIL": "support@acme.example",
		"SBOM_CREATOR":       "sbom@acme.example",
	}})

	msiDoc, _ := readDoc(t, filepath.Join(dir, "app.msi"))
	bundleDoc, _ := readDoc(t, filepath.Join(dir, "app.exe"))

	// The MSI carries both; BSI takes the email, and the URL only when there is none.
	wantMSI := &sbom.OrganizationalEntity{Name: "msis tests",
		Contact: []sbom.OrganizationalContact{{Email: "support@acme.example"}}}
	if got := msiDoc.Metadata.Component.Manufacturer; !reflect.DeepEqual(got, wantMSI) {
		t.Errorf("MSI product creator %+v, want %+v (read back from ARPURLINFOABOUT/ARPCONTACT)", got, wantMSI)
	}
	wantBundle := &sbom.OrganizationalEntity{Name: "msis tests", URL: []string{"https://acme.example"}}
	if got := bundleDoc.Metadata.Component.Manufacturer; !reflect.DeepEqual(got, wantBundle) {
		t.Errorf("bundle product creator %+v, want %+v (read back from Arp/@AboutUrl)", got, wantBundle)
	}
	wantCreator := &sbom.OrganizationalEntity{Contact: []sbom.OrganizationalContact{{Email: "sbom@acme.example"}}}
	for name, d := range map[string]*sbom.Document{"MSI": msiDoc, "bundle": bundleDoc} {
		if got := d.Metadata.Manufacturer; !reflect.DeepEqual(got, wantCreator) {
			t.Errorf("%s document's creator %+v, want %+v", name, got, wantCreator)
		}
	}
}

// A malformed contact would be written into the installer and inherited by every document read
// from it, so it stops the build before anything is generated.
func TestAMalformedContactStopsTheBuild(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "app.txt"), "the application\n")
	script := scriptFor(t, dir, "app.msi", autoBundleScript)
	err := processFile(script, &cliArgs{setOverrides: map[string]string{"MANUFACTURER_URL": "acme.example"},
		templateFolder: repoTemplates(t)})
	if err == nil || !strings.Contains(err.Error(), "MANUFACTURER_URL") {
		t.Fatalf("want an error naming MANUFACTURER_URL, got %v", err)
	}
}

// #64's review: a value accepted by the check survives the round trip through the installer.
// Padded values are trimmed before they are written, so they are still a URL when read back; a
// whitespace-only email is unset, so the MSI carries no ARPCONTACT and the URL is the fallback.
func TestAPaddedContactSurvivesTheInstaller(t *testing.T) {
	requireWix(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "app.txt"), "the application\n")
	write(t, filepath.Join(dir, "stub-vcredist.exe"), "stands in for the VC++ redistributable\n")
	script := scriptFor(t, dir, "app.msi", autoBundleScript)
	buildWithSBOM(t, script, &cliArgs{setOverrides: map[string]string{
		"MANUFACTURER_URL":   "  https://acme.example  ",
		"MANUFACTURER_EMAIL": "   ",
	}})
	pkg, err := msiread.Read(filepath.Join(dir, "app.msi"))
	if err != nil {
		t.Fatal(err)
	}
	if got := pkg.Properties["ARPURLINFOABOUT"]; got != "https://acme.example" {
		t.Errorf("ARPURLINFOABOUT = %q, want the trimmed URL", got)
	}
	if _, ok := pkg.Properties["ARPCONTACT"]; ok {
		t.Error("a whitespace-only MANUFACTURER_EMAIL was written as ARPCONTACT")
	}
	doc, _ := readDoc(t, filepath.Join(dir, "app.msi"))
	want := &sbom.OrganizationalEntity{Name: "msis tests", URL: []string{"https://acme.example"}}
	if got := doc.Metadata.Component.Manufacturer; !reflect.DeepEqual(got, want) {
		t.Errorf("product creator %+v, want %+v", got, want)
	}
}
