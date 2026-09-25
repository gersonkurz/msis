package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const summaryDoc = `{"serialNumber": "urn:uuid:11111111-2222-4333-8444-555555555555", "version": 1,
  "metadata": {"component": {"bom-ref": "p", "name": "MSIS", "purl": "pkg:generic/msis@9.9.9"}}, "components": []}`

func summaryFixture(t *testing.T) (dist, scanDir string) {
	t.Helper()
	root := t.TempDir()
	dist, scanDir = filepath.Join(root, "dist"), filepath.Join(root, "scan")
	for _, d := range []string{dist, scanDir, filepath.Join(dist, "components")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{
		"msis-9.9.9-x64.msi": "msi", "msis-9.9.9-x64.msi.cdx.json": summaryDoc,
		"msis-9.9.9.cdx.json": summaryDoc, "build-manifest.json": "{}",
	} {
		if err := os.WriteFile(filepath.Join(dist, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dist, scanDir
}

func writeReport(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(path, []byte(`{"matches": [{"vulnerability": {"id": "X-1"}, "artifact": {"id": "p"}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

// #71: the release ends saying what to upload - the installers, their SBOMs, the release SBOM,
// and nothing else - and whether each SBOM was scanned by THIS release.
func TestTheReleaseSummary(t *testing.T) {
	dist, scanDir := summaryFixture(t)
	writeReport(t, filepath.Join(scanDir, "msis-9.9.9-x64.msi.grype.json"), time.Hour)
	writeReport(t, filepath.Join(scanDir, "msis-9.9.9.grype.json"), -time.Hour) // older than its SBOM: not this run's
	lines, err := releaseSummary("9.9.9", dist, scanDir)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(lines, "\n")
	for _, want := range []string{
		"=== Release 9.9.9: done ===",
		"Upload these 3 files",
		"msis-9.9.9-x64.msi ", "msis-9.9.9-x64.msi.cdx.json", "msis-9.9.9.cdx.json",
		"Scan: 1 of 2 SBOMs scanned: 1 open finding(s), 0 answered by VEX",
		"not scanned: msis-9.9.9.cdx.json",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the summary does not say %q:\n%s", want, text)
		}
	}
	for _, upload := range lines {
		if strings.HasPrefix(upload, "  build-manifest") || strings.HasPrefix(upload, "  components") {
			t.Errorf("an internal file is listed for upload: %q", upload)
		}
	}

	// No report from this release at all: said plainly, not left to scroll by.
	if err := os.RemoveAll(scanDir); err != nil {
		t.Fatal(err)
	}
	lines, _ = releaseSummary("9.9.9", dist, scanDir)
	if text := strings.Join(lines, "\n"); !strings.Contains(text, "Scan: NOT SCANNED") {
		t.Errorf("no reports, yet the summary says:\n%s", text)
	}

	// An installer without its SBOM is an incomplete release.
	if err := os.Remove(filepath.Join(dist, "msis-9.9.9-x64.msi.cdx.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := releaseSummary("9.9.9", dist, scanDir); err == nil || !strings.Contains(err.Error(), "no SBOM") {
		t.Errorf("an installer without an SBOM: got %v", err)
	}
}

// #71's review: the final line carries the scan's coverage limits (D19) - components no scanner
// can match, and BOM-Links to documents this release did not scan - so "0 open findings" is
// never read as a clean bill. A link to a document scanned in the same release is covered.
func TestTheReleaseSummaryStatesWhatTheScanDidNotCover(t *testing.T) {
	dist, scanDir := summaryFixture(t)
	// The installer's SBOM: one unidentified component, a link to the release SBOM (scanned,
	// so covered) and a link to a document nobody scanned.
	installerDoc := `{"serialNumber": "urn:uuid:aaaaaaaa-2222-4333-8444-555555555555", "version": 1,
  "metadata": {"component": {"bom-ref": "p", "name": "MSIS", "purl": "pkg:generic/msis@9.9.9"}},
  "components": [
    {"bom-ref": "notes", "name": "notes.txt"},
    {"bom-ref": "chain", "name": "MSIS", "externalReferences": [
      {"type": "bom", "url": "urn:cdx:11111111-2222-4333-8444-555555555555/1"},
      {"type": "bom", "url": "urn:cdx:dddddddd-2222-4333-8444-555555555555/1#x"}]}]}`
	if err := os.WriteFile(filepath.Join(dist, "msis-9.9.9-x64.msi.cdx.json"), []byte(installerDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	writeReport(t, filepath.Join(scanDir, "msis-9.9.9-x64.msi.grype.json"), time.Hour)
	writeReport(t, filepath.Join(scanDir, "msis-9.9.9.grype.json"), time.Hour)
	lines, err := releaseSummary("9.9.9", dist, scanDir)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(lines, "\n")
	for _, want := range []string{
		"Scan: 2 of 2 SBOMs scanned",
		"not covered: 2 of 4 components carry neither a purl nor a CPE",
		"not covered: urn:cdx:dddddddd-2222-4333-8444-555555555555/1#x",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the summary does not say %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "not covered: urn:cdx:11111111") {
		t.Errorf("a link to a document this release scanned was reported uncovered:\n%s", text)
	}
}
