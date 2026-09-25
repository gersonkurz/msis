//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gersonkurz/msis/internal/scan"
)

// A document with a component grype's database knows to be vulnerable: x/text before 0.39.0
// (GO-2026-5970, the finding that prompted the 3.0.6 module bump).
const vulnerableDoc = `{
  "bomFormat": "CycloneDX", "specVersion": "1.6",
  "serialNumber": "urn:uuid:3e671687-395b-41f5-a30f-a58921a69b79", "version": 1,
  "metadata": {"component": {"type": "application", "bom-ref": "product", "name": "Product", "version": "1.0"}},
  "components": [
    {"type": "library", "bom-ref": "text", "name": "golang.org/x/text", "version": "v0.20.0",
     "purl": "pkg:golang/golang.org/x/text@v0.20.0"},
    {"type": "file", "bom-ref": "notes", "name": "notes.txt"}
  ]
}`

// #69 with the real grype: /SCAN on a document runs grype, keeps its JSON verbatim beside the
// document, and a second scan keeps the first report rather than overwriting it. grype is
// pinned to its cached database so the test does not depend on the network.
func TestScanRunsGrypeAndKeepsItsReport(t *testing.T) {
	if _, err := exec.LookPath(scan.Scanner); err != nil {
		t.Skip("grype is not on PATH; /SCAN needs it")
	}
	t.Setenv("GRYPE_DB_AUTO_UPDATE", "false")
	t.Setenv("GRYPE_DB_VALIDATE_AGE", "false")
	t.Setenv("GRYPE_CHECK_FOR_APP_UPDATE", "false")

	doc := filepath.Join(t.TempDir(), "app.msi.cdx.json")
	if err := os.WriteFile(doc, []byte(vulnerableDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := scanDocuments([]string{doc}, ""); err != nil {
		t.Fatal(err)
	}
	report, err := os.ReadFile(scan.ReportPath(doc, ""))
	if err != nil {
		t.Fatalf("no report beside the document: %v", err)
	}
	if !strings.Contains(string(report), "GO-2026-5970") {
		t.Errorf("grype's report does not name GO-2026-5970:\n%.600s", report)
	}
	if strings.Contains(string(report), filepath.Dir(doc)) {
		t.Error("the report records the document's directory; grype should be given a relative name")
	}

	if err := scanDocuments([]string{doc}, ""); err != nil {
		t.Fatal(err)
	}
	kept, _ := filepath.Glob(filepath.Join(filepath.Dir(doc), "app.msi.*.grype.json"))
	if len(kept) != 1 {
		t.Errorf("after a second scan, kept %v, want the first report kept by its timestamp", kept)
	}
}

// #69's review, with the real grype: a --fail-on threshold inherited from the user's grype
// configuration makes grype exit 2 WITH its report - findings, not a failure - so the scan
// stands and the report is kept (D19: a finding never fails a run). And a document named in
// another case still finds its VEX sidecar and gets the right report name.
func TestScanWithAThresholdAndAnUpperCaseName(t *testing.T) {
	if _, err := exec.LookPath(scan.Scanner); err != nil {
		t.Skip("grype is not on PATH; /SCAN needs it")
	}
	t.Setenv("GRYPE_DB_AUTO_UPDATE", "false")
	t.Setenv("GRYPE_DB_VALIDATE_AGE", "false")
	t.Setenv("GRYPE_CHECK_FOR_APP_UPDATE", "false")
	t.Setenv("GRYPE_FAIL_ON_SEVERITY", "high")

	dir := t.TempDir()
	doc := filepath.Join(dir, "app.msi.CDX.JSON")
	if err := os.WriteFile(doc, []byte(vulnerableDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	vexSidecar := `{"bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1,
  "metadata": {"component": {"type": "application", "name": "Product",
    "externalReferences": [{"type": "bom", "url": "urn:cdx:3e671687-395b-41f5-a30f-a58921a69b79/1"}]}},
  "vulnerabilities": [{"id": "CVE-2026-56852", "affects": [{"ref": "text"}],
    "analysis": {"state": "not_affected", "justification": "code_not_reachable"}}]}`
	if err := os.WriteFile(filepath.Join(dir, "app.msi.vex.cdx.json"), []byte(vexSidecar), 0o644); err != nil {
		t.Fatal(err)
	}

	res := scanOne(doc, "")
	if res.err != nil {
		t.Fatalf("a findings-only exit failed the scan: %v", res.err)
	}
	if want := filepath.Join(dir, "app.msi.grype.json"); res.out != want {
		t.Errorf("report %s, want %s", res.out, want)
	}
	if len(res.report.Findings) != 1 || res.report.Findings[0].SuppressedBy == "" {
		t.Errorf("findings %+v, want GO-2026-5970 answered by the VEX beside the document (VEX: %s)",
			res.report.Findings, res.report.VEX)
	}
}
